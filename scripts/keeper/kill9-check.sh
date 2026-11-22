#!/usr/bin/env bash
# Done-when 2 and 3 (card hearth#913) on a scratch debug build: a shell with
# a program running survives a kill -9 of the app, and the next start shows
# it with its screen and scrollback; closing a pane still ends its shell.
#   cargo build -p suzuri -p rsz-keeper && scripts/keeper/kill9-check.sh
source "$(dirname "$0")/lib.sh"
APP="$ROOT/target/debug/suzuri"
[[ -x "$APP" && -x "$KEEPER" ]] || fail "build first: cargo build -p suzuri -p rsz-keeper"
before="$(real_stamp)"
OUT="${SHOTS:-$S}"

say "== scratch: $S (HOME, LOCALAPPDATA and RSZ_CONFIG_DIR all in it)"
app1="$(start_app "$APP")"
sleep 2
id="$(kept | jq_ 'd["id"]' | head -1)"
[[ -n "$id" ]] || fail "no kept shell after start"
host="$(kept_field "$id" pid)"; shell="$(kept_field "$id" shell_pid)"
say "app $app1: pane $id is kept by host $host (shell $shell, version $(kept_field "$id" version))"
post /v1/submit "{\"tab_id\":$id,\"line\":\"echo ctl=\$SUZURI_CONTROL_URL; for i in \$(seq 1 400); do echo scroll-\$i; done; i=0; while :; do i=\$((i+1)); echo tick-\$i; sleep 1; done\"}" >/dev/null
sleep 4
t1="$("$KEEPER" --dir "$KDIR" screen "$id" 0 | grep -o 'tick-[0-9]*' | tail -1)"
say "running: $t1"
shot "$app1" "$OUT/kill9-before.png"

say "== kill -9 $app1"
kill -9 "$app1"
sleep 3
alive "$app1" && fail "app still alive"
alive "$host" || fail "host $host died with the app"
alive "$shell" || fail "shell $shell died with the app"
t2="$("$KEEPER" --dir "$KDIR" screen "$id" 0 | grep -o 'tick-[0-9]*' | tail -1)"
say "app gone; host $host and shell $shell alive; the program went on: $t1 -> $t2"

say "== start the app again"
app2="$(start_app "$APP")"
sleep 3
[[ "$(kept_field "$id" viewers)" == "1" ]] || fail "the new app did not attach to shell $id"
snap="$(get "/v1/panes/$id?lines=500")"
oldest="$("$KEEPER" --dir "$KDIR" screen "$id" 1000 | grep -oE 'scroll-[0-9]+' | head -1)"
echo "$snap" | grep -q 'scroll-1\b\|scroll-1"' || echo "$snap" | grep -q 'scroll-12' || fail "scrollback not in the new app: $(echo "$snap" | head -c 300)"
t3="$(echo "$snap" | grep -o 'tick-[0-9]*' | tail -1)"
first_scroll="$(echo "$snap" | grep -oE 'scroll-[0-9]+' | head -1)"
say "app $app2 shows pane $id: same host $(kept_field "$id" pid), same shell $(kept_field "$id" shell_pid); screen at $t3; its last 500 lines start at $first_scroll, and the host's history goes back to $oldest"
[[ "$(kept_field "$id" shell_pid)" == "$shell" ]] || fail "not the same shell"
envurl="$("$KEEPER" --dir "$KDIR" screen "$id" 1000 | sed -n 's/^ctl=//p' | head -1)"
[[ "$envurl" == "$(bridge url)" ]] || fail "the kept shell's SUZURI_CONTROL_URL ($envurl) is not the new app's ($(bridge url))"
say "the kept shell's SUZURI_CONTROL_URL still reaches the new app: $envurl"
shot "$app2" "$OUT/kill9-after.png"

say "== close a pane: its shell ends"
b="$(call split "{\"pane_id\":$id,\"axis\":\"right\"}" | jq_ 'd["pane_id"]')"
sleep 2
bshell="$(kept_field "$b" shell_pid)"; bhost="$(kept_field "$b" pid)"
post /v1/submit "{\"tab_id\":$b,\"line\":\"sleep 9999\"}" >/dev/null
sleep 1
prog="$(pgrep -P "$bshell" sleep | head -1)"
[[ -n "$prog" ]] || fail "sleep 9999 did not start in pane $b"
say "pane $b: host $bhost, shell $bshell, running sleep 9999 (pid $prog)"
call close "{\"pane_id\":$b}" >/dev/null
sleep 2
alive "$bshell" && fail "closed pane's shell $bshell still alive"
alive "$bhost" && fail "closed pane's host $bhost still alive"
alive "$prog" && fail "its program $prog lives on"
[[ -e "$KDIR/$b.sock" ]] && fail "socket left"
alive "$shell" || fail "the other pane's shell died"
say "closed pane $b: host $bhost, shell $bshell and sleep $prog are gone; pane $id still runs"

after="$(real_stamp)"
[[ "$before" == "$after" ]] || fail "the installed suzuri's bridge.json/ai.json changed"
say "installed suzuri's config untouched (no keeper/ or bridge-last.json there): ${after:-none}"
say "PASS"
