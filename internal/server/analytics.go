package server

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/els0r/toilmaster3000/internal/engine"
)

// The four selectable analytics ranges (ADR 0011 / CONTEXT "Time range"). They
// are the wire values of the endpoint's `range` query parameter; huma validates
// the enum, so rangeStart only ever sees one of these.
const (
	rangeToday = "today"
	rangeWeek  = "week"
	rangeMonth = "month"
	rangeDays  = "days"
)

// rangeStart returns the inclusive start instant of the named look-back range,
// computed in now's location (the workstation tz, same local-midnight basis as
// the Approval Feed). The range end is always now, so the handler keeps every
// approval at or after this boundary.
//
//   - today — local midnight of now's day.
//   - week  — ISO-8601 Monday 00:00 of now's week (week start across a month
//     boundary lands on the correct prior-month Monday via calendar arithmetic).
//   - month — the calendar 1st 00:00 of now's month.
//   - days  — a rolling days×24h window ending now (a fixed duration, deliberately
//     not calendar days, so it stays equal-length for the elapsed-aligned delta).
//
// This is the correctness-critical date logic the project keeps server-side and
// table-driven tested; the frontend never recomputes it.
func rangeStart(rng string, days int, now time.Time) time.Time {
	switch rng {
	case rangeWeek:
		midnight := startOfLocalDay(now)
		// time.Weekday is Sunday=0..Saturday=6; (wd+6)%7 is the count of days since
		// the ISO-8601 Monday, so Monday→0 (same day) and Sunday→6.
		sinceMonday := (int(midnight.Weekday()) + 6) % 7
		return midnight.AddDate(0, 0, -sinceMonday)
	case rangeMonth:
		y, m, _ := now.Date()
		return time.Date(y, m, 1, 0, 0, 0, 0, now.Location())
	case rangeDays:
		return now.Add(-time.Duration(days) * 24 * time.Hour)
	case rangeToday:
		return startOfLocalDay(now)
	default: // unreachable once huma validates the enum; defensive — treat as today.
		return startOfLocalDay(now)
	}
}

// prevWindow returns the [start, end) of the elapsed-aligned previous period for
// the named range (ADR 0011): the SAME elapsed slice of the prior period as has
// elapsed in the current one, so an in-progress range is compared like-for-like
// rather than partial-vs-full. The window is half-open — start inclusive, end
// exclusive — so the caller counts approvals at or after start and strictly
// before end.
//
//   - today — yesterday 00:00 → yesterday at now's clock time.
//   - week  — last week's Monday 00:00 → last week at now's weekday + clock
//     offset (the whole window shifted back 7 days).
//   - days  — the immediately-preceding X×24h window: [now-2X·24h, now-X·24h).
//     Equal-length by construction, so it is like-for-like for free.
//
// This is the correctness-critical, table-tested counterpart to rangeStart; the
// frontend never recomputes it.
func prevWindow(rng string, days int, now time.Time) (start, end time.Time) {
	switch rng {
	case rangeWeek:
		start = rangeStart(rangeWeek, days, now).AddDate(0, 0, -7)
		end = now.AddDate(0, 0, -7)
		return start, end
	case rangeDays:
		window := time.Duration(days) * 24 * time.Hour
		end = now.Add(-window)
		start = now.Add(-2 * window)
		return start, end
	case rangeMonth:
		start = rangeStart(rangeMonth, days, now).AddDate(0, -1, 0)
		// The previous end is last month at now's day-of-month + clock time. Building
		// it field-by-field (rather than AddDate'ing the elapsed duration) is what
		// makes the day-of-month explicit, so the clamp below is well-defined.
		y, m, d := now.Date()
		lastY, lastM := y, m-1
		if lastM < time.January {
			lastY, lastM = y-1, time.December
		}
		// Clamp: if now's day-of-month has no counterpart last month (e.g. the 31st
		// against February), cap the previous end at last month's final instant —
		// which is this month's 1st 00:00, the current range start. Without this,
		// time.Date would normalise the overflow forward into the current month.
		if d > daysInMonth(lastY, lastM, now.Location()) {
			end = rangeStart(rangeMonth, days, now)
			return start, end
		}
		end = time.Date(lastY, lastM, d, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), now.Location())
		return start, end
	default: // today
		start = startOfLocalDay(now).AddDate(0, 0, -1)
		end = now.AddDate(0, 0, -1)
		return start, end
	}
}

// daysInMonth returns the number of days in the given year/month. It uses Go's
// date normalisation: day 0 of month+1 is the last day of month, whose Day() is
// the month's length (leap Februaries included).
func daysInMonth(year int, month time.Month, loc *time.Location) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
}

// deltaLabel names the elapsed-aligned slice a range's delta compares against, so
// the period-over-period number reads honestly rather than as a bare percentage
// (ADR 0011). The week/month labels reflect now's position within the period (its
// weekday / day-of-month); today and days are fixed phrasings. It is server-owned
// like the boundary math — the frontend renders the string verbatim.
func deltaLabel(rng string, days int, now time.Time) string {
	switch rng {
	case rangeWeek:
		return "vs last week, Mon–" + now.Weekday().String()[:3] + " aligned"
	case rangeMonth:
		return "vs last month, through the " + ordinal(now.Day())
	case rangeDays:
		unit := "days"
		if days == 1 {
			unit = "day"
		}
		return "vs preceding " + strconv.Itoa(days) + " " + unit
	default: // today
		return "vs yesterday"
	}
}

// ordinal renders n with its English ordinal suffix (1→"1st", 22→"22nd",
// 25→"25th"). The 11–13 teens always take "th" regardless of last digit.
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// Analytics is the wire shape of the Analytics tab's look-back dashboard for a
// range: the auto-vs-human headline split and the context-switches-saved
// headline. Snake_case on the wire per the project's single-convention rule. It
// is computed exclusively from approvals.jsonl (ADR 0009) — no new persistence.
// Slice 1 carries the stats row only; ranges, deltas, the by-type cohort, and
// the scope filter join it in later slices.
type Analytics struct {
	// AutoApproved and HumanReview partition the range's recorded approvals by the
	// matched_rule prefix (ADR 0009): a "human approval: " prefix is Human Review, a
	// human stepping in via the queue; everything else is Auto-approved. The two
	// cover every approval, so their shares sum to 1.
	AutoApproved Stat `json:"auto_approved"`
	HumanReview  Stat `json:"human_review"`
	// SwitchesSaved is the context-switches-saved headline: the raw count of
	// interruptions the robot spared the human, which equals the Auto-approved count
	// (a Human Review approval is a switch the human DID take, so it is not saved).
	SwitchesSaved int `json:"switches_saved"`
	// SwitchesSavedSeries is the per-local-day count of saved switches across the
	// range (oldest day first), backing the switches-saved tile's sparkline (Variant
	// 2). It equals the auto-approved series — a saved switch is an auto-approval —
	// but rides as a flat sibling so the renderer needn't re-derive "switches == auto"
	// to draw its trend, mirroring how SwitchesSaved and SwitchesSavedDelta are flat.
	SwitchesSavedSeries []int `json:"switches_saved_series"`
	// SwitchesSavedMoneyLow and SwitchesSavedMoneyHigh are the money headline as a
	// low/high range derived from the per-switch cost band (ADR 0012): low = count ×
	// CostLow, high = count × CostHigh. They ride alongside the count as flat siblings
	// (like SwitchesSavedDelta) because switches-saved has no Stat share. The server
	// owns the arithmetic; the frontend formats (currency prefix from
	// Assumptions.Currency) and collapses an equal low==high pair to a single figure.
	// There is no hours figure — money no longer flows through hours × rate.
	SwitchesSavedMoneyLow  float64 `json:"switches_saved_money_low"`
	SwitchesSavedMoneyHigh float64 `json:"switches_saved_money_high"`
	// SwitchesSavedDelta is the elapsed-aligned period-over-period change of the
	// switches-saved count (slice 3). It rides alongside the count rather than inside
	// a Stat because switches-saved has no share — and the renderer should not have
	// to re-derive "switches == auto" to find its delta.
	SwitchesSavedDelta Delta `json:"switches_saved_delta"`
	// DeltaLabel names the elapsed-aligned slice every headline delta compares
	// against (e.g. "vs last week, Mon–Thu aligned"). One label for the range, since
	// all three headline deltas share the same previous-period window (ADR 0011).
	DeltaLabel string `json:"delta_label"`
	// Assumptions are the constants the switches-saved time/money figures were
	// computed from (ADR 0010). They ride on the response so the Analytics tab paints
	// the derived figures AND the clickable assumption chip ("× 23 min · $100/hr") in
	// a single fetch; editing them is the separate PUT /settings path, after which the
	// tab re-fetches and the figures recompute.
	Assumptions Assumptions `json:"assumptions"`
}

// Assumptions is the wire shape of the analytics assumption constants (ADR 0010,
// money model revised by ADR 0012): the settings store's per-switch cost band at
// the HTTP boundary, snake_case per the project's single-convention rule. It is
// the GET/PUT /settings body AND the read-only block on the Analytics response
// that names the basis of the money range and feeds the pill's per-switch basis
// sub-label. The huma minimums reject nonsensical edits structurally on the PUT
// path (a zero bound would zero the very headline the feature exists to deliver);
// the cross-field CostHigh >= CostLow check the minimums can't express is enforced
// in the handler.
type Assumptions struct {
	CostLow  int    `json:"cost_low" minimum:"1" doc:"Low CHF estimate of one saved context switch (seeded 10: ~10-min refocus at gross salary)"`
	CostHigh int    `json:"cost_high" minimum:"1" doc:"High CHF estimate of one saved context switch (seeded 26: 23-min flow break at loaded cost)"`
	Currency string `json:"currency" minLength:"1" doc:"Symbol prefixed onto the money figure (seeded \"CHF\")"`
}

// switchesSavedFigures derives the money headline as a low/high range from the
// switches-saved count and the per-switch cost band (ADR 0012): moneyLow = count ×
// CostLow, moneyHigh = count × CostHigh. A zero count (empty range) yields zero of
// both, which the frontend collapses to a single CHF0. This is the pure,
// table-tested arithmetic; the handler reads the band from the settings store and
// the frontend formats and prefixes the currency.
func switchesSavedFigures(count int, a Assumptions) (moneyLow, moneyHigh float64) {
	moneyLow = float64(count) * float64(a.CostLow)
	moneyHigh = float64(count) * float64(a.CostHigh)
	return moneyLow, moneyHigh
}

// The three delta states (ADR 0011). They keep the zero-baseline cases off the
// wire as explicit markers instead of ∞/NaN, so the frontend renders without
// guarding against divide-by-zero artifacts.
const (
	// deltaChanged is a real, finite %-change against a non-zero baseline; Pct
	// carries the signed fraction (which may be exactly 0 for an unchanged count).
	deltaChanged = "changed"
	// deltaNew is a zero baseline with a non-zero current count — growth from
	// nothing, which has no finite percentage; the frontend renders it as "new".
	deltaNew = "new"
	// deltaNone is both counts zero — nothing happened either period, so there is
	// no comparison to make; the frontend renders it as "—".
	deltaNone = "none"
)

// Delta is a headline count's elapsed-aligned period-over-period change (ADR
// 0011). State classifies the comparison (changed | new | none); Pct is the
// signed fraction (now-prev)/prev, meaningful only when State is "changed" and 0
// otherwise. The aligned-comparison label lives once on Analytics (DeltaLabel),
// not here, since all headline deltas share the same window.
type Delta struct {
	Pct   float64 `json:"pct"`
	State string  `json:"state"`
}

// computeDelta returns the elapsed-aligned change of a current count against its
// aligned previous count: a finite signed fraction against a non-zero baseline,
// "new" when the baseline is zero but the current count is not, and "none" when
// both are zero — never a divide-by-zero (ADR 0011).
func computeDelta(now, prev int) Delta {
	switch {
	case prev == 0 && now == 0:
		return Delta{State: deltaNone}
	case prev == 0:
		return Delta{State: deltaNew}
	default:
		return Delta{Pct: float64(now-prev) / float64(prev), State: deltaChanged}
	}
}

// Stat is one headline stat: a count, its share of the range total (a 0..1
// fraction the frontend formats as a percentage), and the count's elapsed-aligned
// period-over-period Delta (slice 3). An empty range yields a 0 share with no
// divide-by-zero. The share is shown for the current range but never delta'd — a
// share-point delta beside a count delta misreads (CONTEXT "Stats row").
type Stat struct {
	Count int     `json:"count"`
	Share float64 `json:"share"`
	Delta Delta   `json:"delta"`
	// Series is the per-local-day count across the range (oldest day first), backing
	// the headline tile's sparkline (Variant 2). One bucket per calendar day the
	// range touches; an empty range yields a zero-filled series, never an empty one,
	// so the sparkline always has bars to lay out.
	Series []int `json:"series"`
}

// dayIndex returns the count of local calendar days from start's day to t's day
// (0 when they share a day, 1 for the next day, …). Both ends are normalised to
// their local midnight and the span is rounded to whole days, so a DST boundary
// (a 23h or 25h "day") still resolves to the correct integer offset rather than
// drifting by one. It is the bucket index for dailySeries.
func dayIndex(start, t time.Time) int {
	from := startOfLocalDay(start)
	to := startOfLocalDay(t)
	return int(math.Round(to.Sub(from).Hours() / 24))
}

// dailySeries buckets a range's approvals into per-local-day counts of each
// headline (auto vs human by the matched_rule prefix, ADR 0009), oldest day
// first, to back the Variant-2 per-tile sparklines. Bucket i is the local day
// startOfLocalDay(start)+i days; the series length covers every calendar day the
// window [start, now] touches, so an empty window still yields a zero-filled
// series (never an empty sparkline) of the right length. Approvals outside the
// window are ignored defensively — the caller pre-scopes via inWindow, so this
// only re-buckets what is already in range. Switches-saved reuses the auto series
// (a saved switch is an auto-approval), so it is not returned separately here.
func dailySeries(approvals []engine.Approval, start, now time.Time) (autoSeries, humanSeries []int) {
	n := max(dayIndex(start, now)+1, 1)
	autoSeries = make([]int, n)
	humanSeries = make([]int, n)
	for _, a := range approvals {
		i := dayIndex(start, a.ApprovedAt)
		if i < 0 || i >= n {
			continue
		}
		if strings.HasPrefix(a.MatchedRule, engine.ManualApprovalPrefix) {
			humanSeries[i]++
		} else {
			autoSeries[i]++
		}
	}
	return autoSeries, humanSeries
}

// aggregateAnalytics computes the slice-1 stats row from the range's approvals:
// it partitions them into Auto-approved vs Human Review by the matched_rule
// prefix (ADR 0009), derives each side's share of the total, and reports
// switches-saved as the auto count. The caller scopes the slice to the range
// (today, for slice 1) before calling; this function is the pure, table-tested
// partition.
func aggregateAnalytics(approvals []engine.Approval) Analytics {
	var autoCount, humanCount int
	for _, a := range approvals {
		if strings.HasPrefix(a.MatchedRule, engine.ManualApprovalPrefix) {
			humanCount++
		} else {
			autoCount++
		}
	}
	total := autoCount + humanCount
	return Analytics{
		AutoApproved:  Stat{Count: autoCount, Share: share(autoCount, total)},
		HumanReview:   Stat{Count: humanCount, Share: share(humanCount, total)},
		SwitchesSaved: autoCount,
	}
}

// inWindow returns the approvals whose ApprovedAt falls in the half-open window
// [start, end) — at or after start, strictly before end. Both the current range
// ([rangeStart, now]) and the aligned previous period ([prevWindow start, end))
// scope through this one filter, so the read boundary is computed in exactly one
// place. The engine keeps the full log; this is a read-side concern only.
func inWindow(approvals []engine.Approval, start, end time.Time) []engine.Approval {
	out := make([]engine.Approval, 0, len(approvals))
	for _, a := range approvals {
		if a.ApprovedAt.Before(start) || !a.ApprovedAt.Before(end) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// withDeltas attaches the elapsed-aligned period deltas (slice 3) to a
// current-range aggregate by comparing each headline count against the SAME
// aggregate computed over the aligned previous-period window, and stamps the
// shared comparison label. Shares are not delta'd. The previous aggregate is just
// aggregateAnalytics over the prior window, so the partition logic is shared and
// only the boundary math (prevWindow) is slice-3-specific.
func withDeltas(cur, prev Analytics, label string) Analytics {
	cur.AutoApproved.Delta = computeDelta(cur.AutoApproved.Count, prev.AutoApproved.Count)
	cur.HumanReview.Delta = computeDelta(cur.HumanReview.Count, prev.HumanReview.Count)
	cur.SwitchesSavedDelta = computeDelta(cur.SwitchesSaved, prev.SwitchesSaved)
	cur.DeltaLabel = label
	return cur
}

// share returns n's fraction of total as a 0..1 value, guarding the empty range
// (total 0) against a divide-by-zero by reporting 0.
func share(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}
