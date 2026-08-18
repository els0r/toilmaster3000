#!/usr/bin/env bash
# Promote raw captures (./raw/, git-ignored) to committed fixtures by running
# the structure-preserving substitution in scrub.jq. See README.md for exactly
# which parts of a fixture are recorded evidence and which are substituted.
#
#   ./scrub.sh
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
raw="$here/raw"
S="$raw/schema_full.json"

# The leak check needs to know what to look for, and that list is exactly the
# private material this whole script exists to remove. So it is derived at run
# time from the environment and never written down here — a scrubber that
# hard-codes the identifiers it scrubs leaks them into the commit it was
# guarding. Add anything else project-specific (internal component names)
# to TM3K_GL_EXTRA as a `|`-separated list. TM3K_GL_HOST is for self-hosted
# instances only — the README names a public SaaS host deliberately, as
# provenance, so passing gitlab.com would just flag that on purpose.
: "${TM3K_GL_PROJECT:?set TM3K_GL_PROJECT=group/project — the leak check needs it}"

for f in mr_pull project_settings; do
  [[ -f "$raw/$f.json" ]] || { echo "missing $raw/$f.json — run capture.sh"; exit 1; }
  jq -f "$here/scrub.jq" "$raw/$f.json" > "$here/$f.json"
  echo "scrubbed -> $f.json"
done

# Schema extracts: the evidence behind the ADR 0030 field-name audit. The full
# introspection dump is 7.5 MB and stays in raw/; these are the slices the
# adapter's decode types are written against.
jq '{ enums: [ .data.__schema.types[]
        | select(.name | IN("MergeRequestReviewState","DetailedMergeStatus",
                            "MergeStatus","PipelineStatusEnum",
                            "SquashOptionSetting","MergeRequestState"))
        | { name, values: [ .enumValues[] | .name ] } ] }' "$S" > "$here/schema_enums.json"
echo "scrubbed -> schema_enums.json"

jq '{ MergeRequest: [ .data.__schema.types[] | select(.name=="MergeRequest")
        | .fields[] | { name, type: (.type.name // .type.ofType.name // .type.ofType.ofType.name) } ],
      Pipeline: [ .data.__schema.types[] | select(.name=="Pipeline")
        | .fields[] | { name, type: (.type.name // .type.ofType.name // .type.ofType.ofType.name) } ],
      UserMergeRequestInteraction: [ .data.__schema.types[]
        | select(.name=="UserMergeRequestInteraction")
        | .fields[] | { name, type: (.type.name // .type.ofType.name) } ],
      DiffStats: [ .data.__schema.types[] | select(.name=="DiffStats") | .fields[].name ],
      DiffStatsSummary: [ .data.__schema.types[] | select(.name=="DiffStatsSummary") | .fields[].name ],
      Project_squash_and_merge: [ .data.__schema.types[] | select(.name=="Project")
        | .fields[] | select(.name | test("(?i)squash|onlyAllow|ffOnly|allowMergeOn"))
        | { name, type: (.type.name // .type.ofType.name) } ] }' "$S" > "$here/schema_fields.json"
echo "scrubbed -> schema_fields.json"

jq '{ "Project.mergeRequests": [ .data.__schema.types[] | select(.name=="Project")
        | .fields[] | select(.name=="mergeRequests") | .args[] | { name, type } ],
      MergeRequestsResolverNegatedParams:
        [ .data.__schema.types[] | select(.name=="MergeRequestsResolverNegatedParams")
          | .inputFields[].name ] }' "$S" > "$here/schema_selector_args.json"
echo "scrubbed -> schema_selector_args.json"

echo
echo "leak check:"
# Generic credential shapes are safe to name; project identity is not. The
# bracket forms keep this line from matching itself.
pattern='glpat[-]|glrt[-]|private[-_]token|PRIVATE[-]TOKEN|Bearer [A-Za-z0-9_-]{20}'
pattern="$pattern|$(printf '%s' "${TM3K_GL_PROJECT%%/*}" | sed 's/[.[\*^$]/\\&/g')"
pattern="$pattern|$(printf '%s' "${TM3K_GL_PROJECT##*/}" | sed 's/[.[\*^$]/\\&/g')"
pattern="$pattern|$(printf '%s' "$TM3K_GL_PROJECT" | sed 's#/#.#g')"
[[ -n "${TM3K_GL_HOST:-}" ]] && pattern="$pattern|$(printf '%s' "$TM3K_GL_HOST" | sed 's/\./\\./g')"
[[ -n "${TM3K_GL_EXTRA:-}" ]] && pattern="$pattern|$TM3K_GL_EXTRA"

# Scans the scripts too, not just the fixtures: this file is committed, so it
# is as capable of leaking as any JSON beside it.
if grep -rniE "$pattern" "$here" --exclude-dir=raw; then
  echo "LEAK FOUND — do not commit" >&2; exit 1
else
  echo "  clean (checked fixtures, queries and scripts)"
fi
