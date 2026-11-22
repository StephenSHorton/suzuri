#!/usr/bin/env bash
# Done-when 5 (card hearth#913): a second program attaches to a shell the
# app is showing, over the keeper's socket: it lists it, watches its frames,
# types into it and reads it back, and the app shows what it typed. The
# second program is `rsz-keeper` (crates/keeper: no rsz-term, no Zig), the
# same client Hearth would use.
#   cargo build -p suzuri -p rsz-keeper && scripts/keeper/attach-check.sh
source "$(dirname "$0")/lib.sh"
APP="$ROOT/target/debug/suzuri"
[[ -x "$APP" && -x "$KEEPER" ]] || fail "build first: cargo build -p suzuri -p rsz-keeper"
OUT="${SHOTS:-$S}"
app="$(start_app "$APP")"
sleep 2
id="$(kept | jq_ 'd["id"]' | head -1)"
say "== rsz-keeper ls (the app $app shows pane $id)"
kept | sed 's/"exe":"[^"]*",//'
say "== rsz-keeper watch $id 3 (a second viewer, while the app views it too)"
( sleep 1; "$KEEPER" --dir "$KDIR" type "$id" 'echo hello from a second program\n' ) &
"$KEEPER" --dir "$KDIR" watch "$id" 3 | grep -E '^hello|frames,' | sed 's/"exe":"[^"]*",//'
wait
say "== rsz-keeper screen $id"
"$KEEPER" --dir "$KDIR" screen "$id" 0 | grep -v '^$' | tail -3
say "== the app's own view of pane $id (its bridge)"
get "/v1/panes/$id?lines=20" | grep -o 'hello from a second program' | head -1 || fail "the app does not show it"
[[ "$(kept_field "$id" viewers)" == "1" ]] || fail "the app's viewer is gone"
shot "$app" "$OUT/attach.png"
say "host $(kept_field "$id" pid) memory: $(ps -o rss= -p "$(kept_field "$id" pid)" | awk '{printf "%.1f MB", $1/1024}') resident (the app: $(ps -o rss= -p "$app" | awk '{printf "%.1f MB", $1/1024}'))"
say "PASS"
