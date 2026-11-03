#!/bin/sh
# Same-machine bench on macOS: rust-suzuri vs Go suzuri with 8 streaming
# panes (fakeagent) and the same external latency probe.
#
#   scripts/bench-mac.sh rsz|suzuri [samples]
#
# Needs: a release build (cargo build --release -p rust-suzuri -p fakeagent),
# a suzuri binary in $SUZURI_BIN (go build -o ... ./cmd/suzuri), and the
# Accessibility + Screen Recording permissions for the terminal running it.
# The app window is brought to the front for the run (an occluded webview or
# Metal layer stops painting); keys go straight to the app's pid.
#
# Output: bench/out/<app>-<time>/ with probe.json, summary.json, shot.png.
set -eu
APP=${1:?rsz or suzuri}
SAMPLES=${2:-300}
R=$(cd "$(dirname "$0")/.." && pwd)
OUT="$R/bench/out/$APP-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUT"
TOOLS="$R/bench/out/tools"
mkdir -p "$TOOLS"
for t in keypost latprobe winid; do
  if [ ! -x "$TOOLS/$t" ] || [ "$R/scripts/$t.swift" -nt "$TOOLS/$t" ]; then
    swiftc -O -swift-version 5 "$R/scripts/$t.swift" -o "$TOOLS/$t" 2>/dev/null
  fi
done
K="$TOOLS/keypost"

# Every pane runs this; the first one to start is the quiet probe pane.
LOCK="$OUT/quiet.lock"
WRAP="$OUT/pane.sh"
printf '#!/bin/sh\nexec "%s" --quiet-first "%s"\n' "$R/target/release/fakeagent" "$LOCK" > "$WRAP"
chmod +x "$WRAP"

webkit_pids() { pgrep -f 'com.apple.WebKit' | sort || true; }
BEFORE=$(webkit_pids)

case "$APP" in
rsz)
  RSZ_BENCH_OUT="$OUT/inapp.json" RSZ_TRACE=1 RSZ_PANES=8 RSZ_CMD="$WRAP" "$R/target/release/rust-suzuri" > "$OUT/app.log" 2>&1 &
  PID=$!
  sleep 4
  ;;
suzuri)
  BIN=${SUZURI_BIN:?set SUZURI_BIN to a suzuri binary}
  # A private HOME: suzuri's config, logs and the shells' rc files stay
  # out of the real ones. The look is the user's, minus the animated
  # ambient and intro (rust-suzuri has neither).
  H="$OUT/home"
  mkdir -p "$H/Library/Application Support/suzuri"
  python3 - "$H" "$WRAP" <<'EOF'
import json, os, sys
home, wrap = sys.argv[1], sys.argv[2]
src = os.path.expanduser("~/Library/Application Support/suzuri/config.json")
c = json.load(open(src)) if os.path.exists(src) else {}
c.update({"intro": "none", "shell_ambient": "none", "shell_matrix": False, "shell_logo": 0,
          "first_run_done": True, "active_profile": "Bench",
          "window": {"x": 264, "y": 150, "width": 1200, "height": 800}})
c["profiles"] = [{"name": "Bench", "shell": wrap}]
json.dump(c, open(os.path.join(home, "Library/Application Support/suzuri/config.json"), "w"), indent=2)
EOF
  HOME="$H" "$BIN" > "$OUT/app.log" 2>&1 &
  PID=$!
  sleep 4
  # Balanced splits so the 8 panes come out equal (suzuri halves the
  # focused pane and focuses the new one): 2 columns, then 4, then split
  # each column down from right to left (only "left" moves: from a bottom
  # half the pane to the left is still full height, so the move is
  # unambiguous), and finally up to the first pane, the quiet probe pane.
  KEY_D=2; KEY_E=14; LEFT=123; RIGHT=124; UP=126
  s() { "$K" "$PID" key "$@"; sleep 1.2; }
  s $KEY_D cmd shift                      # A | B (focus B)
  s $LEFT cmd alt; s $KEY_D cmd shift     # A | A2 | B (focus A2)
  s $RIGHT cmd alt; s $KEY_D cmd shift    # A | A2 | B | B2 (focus B2)
  s $KEY_E cmd shift; s $LEFT cmd alt
  s $KEY_E cmd shift; s $LEFT cmd alt
  s $KEY_E cmd shift; s $LEFT cmd alt
  s $KEY_E cmd shift; s $UP cmd alt
  W0=$("$TOOLS/winid" "$PID"); screencapture -x -o -l "$W0" "$OUT/layout.png" || true
  ;;
*) echo "rsz or suzuri"; exit 2 ;;
esac

AFTER=$(webkit_pids)
HELPERS=$(printf '%s\n%s\n' "$BEFORE" "$AFTER" | sort | uniq -u | tr '\n' ' ')
PIDS="$PID $HELPERS"
osascript -e "tell application \"System Events\" to set frontmost of (first process whose unix id is $PID) to true" >/dev/null
echo "warming up 15 s (pids: $PIDS)"
sleep 15

cpu_secs() {
  ps -o time= -p "$(echo $PIDS | tr ' ' ',')" | awk -F'[:.]' '{
    n = NF; s = 0
    if (n == 3) s = $1 * 60 + $2 + $3 / 100
    else if (n == 4) s = $1 * 3600 + $2 * 60 + $3 + $4 / 100
    t += s } END { printf "%.2f", t }'
}
footprint_mb() {
  for p in $PIDS; do footprint -f bytes "$p" 2>/dev/null | awk '/Footprint:/ {for (i = 1; i < NF; i++) if ($i == "Footprint:") print $(i + 1)}'; done |
    awk '{ t += $1 } END { printf "%.1f", t / 1048576 }'
}
osascript -e "tell application \"System Events\" to set frontmost of (first process whose unix id is $PID) to true" >/dev/null
MEM0=$(footprint_mb)
CPU0=$(cpu_secs)
T0=$(date +%s)
# Memory every 5 s while the probe runs (the webview's heap swings with GC).
( while :; do footprint_mb >> "$OUT/mem.txt"; echo >> "$OUT/mem.txt"; sleep 5; done ) &
SAMPLER=$!
"$TOOLS/latprobe" "$PID" "$SAMPLES" 100 "$OUT/probe.json" 2> "$OUT/probe.err" > /dev/null || true
kill "$SAMPLER" 2>/dev/null || true
# With RSZ_BENCH=1 the app measures the same keys itself and reports when
# its window ends (in-app vs pixel calibration); give it time to finish.
if [ "${RSZ_BENCH:-}" = 1 ]; then
  for _ in $(seq 1 30); do kill -0 "$PID" 2>/dev/null || break; sleep 1; done
fi
T1=$(date +%s)
CPU1=$(cpu_secs)
MEM1=$(footprint_mb)
W=$("$TOOLS/winid" "$PID")
screencapture -x -o -l "$W" "$OUT/shot.png" || true
kill "$PID" 2>/dev/null || true
sleep 1

python3 - "$OUT" "$APP" "$MEM0" "$MEM1" "$CPU0" "$CPU1" "$T0" "$T1" <<'EOF'
import json, sys
out, app, m0, m1, c0, c1, t0, t1 = sys.argv[1:]
probe = json.load(open(f"{out}/probe.json")) if __import__("os").path.exists(f"{out}/probe.json") else {}
secs = max(1, int(t1) - int(t0))
mem = sorted(float(x) for x in open(f"{out}/mem.txt").read().split() if x.strip())
s = {"app": app, "panes": 8, "probe_ms": probe,
     "footprint_mb_samples": {"n": len(mem), "min": mem[0] if mem else 0, "median": mem[len(mem)//2] if mem else 0, "max": mem[-1] if mem else 0},
     "footprint_mb_start": float(m0), "footprint_mb_end": float(m1),
     "cpu_cores_avg": round((float(c1) - float(c0)) / secs, 3), "secs": secs}
json.dump(s, open(f"{out}/summary.json", "w"), indent=2)
print(json.dumps(s, indent=2))
EOF
cat "$OUT/probe.err" >&2
