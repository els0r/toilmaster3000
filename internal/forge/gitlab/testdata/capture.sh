#!/usr/bin/env bash
# Capture raw GitLab GraphQL responses for the fixtures the GitLab adapter
# tests against (issue #75, ADR 0030 §10).
#
#   TM3K_GL_PROJECT=group/project ./capture.sh probe   # versions + introspection
#   TM3K_GL_PROJECT=group/project ./capture.sh pull    # the batched MR pull
#   TM3K_GL_IIDS="1 2 3" ...      ./capture.sh pull    # ...restricted to those MRs
#
# Queries live in ./queries/*.graphql so what was asked is reviewable alongside
# what came back. Responses land unscrubbed in ./raw/ (git-ignored); scrub.sh
# promotes them to the committed fixtures.
#
# Note: `glab api graphql --input` is broken (glab 1.114.0 returns "Unexpected
# end of document"); `-f query=@file` is the working form.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
raw="$here/raw"
mkdir -p "$raw"
: "${TM3K_GL_PROJECT:?set TM3K_GL_PROJECT=group/project}"

gql() { # gql <outfile> <query-file> [extra glab args...]
  local out="$1" q="$2"; shift 2
  printf '  -> %s' "$out"
  if glab api graphql -f "query=@$here/queries/$q" "$@" \
       > "$raw/$out" 2> "$raw/$out.err"; then
    printf '\n'
  else
    printf '  FAILED: %s\n' "$(tr -d '\n' < "$raw/$out.err" | cut -c1-160)"
  fi
}

probe() {
  echo "== versions =="
  glab --version | tee "$raw/glab_version.txt"
  gql instance_version.json introspect_type.graphql -f n=Query >/dev/null 2>&1 || true
  glab api graphql -f query='{ metadata { version revision enterprise } }' \
    > "$raw/instance_version.json"
  cat "$raw/instance_version.json"

  echo "== schema introspection =="
  for t in MergeRequest Project Pipeline MergeRequestReviewState \
           DetailedMergeStatus MergeStatus PipelineStatusEnum \
           SquashOptionSetting MergeRequestReviewer MergeRequestInteraction \
           DiffStats DiffStatsSummary CiJobStatus UserMergeRequestInteraction; do
    gql "type_$t.json" introspect_type.graphql -f "n=$t"
  done

  echo "== project settings (squash option is an ADR 0030 §8 precondition) =="
  gql project_settings.json project_settings.graphql -f "p=$TM3K_GL_PROJECT"
}

pull() {
  local args=( -f "p=$TM3K_GL_PROJECT" )
  local label="<all open>"
  if [[ -n "${TM3K_GL_IIDS:-}" ]]; then
    label="$TM3K_GL_IIDS"
    args+=( -F "iids=$(printf '%s\n' $TM3K_GL_IIDS | jq -R . | jq -sc .)" )
  fi
  echo "== batched merge-request pull (iids: $label) =="
  gql mr_pull.json mr_pull.graphql "${args[@]}"
  jq -r '.data.project.mergeRequests.nodes[]?
         | "  !\(.iid) pipeline=\(.headPipeline.status // "<none>") " +
           "merge=\(.detailedMergeStatus) approved=\(.approved) " +
           "reviewers=\([.reviewers.nodes[]?.mergeRequestInteraction.reviewState] | tostring)"' \
    "$raw/mr_pull.json" 2>/dev/null || true
}

case "${1:-all}" in
  probe) probe ;;
  pull)  pull  ;;
  all)   probe; pull ;;
  *) echo "usage: $0 {probe|pull|all}" >&2; exit 2 ;;
esac
echo "done -> $raw"
