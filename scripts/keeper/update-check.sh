#!/usr/bin/env bash
# Done-when 1 and 4 (card hearth#913): Suzuri's real Install and restart,
# between two signed release builds, on a scratch install. A shell running a
# program in the old version survives the update; the new version shows it
# with its screen and scrollback; the shell's host still runs the old
# program (a keeper update leaves open shells alone) while a new pane gets
# the new one. A throwaway updater key and an http feed on 127.0.0.1, as in
# scripts/release/test-update-mac.sh; nothing is published.
#   scripts/keeper/update-check.sh [old=0.10.0] [new=0.10.1] [port=8787]
OLD="${1:-0.10.0}" NEW="${2:-0.10.1}" PORT="${3:-8787}"
source "$(dirname "$0")/lib.sh"
OUT="${SHOTS:-$S}"
before="$(real_stamp)"
cargo build -q -p rsz-keeper

if [[ -z "${SKIP_BUILD:-}" ]]; then
  cargo tauri signer generate --ci -p "" -w "$S/test.key" >/dev/null
  python3 - "$S" <<'PY'
import json, sys
t = sys.argv[1]
json.dump({"plugins": {"updater": {"pubkey": open(f"{t}/test.key.pub").read().strip(),
                                   "dangerousInsecureTransportProtocol": True}}},
          open(f"{t}/overlay.json", "w"))
PY
  for v in "$OLD" "$NEW"; do
    say "== build $v (signed, not notarized)"
    RSZ_TAURI_CONFIG="$S/overlay.json" RSZ_UPDATER_KEY="$S/test.key" \
      "$ROOT/scripts/release/publish.sh" "$v" --mac-only --no-notarize --no-upload \
      --feed-base "http://127.0.0.1:$PORT" >"$S/build-$v.log" 2>&1 || { tail -20 "$S/build-$v.log"; fail "build $v"; }
  done
fi
(cd "$ROOT/dist/$NEW" && exec python3 -m http.server "$PORT" --bind 127.0.0.1 >"$S/http.log" 2>&1) & SRV=$!
trap 'kill $SRV 2>/dev/null; for p in $(pgrep -f "$S/inst/" || true); do echo $p >>"$S/app.pids"; done; cleanup' EXIT
mkdir -p "$S/inst" && tar -xzf "$ROOT/dist/$OLD/"*.app.tar.gz -C "$S/inst"
APP="$S/inst/suzuri.app"
ver() { /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist"; }
say "== installed $(ver) at $APP; the feed offers $NEW"

app1="$(start_app "$APP/Contents/MacOS/suzuri" RSZ_UPDATE_AUTO=1 RSZ_UPDATE_URL="http://127.0.0.1:$PORT/latest.json")"
sleep 1
id="$(kept | jq_ 'd["id"]' | head -1)"
[[ -n "$id" ]] || fail "no kept shell"
host="$(kept_field "$id" pid)"; shell="$(kept_field "$id" shell_pid)"
post /v1/submit "{\"tab_id\":$id,\"line\":\"for i in \$(seq 1 400); do echo scroll-\$i; done; i=0; while :; do i=\$((i+1)); echo tick-\$i; sleep 1; done\"}" >/dev/null
say "app $app1 ($(ver)): pane $id kept by host $host (version $(kept_field "$id" version)), shell $shell, running a counter"

say "== waiting for the app to install $NEW and restart itself"
for _ in $(seq 120); do [[ "$(ver)" == "$NEW" ]] && ! alive "$app1" && break; sleep 1; done
[[ "$(ver)" == "$NEW" ]] || fail "still $(ver)"
alive "$app1" && fail "the old app did not exit"
app2=""
for _ in $(seq 60); do app2="$(pgrep -f "$APP/Contents/MacOS/suzuri$" | head -1 || true)"; [[ -n "$app2" ]] && break; sleep 0.5; done
[[ -n "$app2" ]] || fail "the app did not restart"
echo "$app2" >>"$S/app.pids"
ps eww -p "$app2" | tr ' ' '\n' | grep -q "RSZ_CONFIG_DIR=$S/cfg" || fail "restarted app lost the scratch env"
say "installed $(ver); app $app1 exited, app $app2 started by the update (scratch env kept)"
for _ in $(seq 40); do [[ "$(kept_field "$id" viewers)" == "1" ]] && break; sleep 0.5; done
[[ "$(kept_field "$id" viewers)" == "1" ]] || fail "app $app2 did not attach to shell $id"
sleep 2
alive "$shell" || fail "the shell died"
[[ "$(kept_field "$id" shell_pid)" == "$shell" && "$(kept_field "$id" pid)" == "$host" ]] || fail "not the same shell"
snap="$(get "/v1/panes/$id?lines=500")"
t="$(echo "$snap" | grep -oE 'tick-[0-9]+' | tail -1)"
oldest="$("$KEEPER" --dir "$KDIR" screen "$id" 1000 | grep -oE 'scroll-[0-9]+' | head -1)"
[[ -n "$t" && "$oldest" == "scroll-1" ]] || fail "screen or scrollback missing ($t, $oldest)"
echo "$snap" | grep -q 'scroll-3[0-9][0-9]' || fail "the new app's view of pane $id has no scrollback"
say "app $app2 shows pane $id: same host $host and shell $shell, the counter at $t, scrollback back to $oldest"
shot "$app2" "$OUT/update-after.png"

say "== the keeper update: the open shell keeps its old host, a new pane gets the new one"
b="$(call split "{\"pane_id\":$id,\"axis\":\"right\"}" | jq_ 'd["pane_id"]')"
sleep 2
say "pane $id: host $(kept_field "$id" pid) reports version $(kept_field "$id" version) (started by the old app)"
say "pane $b: host $(kept_field "$b" pid) reports version $(kept_field "$b" version) (started by the new app)"
[[ "$(kept_field "$id" version)" == "$OLD" ]] || fail "old host should still be $OLD"
[[ "$(kept_field "$b" version)" == "$NEW" ]] || fail "new host should be $NEW"
shot "$app2" "$OUT/update-split.png"

after="$(real_stamp)"
[[ "$before" == "$after" ]] || fail "the installed suzuri's bridge.json/ai.json changed"
say "installed suzuri's config untouched (no keeper/ or bridge-last.json there): ${after:-none}"
say "PASS"
