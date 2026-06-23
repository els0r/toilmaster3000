# On-demand per-PR diff fetch (exception to the no-per-PR-call rule)

The Diff card (a human clicks the Diff pill on a queued PR to skim its changes)
fetches the PR's files through a new per-PR `gh` call —
`gh api repos/{repo}/pulls/{number}/files` — behind a new
`GitHubClient.Diff(ctx, number)` seam method and a
`GET /queue/{number}/diff` endpoint. This is a deliberate, narrow exception to
ADR 0007, which removed the per-PR `gh pr view` N+1 because it did not survive a
higher cycle cadence.

The exception is safe because the cost model is different: ADR 0007's N+1 ran
**once per PR per cycle**, scaling with feed size on a timer; this call runs
**once per user click**, never on the cycle path. It cannot regress the engine
loop. We chose the `.../pulls/{n}/files` JSON API over `gh pr diff` (raw text)
so per-file `additions`/`deletions`/`status` are authoritative rather than
parsed in the frontend, keeping the wire DTO generated from Go (ADR 0003).

To keep it a skim aid and not a GitHub mirror, the fetch is bounded: one page
(`per_page=100`), no preview for files GitHub omits the patch for (binary /
over-large), a "showing first N of M files" banner past the cap, and an
always-present Open-on-GitHub escape hatch. No caching — fetched fresh on each
open.
