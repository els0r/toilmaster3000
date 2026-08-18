# GitLab adapter fixtures

Recorded GitLab GraphQL responses. ADR 0030 §10 moves the testing line from
core-vs-seam to pure-vs-I/O: normalisation is correctness-critical, so it is
tested at core weight against these, not against hand-authored shapes. The
GitLab field names in ADR 0030 were written from the schema as understood
during design; everything here replaces that guess with what a live instance
actually returned (issue #75).

## Provenance

| | |
|---|---|
| Instance | `gitlab.com`, `metadata.version` **19.3.0-pre** (`cad91a0b2b4`), `enterprise: true` |
| CLI | `glab` **1.114.0** (`4d7c6cda7`) |
| Captured | 2026-08-18 |
| Project | a private single-namespace project, scrubbed to `example-group/example-project` |

## What is evidence and what is substituted

The fixtures are scrubbed, so be precise about what they prove.

**Recorded, untouched — this is the evidence.** Every key, every `null`, every
enum value, every number, and the entire nesting shape. If a field is absent
here it was absent on the wire; if it is `null` the instance returned `null`.

**Substituted by `scrub.jq`** — the free-text that would leak a private
project: usernames, display names, project path, host, MR titles, branch names
and file paths. Substitutions preserve the shape each fold depends on: a
conventional-commit title keeps its `type(scope)!:` structure and any `Draft:`
prefix, a path keeps its directory depth and extension, a display name stays
correlated with its login. Nothing that a normalisation function reads is
invented.

## Files

| File | What it is |
|---|---|
| `mr_pull.json` | The ADR 0030 §4 batched pull — 41 merge requests in **one call**, carrying pipeline status, `diffStatsSummary`, per-file `diffStats`, approval state, reviewer `reviewState`, discussion counts and merge status together |
| `project_settings.json` | The ADR 0030 §8 precondition probe |
| `schema_enums.json` | Every enum the adapter maps from |
| `schema_fields.json` | Field-name-and-type audit for `MergeRequest`, `Pipeline`, `UserMergeRequestInteraction`, `DiffStats*`, and `Project`'s squash/merge settings |
| `schema_selector_args.json` | `Project.mergeRequests` arguments and the `not:` negation input — the evidence that ADR 0030 §5's three seam obligations are natively satisfiable |
| `queries/*.graphql` | What was asked, so it is reviewable next to what came back |

## Coverage, and what is missing

Present in `mr_pull.json`:

| Pipeline | Merge status | MR state | Count |
|---|---|---|---|
| absent | `NOT_OPEN` | closed | 2 |
| absent | `NOT_OPEN` | merged | 28 |
| `FAILED` | `MERGEABLE` | opened | 1 |
| `FAILED` | `NOT_OPEN` | merged | 10 |

**Deferred — not yet captured.** `MANUAL`, `SKIPPED`, `CANCELED` and `SUCCESS`
pipelines; a genuinely approved MR; a reviewer at `REQUESTED_CHANGES`; and
`NEED_REBASE`. The capture project's pipelines all fail with *"The pipeline
failed due to the user not being verified"* — gitlab.com withholds shared
runners from unverified accounts, so no job has ever executed there and no
pipeline can reach those states. `NEED_REBASE` additionally needs
`mergeRequestsFfOnlyEnabled`, and `REQUESTED_CHANGES` needs a second account.
Tracked on #72; slices #2b, #3 and #4 stay gated on it.

## Re-capturing

```sh
export TM3K_GL_PROJECT=group/project
export TM3K_GL_EXTRA='internal-name|another'  # optional, `|`-separated
export TM3K_GL_HOST=gitlab.example.com        # optional, self-hosted only

./capture.sh all   # -> raw/ (git-ignored)
./scrub.sh         # raw/ -> committed fixtures, then leak-checks
```

`scrub.sh` exits non-zero if its leak check finds a real identifier, and the
check covers the scripts and queries as well as the fixtures — a committed
script leaks as readily as a committed JSON.

Its search pattern is **built at run time from those variables and never
written into the file**, because the list of things to search for is precisely
the private material the script exists to remove. Hard-coding it would leak the
identifiers into the very commit the check was guarding. `TM3K_GL_EXTRA` is the
place for internal component names that appear in paths or titles.
`TM3K_GL_HOST` is for self-hosted instances only: this README names a public
SaaS host deliberately, as provenance.

Two `glab` 1.114.0 quirks the scripts work around, both cost real time to
find: `glab api graphql --input` always returns `Unexpected end of document`,
and `-f query=@file` is not expanded as a file read (only `-F` does `@`, and
for GraphQL it then ignores the query). Passing the query inline via
`-f query="$(cat file)"` is the form that works. Separately, **any** query
containing `__type` is rewritten by `glab` into a full `__schema`
introspection, so targeted introspection is impossible — `capture.sh` takes
the whole 7.5 MB dump into `raw/` and `scrub.sh` slices the committed extracts
out of it with `jq`.
