#!/bin/bash
# Run ON THE REMOTE (via `make e2e-quick|e2e-full`). Runs the named e2e suites one after the other, prints the duration of each and a
# summary table; exit status != 0 if any suite failed (the run continues after a failure).
#   test/run-suites.sh live snap tui ...        (suite name = test/e2e-<name>.sh)
# Env: SUITE_ENV_<name>="VAR=val ..." extra environment for one suite (e2e-full uses it to cut e2e-real down to two images)
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[ $# -gt 0 ] || { echo "usage: $0 suite..."; exit 2; }
rows=(); rc=0; T0=$(date +%s)
for s in "$@"; do
  [ -x "$ROOT/test/e2e-$s.sh" ] || { echo "unknown suite $s"; rows+=("$s MISSING 0"); rc=1; continue; }
  v=SUITE_ENV_$s; extra=${!v:-}
  echo "=== suite $s $(date +%T) ${extra:+($extra)}"
  t0=$(date +%s)
  eval "env $extra \"$ROOT/test/e2e-$s.sh\"" 2>&1 | tee "$ROOT/work/suite-$s.log" | grep -aE '^(FAIL|E2E-|PASS: .*ALL)|ALL PASS|FAILED' 
  r=${PIPESTATUS[0]}; d=$(( $(date +%s) - t0 ))
  echo "=== suite $s: rc=$r ${d}s (full log: work/suite-$s.log)"
  rows+=("$s $([ $r = 0 ] && echo PASS || echo FAIL) $d"); [ $r = 0 ] || rc=1
done
echo; echo "SUITE      RESULT  SECONDS"
for r in "${rows[@]}"; do set -- $r; printf '%-10s %-7s %s\n' "$1" "$2" "$3"; done
echo "TOTAL $(( $(date +%s) - T0 )) s   overall: $([ $rc = 0 ] && echo PASS || echo FAIL)"
exit $rc
