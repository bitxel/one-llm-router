#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

PROFILE="${COVERAGE_PROFILE_PATH:-}"
cleanup_profile=0
if [[ -z "${PROFILE}" ]]; then
  PROFILE="$(mktemp "${TMPDIR:-/tmp}/coverage-floor.XXXXXX.out")"
  cleanup_profile=1
fi
trap 'if [[ "${cleanup_profile}" -eq 1 ]]; then rm -f "${PROFILE}"; fi' EXIT
rm -f "${PROFILE}"

FLOOR="${COVERAGE_FLOOR:-80}"
PACKAGES=(
  "./internal/oauth/..."
  "./internal/core/..."
  "./internal/store/..."
  "./internal/api"
  "./internal/api/oauthapi/..."
  "./internal/api/adminapi/..."
  "./internal/api/exportapi/..."
  "./internal/api/playgroundapi/..."
)
TARGET_IMPORTS=(
  "github.com/user/one-llm-router/internal/oauth"
  "github.com/user/one-llm-router/internal/core"
  "github.com/user/one-llm-router/internal/store"
  "github.com/user/one-llm-router/internal/api"
  "github.com/user/one-llm-router/internal/api/oauthapi"
  "github.com/user/one-llm-router/internal/api/adminapi"
  "github.com/user/one-llm-router/internal/api/exportapi"
  "github.com/user/one-llm-router/internal/api/playgroundapi"
)

go test -coverprofile="${PROFILE}" -covermode=atomic "${PACKAGES[@]}"

RESOLVED_TARGETS=()
while IFS= read -r pkg; do
  [[ -n "${pkg}" ]] && RESOLVED_TARGETS+=("${pkg}")
done < <(go list "${PACKAGES[@]}")
targets_csv="$(IFS=,; echo "${RESOLVED_TARGETS[*]}")"

awk -v floor="${FLOOR}" -v targets="${targets_csv}" '
BEGIN {
  split(targets, targetList, ",")
}
NR == 1 {
  next
}
{
  file = $1
  sub(/:.*/, "", file)
  pkg = file
  sub(/\/[^\/]+$/, "", pkg)
  stmts = $2 + 0
  count = $3 + 0
  total[pkg] += stmts
  if (count > 0) {
    covered[pkg] += stmts
  }
}
END {
  fail = 0
  printed = 0
  for (i = 1; i in targetList; i++) {
    pkg = targetList[i]
    pct = (total[pkg] > 0) ? (covered[pkg] * 100.0 / total[pkg]) : 0
    printf "%s %.1f%% (%d/%d covered statements)\n", pkg, pct, covered[pkg], total[pkg]
    printed++
    if (pct + 1e-9 < floor) {
      fail = 1
    }
  }
  if (printed == 0) {
    print "coverage-floor: no target packages found in cover profile" > "/dev/stderr"
    exit 1
  }
  if (fail != 0) {
    printf "coverage-floor: threshold %.1f%% not met\n", floor > "/dev/stderr"
    exit 1
  }
}' "${PROFILE}"

echo "coverage-floor: all gated packages >= ${FLOOR}%"
