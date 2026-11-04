#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
if [[ -n "${DCC_TEST_PORT:-}" ]]; then
  PORT="$DCC_TEST_PORT"
else
  PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
fi
DATA="$(mktemp -d)"
LOG="$(mktemp)"
BIN="$(mktemp)"
PID=""
cleanup() {
  if [[ -n "$PID" ]]; then kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; fi
  rm -rf "$DATA" "$LOG" "$BIN"
}
trap cleanup EXIT

go build -ldflags "-X main.version=$(tr -d '[:space:]' < VERSION)" -o "$BIN" ./src/dcclite
"$BIN" --no-browser --listen "127.0.0.1:${PORT}" --data-dir "$DATA" >"$LOG" 2>&1 &
PID=$!
BASE="http://127.0.0.1:${PORT}"
READY=0
for _ in $(seq 1 80); do
  if ! kill -0 "$PID" 2>/dev/null; then cat "$LOG"; exit 1; fi
  if curl -fsS "$BASE/api/v1/health" >/dev/null 2>&1; then READY=1; break; fi
  sleep 0.1
done
if [[ "$READY" != "1" ]]; then cat "$LOG"; echo "Integration server did not become ready on $BASE" >&2; exit 1; fi

curl -fsS "$BASE/api/v1/health" -o "$DATA/health.json"
grep -q '"ok":true' "$DATA/health.json"
EXPECTED_VERSION="$(tr -d '[:space:]' < VERSION)"
grep -q "\"version\":\"$EXPECTED_VERSION\"" "$DATA/health.json"

# The embedded UI and the local terminal engine are served.
curl -fsS "$BASE/" -o "$DATA/index.html"
curl -fsS "$BASE/js/terminal.js" -o "$DATA/terminal.js"
for js in $(grep -o "/js/[a-z-]*\.js" "$DATA/index.html"); do curl -fsS "$BASE$js" -o /dev/null; done
curl -fsS "$BASE/app.css" -o "$DATA/app.css"
curl -fsS "$BASE/vendor/xterm.js" -o "$DATA/xterm.js"
curl -fsS "$BASE/vendor/xterm.css" -o "$DATA/xterm.css"
grep -q 'DCC Lite' "$DATA/index.html"
grep -q 'window.DCCXterm' "$DATA/xterm.js"
grep -q '.xterm' "$DATA/xterm.css"
for id in deviceSelect terminalTabs localPane remotePane transferList view-devices view-files view-terminal; do grep -q "id=\"$id\"" "$DATA/index.html"; done
grep -q 'cursorStyle: "bar"' "$DATA/terminal.js"

# Removed features are really gone.
for path in modules openwebif/x/summary management/x/packages backups scripts network/interfaces; do
  CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/api/v1/$path")"
  [[ "$CODE" == "404" ]] || { echo "removed route /api/v1/$path answered $CODE"; exit 1; }
done

# Security: cross-origin writes and path traversal are refused.
STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/profiles" \
  -H 'Origin: http://example.invalid' -H 'Content-Type: application/json' -d '{}')"
[[ "$STATUS" == "403" ]]
STATUS="$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/api/v1/files/local/list?path=..%2F..")"
[[ "$STATUS" == "400" ]]
STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/files/local/mkdir" \
  -H 'Content-Type: application/json' -d '{"path":"../dccnext-escape"}')"
[[ "$STATUS" == "422" ]]

# Devices: create, never leak the password, export without it, update, delete.
curl -fsS -X POST "$BASE/api/v1/profiles" -H 'Content-Type: application/json' \
  -d '{"name":"Integration box","host":"127.0.0.1","ftp_port":2121}' -o "$DATA/profile.json"
ID="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["profile"]["id"])' "$DATA/profile.json")"
curl -fsS "$BASE/api/v1/profiles" -o "$DATA/profiles-list.json"
grep -q '"credential_configured":true' "$DATA/profiles-list.json"
if grep -q 'dreambox' "$DATA/profiles-list.json"; then echo "profile API leaked a password"; exit 1; fi
grep -q '"password": "dreambox"' "$DATA/profiles.json"
curl -fsS "$BASE/api/v1/profiles/export" -o "$DATA/export.json"
if grep -q 'dreambox' "$DATA/export.json"; then echo "export leaked a password"; exit 1; fi
curl -fsS -X POST "$BASE/api/v1/profiles/$ID/test" -o "$DATA/test.json"
grep -q '"services"' "$DATA/test.json"
curl -fsS "$BASE/api/v1/files/sessions" | grep -q '"sessions":\[\]'
curl -fsS "$BASE/api/v1/files/local/list?path=." | grep -q '"entries"'
curl -fsS -X DELETE "$BASE/api/v1/profiles/$ID" -o /dev/null

# Editing: the content endpoints exist and validate input.
CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/api/v1/files/sessions/none/content")"
[[ "$CODE" == "400" ]] || { echo "content endpoint without a path answered $CODE"; exit 1; }
CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/api/v1/files/sessions/none/content?path=/etc/hostname")"
[[ "$CODE" == "404" ]] || { echo "content endpoint on an unknown session answered $CODE"; exit 1; }

# Remote access: a second, TLS-only listener demands sign-in.
kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true
if [[ -n "${DCC_TEST_PORT:-}" ]]; then RPORT=$((DCC_TEST_PORT + 1)); else
  RPORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
fi
RDATA="$(mktemp -d)"; RLOG="$(mktemp)"
"$BIN" --no-browser --listen "127.0.0.1:${PORT}" --remote-listen "127.0.0.1:${RPORT}" --data-dir "$RDATA" >"$RLOG" 2>&1 &
PID=$!
RBASE="https://127.0.0.1:${RPORT}"
READY=0
for _ in $(seq 1 80); do
  if curl -fsSk -o /dev/null "$RBASE/login" 2>/dev/null; then READY=1; break; fi
  sleep 0.1
done
[[ "$READY" == "1" ]] || { cat "$RLOG"; echo "remote listener did not start" >&2; exit 1; }
RPASS="$(sed -n 's/.*(shown once[^)]*): //p' "$RLOG" | head -n1)"
[[ -n "$RPASS" ]] || { cat "$RLOG"; echo "no generated remote password in the log" >&2; exit 1; }
[[ "$(curl -sSk -o /dev/null -w '%{http_code}' "$RBASE/api/v1/profiles")" == "401" ]]
[[ "$(curl -sSk -o /dev/null -w '%{http_code}' "$RBASE/")" == "302" ]]
[[ "$(curl -sSk -o /dev/null -w '%{http_code}' -X POST "$RBASE/api/v1/auth/login" -H "Origin: $RBASE" -d '{"password":"wrong"}')" == "401" ]]
JAR="$RDATA/cookies"
[[ "$(curl -sSk -o /dev/null -w '%{http_code}' -c "$JAR" -X POST "$RBASE/api/v1/auth/login" -H "Origin: $RBASE" -d "{\"password\":\"$RPASS\"}")" == "200" ]]
[[ "$(curl -sSk -o /dev/null -w '%{http_code}' -b "$JAR" "$RBASE/api/v1/profiles")" == "200" ]]
[[ "$(curl -sSk -o /dev/null -w '%{http_code}' -b "$JAR" -X POST "$RBASE/api/v1/profiles" -H 'Origin: https://evil.invalid' -d '{}')" == "403" ]]
rm -rf "$RDATA" "$RLOG"

echo "Integration tests passed."
