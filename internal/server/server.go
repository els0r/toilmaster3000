// Package server composes the toilmaster3000 HTTP surface: the huma v2 JSON
// API under /api/toilmaster3000/v1 plus the embedded React SPA. It is a thin
// read layer over the engine; every handler is a locked read on the engine's
// store.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/els0r/toilmaster3000/internal/conventionalcommit"
	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
)

// APIPrefix is the verbose-but-unambiguous mount point for the JSON API.
const APIPrefix = "/api/toilmaster3000/v1"

// CycleStatus is the wire shape of the Cycle status line. Snake_case on the
// wire per the project's single-convention rule.
type CycleStatus struct {
	LastRun       *time.Time `json:"last_run"`
	Outcome       string     `json:"outcome"`
	ApprovedCount int        `json:"approved_count"`
	QueueCount    int        `json:"queue_count"`
	DroppedCount  int        `json:"dropped_count"`
}

// TitleParts is the server-parsed conventional-commit title, computed once on
// the backend (the Go parser of record) and shipped on the wire so the frontend
// never parses a title itself (ADR 0006). It rides both Approval and QueueItem
// alongside the unchanged raw title. A non-conventional title is the failed-parse
// case: TitleParts is the zero value and the frontend falls back to the raw
// title. Snake_case on the wire per the project's single-convention rule.
type TitleParts struct {
	Type        string   `json:"type"`
	Scopes      []string `json:"scopes"`
	Breaking    bool     `json:"breaking"`
	Description string   `json:"description"`
}

// titleParts parses a raw PR title into its wire TitleParts. A non-conventional
// title (Parse ok=false) yields the zero value, the failed-parse signal the
// frontend renders the raw title for. The parser returns a single verbatim scope
// (possibly comma/slash separated); splitScopes turns it into the wire's scope
// list.
func titleParts(title string) TitleParts {
	c, ok := conventionalcommit.Parse(title)
	if !ok {
		return TitleParts{}
	}
	return TitleParts{
		Type:        c.Type,
		Scopes:      splitScopes(c.Scope),
		Breaking:    c.Breaking,
		Description: c.Description,
	}
}

// splitScopes splits the parser's verbatim scope (e.g. "api", "deps,ci" or
// "team/service-a") on commas and slashes, trims each part, and drops
// empties. An empty scope yields an empty slice — the wire's "no scopes" value.
func splitScopes(scope string) []string {
	parts := strings.FieldsFunc(scope, func(r rune) bool {
		return r == ',' || r == '/'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Approval is the wire shape of one Approval Feed entry. Snake_case mirrors the
// on-disk approvals.jsonl record exactly, plus the derived TitleParts and Manual
// flag (neither persisted; both computed parse-on-read in approvalToBody).
type Approval struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	TitleParts  TitleParts `json:"title_parts"`
	Author      string     `json:"author"`
	URL         string     `json:"url"`
	MatchedRule string     `json:"matched_rule"`
	// Manual marks a human override approve from the Needs-Human-Review queue
	// (matched_rule carries engine.ManualApprovalPrefix). Derived on the wire so
	// the frontend renders the "manual override" badge without string-sniffing the
	// matched_rule itself.
	Manual bool `json:"manual"`
	// State is the PR's live GitHub lifecycle (open|merged|closed|unknown), the
	// Approval Feed's colored top-bar signal. Sourced from the engine's volatile
	// in-memory map (never persisted), not from the approvals.jsonl record;
	// "unknown" is the default before the first out-of-band refresh. The enum tag
	// makes the generated frontend type an exhaustive union.
	State      string    `json:"state" enum:"open,merged,closed,unknown"`
	ApprovedAt time.Time `json:"approved_at"`
}

// QueueItem is the wire shape of one Needs-Human-Review queue entry: a PR routed
// here for one or more reasons (carried as a list). Snake_case mirrors the
// engine's QueueItem exactly, plus the derived TitleParts (computed
// parse-on-read in queueItemToBody).
type QueueItem struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	TitleParts TitleParts `json:"title_parts"`
	Author     string     `json:"author"`
	URL        string     `json:"url"`
	// Additions, Deletions, and ChangedFiles are the PR's diff magnitude, surfaced
	// on the queue so a human can tell a small fix from a large refactor. The feed
	// deliberately omits them (diff size is noise there).
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
	ChangedFiles int      `json:"changed_files"`
	Reasons      []string `json:"reasons"`
}

// cycleStatusToBody converts the engine's internal cycle status to its wire
// DTO. The engine type carries no json tags (it is internal); the wire shape is
// owned here. See ADR 0002.
func cycleStatusToBody(s engine.Status) CycleStatus {
	return CycleStatus{
		LastRun:       s.LastRun,
		Outcome:       s.Outcome,
		ApprovedCount: s.ApprovedCount,
		QueueCount:    s.QueueCount,
		DroppedCount:  s.DroppedCount,
	}
}

// approvalToBody converts an engine Approval (whose json tags serve the
// approvals.jsonl disk format) to its wire DTO. Identical disk/wire fields today
// is deliberate decoupling, not redundancy — see ADR 0002. The PR State is the
// one wire field NOT sourced from the record: it is the volatile lifecycle the
// caller looks up in the engine's in-memory map and passes in. An empty state
// (the engine has not refreshed this PR yet) becomes the neutral "unknown" — PR
// State is never guessed.
func approvalToBody(a engine.Approval, state github.PRState) Approval {
	if state == "" {
		state = github.PRStateUnknown
	}
	return Approval{
		Number:      a.Number,
		Title:       a.Title,
		TitleParts:  titleParts(a.Title),
		Author:      a.Author,
		URL:         a.URL,
		MatchedRule: a.MatchedRule,
		Manual:      strings.HasPrefix(a.MatchedRule, engine.ManualApprovalPrefix),
		State:       string(state),
		ApprovedAt:  a.ApprovedAt,
	}
}

// queueItemToBody converts an engine QueueItem to its wire DTO. See ADR 0002
// for why the engine type does not cross the boundary directly.
func queueItemToBody(q engine.QueueItem) QueueItem {
	return QueueItem{
		Number:       q.Number,
		Title:        q.Title,
		TitleParts:   titleParts(q.Title),
		Author:       q.Author,
		URL:          q.URL,
		Additions:    q.Additions,
		Deletions:    q.Deletions,
		ChangedFiles: q.ChangedFiles,
		Reasons:      q.Reasons,
	}
}

// FileDiff is the wire shape of one changed file in a PR's diff (the Diff card):
// the path, its status, the per-file changed-line counts, and the unified-diff
// patch. Patch is empty for files GitHub omits the patch for (binary/over-large);
// the card renders those as "no preview" with an Open-on-GitHub escape hatch.
type FileDiff struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

// PRDiffBody is the wire shape of one queued PR's diff: the changed files fetched
// (at most one page) plus TotalFiles, the PR's authoritative changed_files count.
// The card compares len(Files) against TotalFiles to render "first N of M files".
type PRDiffBody struct {
	Files      []FileDiff `json:"files"`
	TotalFiles int        `json:"total_files"`
}

// fileDiffToBody converts a github.FileDiff to its wire DTO (ADR 0002 — the
// github seam type does not cross the boundary directly).
func fileDiffToBody(f github.FileDiff) FileDiff {
	return FileDiff{
		Filename:  f.Filename,
		Status:    f.Status,
		Additions: f.Additions,
		Deletions: f.Deletions,
		Patch:     f.Patch,
	}
}

// startOfLocalDay returns local midnight (00:00:00) of t's day in t's location —
// the inclusive cutoff for the today-scoped Approval Feed. Comparison against it
// is instant-based (time.Time.Before), so a record's own offset is irrelevant.
func startOfLocalDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

type statusOutput struct {
	Body CycleStatus
}

type approvalsOutput struct {
	Body []Approval
}

type queueOutput struct {
	Body []QueueItem
}

type queueDiffOutput struct {
	Body PRDiffBody
}

// analyticsInput carries the time-picker selection. `range` is the selectable
// window (huma enforces the enum, defaulting to today so a bare request still
// works); `days` is the custom rolling-window length, structurally bounded to >= 1
// and semantically required only for range=days (guarded in the handler).
type analyticsInput struct {
	Range string `query:"range" enum:"today,week,month,days" default:"today" doc:"Look-back window: today | week (ISO Monday) | month (calendar 1st) | days (rolling)"`
	Days  int    `query:"days" minimum:"1" doc:"Rolling-window length in days; required for range=days"`
}

type analyticsOutput struct {
	Body Analytics
}

// manualApproveOutput is the success body of a manual queue approval: a simple
// ok marker plus the approved PR number.
type manualApproveOutput struct {
	Body struct {
		OK     bool `json:"ok"`
		Number int  `json:"number"`
	}
}

// RuleBody is the wire shape of a rule for the CRUD API: snake_case, mirroring
// rule.Rule's json tags. Only Name is structurally required; every predicate
// field is optional so a client need only send what it constrains. The id is
// generated on create and taken from the path on update, so it is never part of
// the request body (it appears only in responses).
//
// Semantic validation — reject-empty-rule and regex-compiles — lives in
// rule.Validate, not the huma schema (per ADR 0001).
type RuleBody struct {
	ID   string `json:"id,omitempty" readOnly:"true" doc:"Stable generated id (response only)"`
	Name string `json:"name" doc:"Editable rule name"`
	// Class is the Rule class: "review" routes matches to Needs-Human-Review;
	// anything else (including absent) is an Approve Rule. omitempty + no enum so
	// an absent class is still accepted and read as approve on input; toRule
	// defaults it to "approve" before persisting, so the stored rule always has an
	// explicit class.
	Class              string   `json:"class,omitempty" doc:"Rule class: approve (default) or review"`
	Enabled            bool     `json:"enabled,omitempty" doc:"Whether the rule contributes matches"`
	AuthorsInclude     []string `json:"authors_include,omitempty"`
	AuthorsExclude     []string `json:"authors_exclude,omitempty"`
	TypeInclude        string   `json:"type_include,omitempty"`
	TypeExclude        string   `json:"type_exclude,omitempty"`
	ScopeInclude       string   `json:"scope_include,omitempty"`
	ScopeExclude       string   `json:"scope_exclude,omitempty"`
	DescriptionInclude string   `json:"description_include,omitempty"`
	DescriptionExclude string   `json:"description_exclude,omitempty"`
	// DiffMin/DiffMax bound a PR's total changed lines. minimum:"0" lets huma
	// reject a negative bound structurally on the wire; 0 means unconstrained.
	DiffMin int `json:"diff_min,omitempty" minimum:"0" doc:"Lower diff-size bound (additions+deletions); 0 = unconstrained"`
	DiffMax int `json:"diff_max,omitempty" minimum:"0" doc:"Upper diff-size bound (additions+deletions); 0 = unconstrained"`
}

// toRule converts an inbound RuleBody to a rule.Rule for the store. The id is
// not carried from the body: Create generates it and Update takes it from the
// path.
func (b RuleBody) toRule() rule.Rule {
	// An absent class reads as approve; default it to an explicit "approve" so the
	// persisted rule (and a PUT round-trip) always carries an explicit class.
	class := b.Class
	if class == "" {
		class = "approve"
	}
	return rule.Rule{
		Name:               b.Name,
		Class:              class,
		Enabled:            b.Enabled,
		AuthorsInclude:     b.AuthorsInclude,
		AuthorsExclude:     b.AuthorsExclude,
		TypeInclude:        b.TypeInclude,
		TypeExclude:        b.TypeExclude,
		ScopeInclude:       b.ScopeInclude,
		ScopeExclude:       b.ScopeExclude,
		DescriptionInclude: b.DescriptionInclude,
		DescriptionExclude: b.DescriptionExclude,
		DiffMin:            b.DiffMin,
		DiffMax:            b.DiffMax,
	}
}

// ruleToBody converts a stored rule.Rule to its wire RuleBody (with id) for
// responses.
func ruleToBody(r rule.Rule) RuleBody {
	return RuleBody{
		ID:                 r.ID,
		Name:               r.Name,
		Class:              r.Class,
		Enabled:            r.Enabled,
		AuthorsInclude:     r.AuthorsInclude,
		AuthorsExclude:     r.AuthorsExclude,
		TypeInclude:        r.TypeInclude,
		TypeExclude:        r.TypeExclude,
		ScopeInclude:       r.ScopeInclude,
		ScopeExclude:       r.ScopeExclude,
		DescriptionInclude: r.DescriptionInclude,
		DescriptionExclude: r.DescriptionExclude,
		DiffMin:            r.DiffMin,
		DiffMax:            r.DiffMax,
	}
}

// rulesOutput wraps a rule list for huma; bodies marshal via RuleBody's
// snake_case json tags, keeping the wire convention consistent.
type rulesOutput struct {
	Body []RuleBody
}

// ruleOutput wraps a single rule (create/update responses).
type ruleOutput struct {
	Body RuleBody
}

// ruleInput is the create request body: a rule without an id.
type ruleInput struct {
	Body RuleBody
}

// ruleIDPath is the {id} path parameter shared by PUT and DELETE.
type ruleIDPath struct {
	ID string `path:"id" doc:"Stable rule id"`
}

// ruleHTTPError maps a service-layer rule error to a huma HTTP error: the
// semantic-validation failures and unknown-id become 4xx with the offending
// message surfaced to the client; anything else is a 500. It returns nil when
// err is nil.
func ruleHTTPError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, rule.ErrEmptyRule), errors.Is(err, rule.ErrInvalidRegex), errors.Is(err, rule.ErrInvalidDiffRange):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, rule.ErrRuleNotFound):
		return huma.Error404NotFound(err.Error())
	default:
		return huma.Error500InternalServerError(err.Error())
	}
}

// New builds the composed HTTP handler over the given engine. spa is the
// filesystem rooted at the built frontend (an index.html at its root); in
// production it is the go:embed'd frontend/dist. The engine is the only
// injected dependency tests substitute (via its fake GitHubClient and a
// temp-dir state path), so all backend behaviour is asserted through this HTTP
// surface.
func New(spa fs.FS, eng *engine.Engine, rules *rule.Store) (http.Handler, error) {
	mux := http.NewServeMux()
	api := humago.New(mux, Config())
	RegisterAPI(api, eng, rules)

	spaHandler, err := newSPAHandler(spa)
	if err != nil {
		return nil, err
	}
	// Least-specific pattern: the typed API and huma's /openapi, /docs routes
	// are registered above with more specific patterns and win over this.
	mux.Handle("/", spaHandler)

	return mux, nil
}

// Config is the shared huma configuration used by both the live server (New)
// and the OpenAPI spec generator (cmd/openapigen). Sharing it is what keeps the
// committed openapi.json byte-identical to the document the binary serves at
// /openapi.json.
func Config() huma.Config {
	return huma.DefaultConfig("toilmaster3000", "0.1.0")
}

// RegisterAPI registers every typed operation on api. Both New and
// cmd/openapigen call it, so the generated spec stays in sync with the served
// one by construction. The handler closures capture eng/rules but are never
// invoked during spec generation, so passing a nil engine/rules is safe there.
func RegisterAPI(api huma.API, eng *engine.Engine, rules *rule.Store) {
	huma.Register(api, huma.Operation{
		OperationID: "get-status",
		Method:      http.MethodGet,
		Path:        APIPrefix + "/status",
		Summary:     "Cycle status line",
	}, func(_ context.Context, _ *struct{}) (*statusOutput, error) {
		return &statusOutput{Body: cycleStatusToBody(eng.Status())}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-approvals",
		Method:      http.MethodGet,
		Path:        APIPrefix + "/approvals",
		Summary:     "Approval Feed, newest-first",
	}, func(_ context.Context, _ *struct{}) (*approvalsOutput, error) {
		feed := eng.Approvals()
		// PR State is volatile and lives outside the approval record: read the
		// engine's in-memory snapshot once and zip each entry's live lifecycle into
		// the wire DTO (a missing entry becomes "unknown" in approvalToBody).
		states := eng.PRStates()
		// Today-scoped: the feed answers "what did the robot do today," so only
		// approvals at or after local midnight (workstation tz) cross the wire. The
		// engine keeps the full feed as its dedup/restart truth; the scoping is a
		// read-boundary concern, so it lives here, not in the engine. The seeded
		// historical approvals (past timestamps) live on only in the dedup set.
		cutoff := startOfLocalDay(time.Now())
		out := make([]Approval, 0, len(feed))
		for _, a := range feed {
			if a.ApprovedAt.Before(cutoff) {
				continue
			}
			out = append(out, approvalToBody(a, states[a.Number]))
		}
		return &approvalsOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-queue",
		Method:      http.MethodGet,
		Path:        APIPrefix + "/queue",
		Summary:     "Needs-Human-Review queue (derived live)",
	}, func(_ context.Context, _ *struct{}) (*queueOutput, error) {
		items := eng.Queue()
		out := make([]QueueItem, 0, len(items))
		for _, q := range items {
			out = append(out, queueItemToBody(q))
		}
		return &queueOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "approve-queue-item",
		Method:      http.MethodPost,
		Path:        APIPrefix + "/queue/{number}/approve",
		Summary:     "Manual override approve (the only path to approve a breaking change)",
	}, func(ctx context.Context, in *struct {
		Number int `path:"number" doc:"PR number to approve from the queue"`
	}) (*manualApproveOutput, error) {
		if err := eng.ApproveManually(ctx, in.Number); err != nil {
			if errors.Is(err, engine.ErrNotInQueue) {
				return nil, huma.Error404NotFound(err.Error())
			}
			return nil, huma.Error500InternalServerError(err.Error())
		}
		out := &manualApproveOutput{}
		out.Body.OK = true
		out.Body.Number = in.Number
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-queue-diff",
		Method:      http.MethodGet,
		Path:        APIPrefix + "/queue/{number}/diff",
		Summary:     "Fetch a queued PR's diff on demand (the Diff card)",
	}, func(ctx context.Context, in *struct {
		Number int `path:"number" doc:"PR number whose diff to fetch from the queue"`
	}) (*queueDiffOutput, error) {
		files, total, err := eng.Diff(ctx, in.Number)
		if err != nil {
			if errors.Is(err, engine.ErrNotInQueue) {
				return nil, huma.Error404NotFound(err.Error())
			}
			return nil, huma.Error500InternalServerError(err.Error())
		}
		out := make([]FileDiff, 0, len(files))
		for _, f := range files {
			out = append(out, fileDiffToBody(f))
		}
		return &queueDiffOutput{Body: PRDiffBody{Files: out, TotalFiles: total}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-analytics",
		Method:      http.MethodGet,
		Path:        APIPrefix + "/analytics",
		Summary:     "Approval-history analytics for a selectable range (slice 2: time picker)",
	}, func(_ context.Context, in *analyticsInput) (*analyticsOutput, error) {
		// huma validates `range` (enum) and `days` (>= 1 when present) structurally;
		// the days-range needs a day count, which huma can't make conditionally
		// required, so guard it here (ADR 0011 / CONTEXT "Time range").
		if in.Range == rangeDays && in.Days < 1 {
			return nil, huma.Error422UnprocessableEntity("days is required and must be >= 1 for the days range")
		}
		// The range runs [cutoff, now]; the engine keeps the full log as its dedup/
		// restart truth, so scoping to the range is a read-boundary concern that lives
		// here. The correctness-critical boundary math is the table-tested rangeStart /
		// prevWindow; this handler only filters and composes.
		now := time.Now()
		feed := eng.Approvals()
		cur := aggregateAnalytics(inWindow(feed, rangeStart(in.Range, in.Days, now), now))
		// Slice 3: the elapsed-aligned previous period is compared like-for-like (only
		// the same elapsed slice of the prior period, not partial-vs-full — ADR 0011),
		// so each headline carries an honest period-over-period delta.
		prevStart, prevEnd := prevWindow(in.Range, in.Days, now)
		prev := aggregateAnalytics(inWindow(feed, prevStart, prevEnd))
		body := withDeltas(cur, prev, deltaLabel(in.Range, in.Days, now))
		return &analyticsOutput{Body: body}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-rules",
		Method:      http.MethodGet,
		Path:        APIPrefix + "/rules",
		Summary:     "List rules in file order",
	}, func(_ context.Context, _ *struct{}) (*rulesOutput, error) {
		list := rules.List()
		out := make([]RuleBody, 0, len(list))
		for _, r := range list {
			out = append(out, ruleToBody(r))
		}
		return &rulesOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-rule",
		Method:        http.MethodPost,
		Path:          APIPrefix + "/rules",
		Summary:       "Create a rule",
		DefaultStatus: http.StatusCreated,
	}, func(_ context.Context, in *ruleInput) (*ruleOutput, error) {
		created, err := rules.Create(in.Body.toRule())
		if err != nil {
			return nil, ruleHTTPError(err)
		}
		return &ruleOutput{Body: ruleToBody(created)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-rule",
		Method:      http.MethodPut,
		Path:        APIPrefix + "/rules/{id}",
		Summary:     "Full-replace a rule (also enable/disable)",
	}, func(_ context.Context, in *struct {
		ID   string `path:"id" doc:"Stable rule id"`
		Body RuleBody
	}) (*ruleOutput, error) {
		updated, err := rules.Update(in.ID, in.Body.toRule())
		if err != nil {
			return nil, ruleHTTPError(err)
		}
		return &ruleOutput{Body: ruleToBody(updated)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-rule",
		Method:        http.MethodDelete,
		Path:          APIPrefix + "/rules/{id}",
		Summary:       "Delete a rule",
		DefaultStatus: http.StatusNoContent,
	}, func(_ context.Context, in *ruleIDPath) (*struct{}, error) {
		if err := rules.Delete(in.ID); err != nil {
			return nil, ruleHTTPError(err)
		}
		return nil, nil
	})
}

// newSPAHandler serves static assets from spa and falls back to its index.html
// shell for any path that isn't a real file, so client-side routing works on
// deep-link refreshes.
func newSPAHandler(spa fs.FS) (http.Handler, error) {
	index, err := fs.ReadFile(spa, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read SPA shell (was frontend built?): %w", err)
	}
	fileServer := http.FileServerFS(spa)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if f, err := spa.Open(name); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	}), nil
}
