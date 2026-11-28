#!/bin/sh
# Runs every front-end test in tests/web.
# Tests that need jsdom or playwright are skipped when it is not installed
# (npm install jsdom playwright && npx playwright install chromium).
# Set REQUIRE_ALL=1 (CI does) to fail instead of skipping.
cd "$(dirname "$0")/web" || exit 1
fail=0
for t in *.test.js; do
  for dep in jsdom playwright; do
    if grep -q "require(\"$dep\")" "$t" && ! node -e "require('$dep')" 2>/dev/null; then
      echo "SKIP  $t ($dep not installed)"
      [ -n "$REQUIRE_ALL" ] && fail=1
      continue 2
    fi
  done
  if out=$(node "$t" 2>&1); then echo "PASS  $t"; else echo "FAIL  $t"; echo "$out" | head -8; fail=1; fi
done
exit $fail
