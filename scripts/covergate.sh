#!/usr/bin/env bash
# Fails if any package's statement coverage is below its threshold.
# Usage: scripts/covergate.sh [default-threshold]   (default 85)
# Per-package overrides live in the OVERRIDES map below.
set -euo pipefail
DEFAULT="${1:-85}"
declare -A OVERRIDES=(
  [koine/cmd/koine]=75   # main() itself and the blocking-server happy path are not unit-testable
)
fail=0
while read -r status pkg _ _ cov _; do
  [[ "$status" == "ok" ]] || continue
  pct="${cov%\%}"
  want="${OVERRIDES[$pkg]:-$DEFAULT}"
  if awk "BEGIN{exit !($pct < $want)}"; then
    printf 'FAIL  %-28s %5s%% < %s%%\n' "$pkg" "$pct" "$want"; fail=1
  else
    printf 'ok    %-28s %5s%% >= %s%%\n' "$pkg" "$pct" "$want"
  fi
done < <(go test -count=1 -cover ./... 2>&1 | tee /dev/stderr | grep -E '^ok ')
exit $fail
