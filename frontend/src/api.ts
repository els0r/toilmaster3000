// API client for the toilmaster3000 backend. The wire types are generated from
// the backend's OpenAPI spec (src/api/schema.d.ts, produced by `make generate`),
// so the snake_case shapes track the Go DTOs automatically — one convention
// across the boundary, enforced rather than mirrored by hand.

import type { components } from "./api/schema";

export const API_BASE = "/api/toilmaster3000/v1";

export type CycleStatus = components["schemas"]["CycleStatus"];

export async function fetchStatus(): Promise<CycleStatus> {
  const resp = await fetch(`${API_BASE}/status`);
  if (!resp.ok) {
    throw new Error(`status request failed: ${resp.status}`);
  }
  return resp.json();
}

// Approval is one entry in the read-only Approval Feed (generated from the
// backend's Approval DTO). Every entry carries a GitHub url.
export type Approval = components["schemas"]["Approval"];

export async function fetchApprovals(): Promise<Approval[]> {
  const resp = await fetch(`${API_BASE}/approvals`);
  if (!resp.ok) {
    throw new Error(`approvals request failed: ${resp.status}`);
  }
  return resp.json();
}

// Analytics is the look-back dashboard payload (generated from the backend's
// Analytics DTO): the auto-vs-human headline split (each a count + its 0..1
// share of the range total) and the context-switches-saved count. Computed
// server-side from the approval history (ADR 0009); the frontend is a pure
// renderer. Slice 1 is today-scoped with no ranges, deltas, or cohort yet.
export type Analytics = components["schemas"]["Analytics"];

// Delta is a headline count's elapsed-aligned period-over-period change (slice 3):
// a signed fraction `pct` plus a `state` — "changed" (finite %), "new" (zero
// baseline, no finite %), or "none" (both periods empty). The server owns the
// boundary math and the zero-baseline classification (ADR 0011); the frontend
// only formats the badge, so no ∞/NaN can reach the renderer.
export type Delta = components["schemas"]["Delta"];

// TypeCohortRow is one row of the By-Type cohort (generated from the backend's
// TypeCohortRow DTO): a conventional-commit type, its count of the range's
// approvals, that count's 0..1 share of the range total, and the auto/human split
// of the bucket — the actionable signal being which types still pull a human in.
// The server emits the fixed Conventional Commits axis plus a trailing "other",
// every row present and in spec order, so the frontend renders a stable table.
export type TypeCohortRow = components["schemas"]["TypeCohortRow"];

// AnalyticsRange is the time-picker selection: the four selectable look-back
// windows (CONTEXT "Time range"). The correctness-critical boundary math lives
// server-side; the client only names the window (and, for `days`, its length).
export type AnalyticsRange = "today" | "week" | "month" | "days";

// fetchAnalytics pulls the Analytics tab's aggregates for the selected range.
// Unlike the feed/queue/status endpoints, this is fetched on tab-open and control
// changes (debounced), not on the 10s poll, so the dashboard's lean-back cadence
// stays off the live timer. `days` rides the wire only for the `days` range (the
// other three derive their bounds entirely server-side).
export async function fetchAnalytics(
  range: AnalyticsRange = "today",
  days = 7,
): Promise<Analytics> {
  const params = new URLSearchParams({ range });
  if (range === "days") {
    params.set("days", String(days));
  }
  const resp = await fetch(`${API_BASE}/analytics?${params}`);
  if (!resp.ok) {
    throw new Error(`analytics request failed: ${resp.status}`);
  }
  return resp.json();
}

// Settings is the analytics assumption constants (generated from the backend's
// Assumptions DTO): the per-switch cost band cost_low / cost_high and currency
// (ADR 0012). They drive the switches-saved money range and are surfaced as the
// money pill's read-only per-switch basis. The same shape rides on the Analytics
// response's `assumptions` block, so the pill paints from the analytics fetch and
// is edited through updateSettings (PUT /settings).
export type Settings = components["schemas"]["Assumptions"];

// fetchSettings reads the persisted assumption constants for the Settings tab's
// editor form. The Analytics tab does not call this — it reads the same constants
// off its own analytics response's `assumptions` block (one fetch paints the
// figures and the read-only money pill); this endpoint is the editable resource.
export async function fetchSettings(): Promise<Settings> {
  const resp = await fetch(`${API_BASE}/settings`);
  if (!resp.ok) {
    throw new Error(`settings request failed: ${resp.status}`);
  }
  return resp.json();
}

// updateSettings full-replaces the assumption constants (the Settings tab Save).
// The server persists them to .config/settings.yaml and returns the stored
// values; the Analytics tab recomputes its figures on its next fetch. Throws the
// server's message on a validation failure (e.g. a zero minutes-per-switch).
export async function updateSettings(settings: Settings): Promise<Settings> {
  const resp = await fetch(`${API_BASE}/settings`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(settings),
  });
  if (!resp.ok) {
    throw new Error(await extractError(resp, "update settings failed"));
  }
  return resp.json();
}

// QueueItem is a Needs-Human-Review entry (generated from the backend's
// QueueItem DTO): a PR routed here for one or more `reasons` (MVP today:
// `["breaking_change"]`). Each carries a GitHub url and is approvable only via
// an explicit human override.
export type QueueItem = components["schemas"]["QueueItem"];

export async function fetchQueue(): Promise<QueueItem[]> {
  const resp = await fetch(`${API_BASE}/queue`);
  if (!resp.ok) {
    throw new Error(`queue request failed: ${resp.status}`);
  }
  return resp.json();
}

// approveQueueItem issues the manual override approve for one queued PR. This is
// the only path that approves a breaking change; the server records it as
// `matched_rule: "human approval: <reasons joined>"` and the item leaves the
// queue on the next cycle. Throws the server's message on a not-found or other
// failure.
export async function approveQueueItem(number: number): Promise<void> {
  const resp = await fetch(`${API_BASE}/queue/${number}/approve`, {
    method: "POST",
  });
  if (!resp.ok) {
    throw new Error(await extractError(resp, "approve queue item failed"));
  }
}

// Rule is the rule wire shape (generated from the backend's RuleBody DTO). `id`
// is server-generated and read-only; `enabled` and every predicate field are
// optional in the schema (only `name` is required), so the generated type marks
// them optional. A rule matches a PR when its author and title-part conditions
// all pass; a PR is auto-approved if any enabled rule matches.
export type Rule = components["schemas"]["RuleBody"];

export async function fetchRules(): Promise<Rule[]> {
  const resp = await fetch(`${API_BASE}/rules`);
  if (!resp.ok) {
    throw new Error(`rules request failed: ${resp.status}`);
  }
  return resp.json();
}

// PRDiff is the on-demand diff of one queued PR (generated from the backend's
// PRDiffBody DTO): the changed files fetched (at most one page) plus total_files,
// the PR's authoritative changed_files count. The Diff card compares files.length
// against total_files to show "first N of M files". FileDiff.patch is empty for
// binary/over-large files (rendered as "no preview").
export type PRDiff = components["schemas"]["PRDiffBody"];
export type FileDiff = components["schemas"]["FileDiff"];

// fetchDiff fetches a queued PR's diff on demand (the Diff pill click). The
// endpoint is queue-only: a number not in the queue is a 404, surfaced as the
// server's message so the card can show why.
export async function fetchDiff(number: number): Promise<PRDiff> {
  const resp = await fetch(`${API_BASE}/queue/${number}/diff`);
  if (!resp.ok) {
    throw new Error(await extractError(resp, "diff request failed"));
  }
  return resp.json();
}

// extractError pulls a human-readable message out of a non-2xx response,
// preferring huma's `detail` field so server-side validation errors (e.g. an
// invalid regex naming the offending field) surface to the user.
async function extractError(resp: Response, fallback: string): Promise<string> {
  try {
    const body = await resp.json();
    if (body && typeof body.detail === "string" && body.detail) {
      return body.detail;
    }
  } catch {
    /* non-JSON body; fall through to the generic message */
  }
  return `${fallback}: ${resp.status}`;
}

// createRule POSTs a new rule (no id; the server generates it) and returns the
// created rule. A validation failure throws with the server's message.
export async function createRule(rule: Rule): Promise<Rule> {
  const resp = await fetch(`${API_BASE}/rules`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(rule),
  });
  if (!resp.ok) {
    throw new Error(await extractError(resp, "create rule failed"));
  }
  return resp.json();
}

// updateRule PUTs a full-replacement of the rule with the given id. This is also
// how a rule is enabled/disabled (PUT with `enabled` flipped). Throws the
// server's message on a validation or not-found failure.
export async function updateRule(id: string, rule: Rule): Promise<Rule> {
  const resp = await fetch(`${API_BASE}/rules/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(rule),
  });
  if (!resp.ok) {
    throw new Error(await extractError(resp, "update rule failed"));
  }
  return resp.json();
}

// deleteRule removes the rule with the given id. Throws on a not-found failure.
export async function deleteRule(id: string): Promise<void> {
  const resp = await fetch(`${API_BASE}/rules/${id}`, { method: "DELETE" });
  if (!resp.ok) {
    throw new Error(await extractError(resp, "delete rule failed"));
  }
}
