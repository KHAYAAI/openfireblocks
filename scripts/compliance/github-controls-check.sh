#!/usr/bin/env bash
# GitHub controls check (read-only).
#
# Checks the repository settings that SOC 2 CC8.1 and ISO 27001 A.8.4, A.8.25,
# A.8.32 depend on, and that the policies in docs/compliance/isms/ require:
# branch protection on the default branch, Dependabot and secret scanning,
# CODEOWNERS, pinned third-party actions, least-privilege workflow tokens.
#
#   scripts/compliance/github-controls-check.sh [owner/repo] [--json out.json] [--no-fail]
#
# Needs `gh` (authenticated) or curl with GITHUB_TOKEN, and jq. Some settings
# (branch protection details, secret scanning) are readable only with an admin
# token; without one they are reported UNKNOWN, never PASS.
#
# Exit status: 1 if any check FAILs (unless --no-fail), else 0.
set -uo pipefail

REPO="${GITHUB_REPOSITORY:-}"
JSON_OUT=""
NO_FAIL=0
while [ $# -gt 0 ]; do
  case "$1" in
    --json) JSON_OUT="$2"; shift 2 ;;
    --no-fail) NO_FAIL=1; shift ;;
    -h|--help) sed -n '2,17p' "$0"; exit 0 ;;
    *) REPO="$1"; shift ;;
  esac
done
if [ -z "$REPO" ]; then
  url=$(git config --get remote.origin.url 2>/dev/null || true)
  REPO=$(printf '%s' "$url" | sed -E 's#(\.git)?$##; s#.*[:/]([^/]+/[^/]+)$#\1#')
fi
[ -n "$REPO" ] || { echo "usage: $0 owner/repo" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)

# api PATH -> prints body; returns 0 on 2xx, 1 otherwise (body still printed).
api() {
  if command -v gh >/dev/null 2>&1; then
    gh api -H 'Accept: application/vnd.github+json' "$1" 2>/dev/null
  else
    local body code
    body=$(curl -sS -w '\n%{http_code}' -H "Authorization: Bearer ${GITHUB_TOKEN:?set GITHUB_TOKEN or install gh}" \
      -H 'Accept: application/vnd.github+json' "https://api.github.com/$1")
    code=${body##*$'\n'}; printf '%s' "${body%$'\n'*}"
    [ "${code:0:1}" = 2 ]
  fi
}

RESULTS=()
FAILS=0
record() { # status id message
  printf '%-8s %-28s %s\n' "$1" "$2" "$3"
  RESULTS+=("$(jq -cn --arg s "$1" --arg i "$2" --arg m "$3" '{status:$s,id:$i,message:$m}')")
  [ "$1" = FAIL ] && FAILS=$((FAILS + 1))
  return 0
}

echo "GitHub controls check: $REPO  ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
echo

repo_json=$(api "repos/$REPO") || { echo "cannot read repos/$REPO: $repo_json" >&2; exit 2; }
visibility=$(jq -r '.visibility' <<<"$repo_json")
default_branch=$(jq -r '.default_branch' <<<"$repo_json")
is_admin=$(jq -r '.permissions.admin // false' <<<"$repo_json")
enc_branch=$(jq -rn --arg b "$default_branch" '$b|@uri')

# 1. Visibility: a decision, not a defect (risk R-21).
if [ "$visibility" = public ]; then
  record WARN repo.visibility "Repository is PUBLIC. Fine only if nothing in it is above 'Public' and no material says 'source under NDA' (risk R-21)."
else
  record PASS repo.visibility "Repository is $visibility."
fi

# 2. Default branch name.
case "$default_branch" in
  main|master) record PASS repo.default_branch "Default branch is '$default_branch'." ;;
  *) record WARN repo.default_branch "Default branch is '$default_branch'. Use a stable name (main) and protect it." ;;
esac

# 3. Branch protection, classic or rulesets.
branch_json=$(api "repos/$REPO/branches/$enc_branch") || branch_json='{}'
classic=$(jq -r '.protected // false' <<<"$branch_json")
rules_json=$(api "repos/$REPO/rules/branches/$enc_branch") || rules_json='[]'
rule_types=$(jq -r '[.[]?.type] | unique | join(",")' <<<"$rules_json" 2>/dev/null || echo "")

if [ "$classic" != true ] && [ -z "$rule_types" ]; then
  record FAIL branch.protected "Default branch '$default_branch' has no branch protection and no ruleset: anyone with write access can push, force-push or delete it."
else
  record PASS branch.protected "Default branch is protected (classic: $classic; rulesets: ${rule_types:-none})."
  reviews=""; admins=""; force=""; deletions=""; checks=""
  if [ "$classic" = true ]; then
    if prot=$(api "repos/$REPO/branches/$enc_branch/protection"); then
      reviews=$(jq -r '.required_pull_request_reviews.required_approving_review_count // 0' <<<"$prot")
      admins=$(jq -r '.enforce_admins.enabled // false' <<<"$prot")
      force=$(jq -r '.allow_force_pushes.enabled // false' <<<"$prot")
      deletions=$(jq -r '.allow_deletions.enabled // false' <<<"$prot")
      checks=$(jq -r '(.required_status_checks.contexts // []) + [(.required_status_checks.checks // [])[].context] | unique | length' <<<"$prot")
    fi
  fi
  if [ -n "$rule_types" ]; then
    r=$(jq -r '[.[] | select(.type=="pull_request") | .parameters.required_approving_review_count] | max // empty' <<<"$rules_json")
    [ -n "$r" ] && reviews=$(( ${reviews:-0} > r ? ${reviews:-0} : r ))
    grep -q non_fast_forward <<<"$rule_types" && force=false
    grep -q deletion <<<"$rule_types" && deletions=false
    grep -q required_status_checks <<<"$rule_types" && checks=${checks:-1}
  fi
  if [ -z "$reviews" ]; then record UNKNOWN branch.reviews "Cannot read protection details (needs an admin token)."
  elif [ "$reviews" -ge 1 ]; then record PASS branch.reviews "$reviews approving review(s) required."
  else record FAIL branch.reviews "No approving review required before merging."; fi
  if [ -n "$force" ]; then
    [ "$force" = false ] && record PASS branch.force_push "Force pushes blocked." || record FAIL branch.force_push "Force pushes allowed."
  fi
  if [ -n "$deletions" ]; then
    [ "$deletions" = false ] && record PASS branch.deletion "Branch deletion blocked." || record FAIL branch.deletion "Branch deletion allowed."
  fi
  if [ -n "$checks" ]; then
    [ "${checks:-0}" -ge 1 ] && record PASS branch.status_checks "Status checks required." || record FAIL branch.status_checks "No status checks required before merging."
  fi
  if [ -n "$admins" ]; then
    [ "$admins" = true ] && record PASS branch.enforce_admins "Rules apply to administrators." || record WARN branch.enforce_admins "Administrators can bypass the rules."
  fi
fi

# 4. Dependabot alerts.
if da=$(api "repos/$REPO/dependabot/alerts?state=open&per_page=100"); then
  n=$(jq 'length' <<<"$da"); crit=$(jq '[.[] | select(.security_advisory.severity=="critical" or .security_advisory.severity=="high")] | length' <<<"$da")
  if [ "$crit" -gt 0 ]; then record FAIL dependabot.alerts "Dependabot alerts on; $n open, $crit high/critical."
  else record PASS dependabot.alerts "Dependabot alerts on; $n open, none high/critical."; fi
elif grep -qi "disabled" <<<"$da"; then
  record FAIL dependabot.alerts "Dependabot alerts are DISABLED. Turn on: Settings > Code security > Dependabot alerts."
else
  record UNKNOWN dependabot.alerts "Cannot read Dependabot alerts (token lacks security_events/admin)."
fi
[ -f "$ROOT/.github/dependabot.yml" ] && record PASS dependabot.config ".github/dependabot.yml present (version updates)." \
  || record WARN dependabot.config "No .github/dependabot.yml (version updates)."

# 5. Secret scanning and push protection.
sa=$(jq -c '.security_and_analysis // empty' <<<"$repo_json")
if [ -z "$sa" ]; then
  record UNKNOWN secret_scanning "Not visible to this token (admin needed). Turn on secret scanning and push protection in Settings > Code security."
else
  ss=$(jq -r '.secret_scanning.status // "disabled"' <<<"$sa"); pp=$(jq -r '.secret_scanning_push_protection.status // "disabled"' <<<"$sa")
  [ "$ss" = enabled ] && record PASS secret_scanning "Secret scanning enabled." || record FAIL secret_scanning "Secret scanning $ss."
  [ "$pp" = enabled ] && record PASS secret_push_protection "Push protection enabled." || record FAIL secret_push_protection "Push protection $pp."
fi

# 6. Files in the repository.
if [ -f "$ROOT/.github/CODEOWNERS" ] || [ -f "$ROOT/CODEOWNERS" ] || [ -f "$ROOT/docs/CODEOWNERS" ]; then
  record PASS repo.codeowners "CODEOWNERS present."
else
  record WARN repo.codeowners "No CODEOWNERS: reviews of signing, approvals and migrations are not routed to named owners."
fi
if [ -f "$ROOT/SECURITY.md" ] || [ -f "$ROOT/.github/SECURITY.md" ]; then
  record PASS repo.security_policy "SECURITY.md present."
else
  record WARN repo.security_policy "No SECURITY.md telling researchers how to report vulnerabilities."
fi

# 7. Workflows: third-party actions pinned to a commit, token permissions declared.
unpinned=0; noperm=0; wf_count=0
for wf in "$ROOT"/.github/workflows/*.y*ml; do
  [ -f "$wf" ] || continue
  wf_count=$((wf_count + 1))
  while IFS= read -r use; do
    ref=${use#*@}
    case "$use" in
      ./*|docker://*) continue ;;
      actions/*|github/*) continue ;; # GitHub-owned; tag pins accepted by the policy
    esac
    [[ "$ref" =~ ^[0-9a-f]{40}$ ]] || { unpinned=$((unpinned + 1)); echo "         unpinned: $(basename "$wf"): $use"; }
  done < <(grep -E '^\s*-?\s*uses:\s*' "$wf" | sed -E 's/.*uses:\s*//; s/["'\'']//g; s/\s+#.*$//; s/\s+$//')
  grep -qE '^permissions:' "$wf" || { noperm=$((noperm + 1)); echo "         no top-level permissions: $(basename "$wf")"; }
done
[ "$unpinned" -eq 0 ] && record PASS actions.pinned "Third-party actions pinned to commit SHAs ($wf_count workflows)." \
  || record WARN actions.pinned "$unpinned third-party action reference(s) not pinned to a commit SHA."
[ "$noperm" -eq 0 ] && record PASS actions.permissions "Every workflow declares top-level token permissions." \
  || record WARN actions.permissions "$noperm workflow(s) without a top-level 'permissions:' block (token gets the repository default)."

echo
pass=$(printf '%s\n' "${RESULTS[@]}" | jq -s '[.[]|select(.status=="PASS")]|length')
warn=$(printf '%s\n' "${RESULTS[@]}" | jq -s '[.[]|select(.status=="WARN")]|length')
unk=$(printf '%s\n' "${RESULTS[@]}" | jq -s '[.[]|select(.status=="UNKNOWN")]|length')
echo "Summary: $pass PASS, $FAILS FAIL, $warn WARN, $unk UNKNOWN (admin token: $is_admin)"

if [ -n "$JSON_OUT" ]; then
  printf '%s\n' "${RESULTS[@]}" | jq -s --arg repo "$REPO" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{check:"github-controls", repo:$repo, at:$at, results:.}' > "$JSON_OUT"
  echo "Wrote $JSON_OUT"
fi
[ "$FAILS" -gt 0 ] && [ "$NO_FAIL" -eq 0 ] && exit 1
exit 0
