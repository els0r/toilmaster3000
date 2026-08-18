# Structure-preserving scrub (issue #75).
#
# What survives untouched, and is therefore the recorded evidence: every key,
# every null, every enum value, every number, and the whole nesting shape.
# What is substituted: the free-text strings that would leak a private
# project — usernames, real names, project path, host, MR titles, branch names
# and file paths. Substitutions preserve the *shape* each fold depends on:
# a conventional-commit title keeps its type/scope/`!`/`Draft:` structure, a
# path keeps its depth and extension.
#
# Usage: jq -f scrub.jq --arg project <group/project> --arg host <host> <in>

# Deterministic, collision-free-enough pseudonym from the real login.
def uname: if . == null then null else "user-" + (explode | add | tostring | .[-2:]) end;

def scrub_title($iid):
  if . == null then null
  else . as $t
  | ($t | test("^Draft: ")) as $draft
  | ($t | sub("^Draft: "; "")) as $body
  | ( $body
      | capture("^(?<type>[a-zA-Z]+)(\\((?<scope>[^)]*)\\))?(?<bang>!)?: ")
      // null ) as $cc
  | (if $cc == null then "subject \($iid)"
     else "\($cc.type)\(if $cc.scope then "(scope)" else "" end)\($cc.bang // ""): subject \($iid)"
     end) as $new
  | (if $draft then "Draft: " else "" end) + $new
  end;

def scrub_branch($iid):
  if . == null then null
  elif . == "main" or . == "master" then .
  else (capture("^(?<p>[a-zA-Z]+)/") // null) as $p
  | (if $p then "\($p.p)/branch-\($iid)" else "branch-\($iid)" end)
  end;

def scrub_path($n):
  if . == null then null
  else (split("/")) as $seg
  | ($seg | length) as $d
  | (($seg[-1] | capture("\\.(?<ext>[A-Za-z0-9]+)$") // null)) as $e
  | ([range(0; $d - 1) | "dir\(. + 1)"]
     + ["file\($n)" + (if $e then ".\($e.ext)" else "" end)])
  | join("/")
  end;

# The display name is derived from the pseudonymous login, so the same person
# stays recognisably one person across the fixture.
def scrub_user: if . == null then null
  else .username = (.username | uname)
     | (if has("name") then .name = ("User " + (.username | ltrimstr("user-"))) else . end)
  end;

def scrub_mr:
  .iid as $iid
  | .title = (.title | scrub_title($iid))
  | .sourceBranch = (.sourceBranch | scrub_branch($iid))
  | .targetBranch = (.targetBranch | scrub_branch($iid))
  | .webUrl = "https://gitlab.example.com/example-group/example-project/-/merge_requests/\($iid)"
  | (if .author then .author |= scrub_user else . end)
  | (if .approvedBy then .approvedBy.nodes |= map(scrub_user) else . end)
  | (if .reviewers then .reviewers.nodes |= map(scrub_user) else . end)
  | (if .diffStats then
       .diffStats |= (to_entries
         | map(.key as $i | .value | .path = (.path | scrub_path($i + 1))))
     else . end);

walk(if type == "object" and has("fullPath") and (.fullPath | type) == "string"
     then .fullPath = "example-group/example-project" else . end)
| (if .data.project.mergeRequests.nodes
   then .data.project.mergeRequests.nodes |= map(scrub_mr) else . end)
