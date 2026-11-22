# Shared by the keeper checks (card hearth#913). Every run is a scratch
# suzuri: its own HOME, LOCALAPPDATA and RSZ_CONFIG_DIR under one short
# folder, so it never shares bridge.json, ai.json, settings or kept shells
# with the installed suzuri. Processes are stopped by pid, never by name.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Short: a socket path must fit in 104 bytes.
S="${RSZK_SCRATCH:-/tmp/rszk-$$}"
mkdir -p "$S/home" "$S/local" "$S/cfg"
S="$(cd "$S" && pwd -P)"
KDIR="$S/cfg/keeper"
KEEPER="$ROOT/target/debug/rsz-keeper"

say() { printf '%s\n' "$*"; }
fail() { say "FAIL: $*"; exit 1; }

real_cfg="$HOME/Library/Application Support/suzuri"
# The installed suzuri's files, to show they were not touched.
# A keeper/ or restore/ folder or bridge-last.json there would mean a check leaked into it.
real_stamp() { stat -f '%N %m' "$real_cfg/bridge.json" "$real_cfg/ai.json" "$real_cfg/keeper" "$real_cfg/restore" "$real_cfg/bridge-last.json" 2>/dev/null || true; }

# start_app BINARY [extra env...]: starts it in the scratch env, prints its pid.
start_app() {
  local bin="$1"; shift
  rm -f "$S/local/suzuri/bridge.json"
  env HOME="$S/home" LOCALAPPDATA="$S/local" RSZ_CONFIG_DIR="$S/cfg" RSZ_KEEPER_DIR="$KDIR" RSZ_BRIDGE=1 "$@" "$bin" >>"$S/app.log" 2>&1 &
  local pid=$!
  echo "$pid" >>"$S/app.pids"
  for _ in $(seq 100); do [[ -s "$S/local/suzuri/bridge.json" ]] && break; sleep 0.2; done
  [[ -s "$S/local/suzuri/bridge.json" ]] || fail "app $pid wrote no bridge.json"
  echo "$pid"
}

bridge() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d[sys.argv[2]])' "$S/local/suzuri/bridge.json" "$1"; }
# get PATH / post PATH JSON against the scratch app's bridge.
get() { curl -fsS -H "Authorization: Bearer $(bridge token)" "$(bridge url)$1"; }
post() { curl -fsS -H "Authorization: Bearer $(bridge token)" -H 'Content-Type: application/json' -d "$2" "$(bridge url)$1"; }
call() { post /v1/call "{\"tool\":\"$1\",\"args\":${2:-{\}}}"; }
jq_() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

# The kept shells' hellos, one JSON per line.
kept() { "$KEEPER" --dir "$KDIR" ls; }
kept_field() { kept | python3 -c "import json,sys
for l in sys.stdin:
  d=json.loads(l)
  if d.get('id')==$1: print(d.get('$2'))"; }

alive() { kill -0 "$1" 2>/dev/null; }

# shot PID FILE: a picture of that app's window only.
# A WKWebView behind other windows paints nothing, so the window comes to
# the front for a moment and the app that was in front gets it back.
shot() {
  local wid prev
  prev="$(osascript -e 'tell application "System Events" to get name of first process whose frontmost is true' 2>/dev/null || true)"
  osascript -e "tell application \"System Events\" to set frontmost of (first process whose unix id is $1) to true" >/dev/null 2>&1 || true
  sleep 1.5
  wid="$(swift "$ROOT/scripts/winid.swift" "$1" 2>/dev/null || true)"
  [[ -n "$wid" ]] && screencapture -x -o -l "$wid" "$2" && say "picture: $2" || say "(no picture: window of $1 not found)"
  [[ -n "$prev" ]] && osascript -e "tell application \"System Events\" to set frontmost of process \"$prev\" to true" >/dev/null 2>&1 || true
}

cleanup() {
  for p in $(cat "$S/app.pids" 2>/dev/null); do kill -9 "$p" 2>/dev/null || true; done
  if [[ -d "$KDIR" && -x "$KEEPER" ]]; then
    for id in $("$KEEPER" --dir "$KDIR" ls 2>/dev/null | python3 -c 'import json,sys
for l in sys.stdin: print(json.loads(l)["id"])' 2>/dev/null); do "$KEEPER" --dir "$KDIR" close "$id" || true; done
  fi
  sleep 0.5
  [[ -n "${KEEP_SCRATCH:-}" ]] || rm -rf "$S"
}
trap cleanup EXIT
