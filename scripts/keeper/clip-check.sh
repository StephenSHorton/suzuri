#!/usr/bin/env bash
# Card hearth#936: a program in a kept shell that reads the clipboard (OSC 52
# "?") gets the clipboard's text while a Suzuri window shows the shell, and
# an empty answer once no window is attached. Runs the real app; puts a
# marker on the clipboard and puts the old text back after.
#   cargo build -p suzuri -p rsz-keeper && scripts/keeper/clip-check.sh
source "$(dirname "$0")/lib.sh"
APP="$ROOT/target/debug/suzuri"
[[ -x "$APP" && -x "$KEEPER" ]] || fail "build first: cargo build -p suzuri -p rsz-keeper"
old_clip="$(pbpaste 2>/dev/null || true)"
trap 'printf %s "$old_clip" | pbcopy; cleanup' EXIT
# The program: reads the clipboard with OSC 52, prints GOT-<tag>[<base64>].
cat >"$S/ask.sh" <<'ASK'
stty raw -echo
printf '\033]52;c;?\007'
bel=$(printf '\007')
r=''
while :; do c=$(dd bs=1 count=1 2>/dev/null); [ "$c" = "$bel" ] && break; r="$r$c"; done
stty sane
echo "GOT-$1[${r#*;c;}]"
ASK
mark="clip-check $$ 硯"
printf %s "$mark" | pbcopy
want="$(printf %s "$mark" | base64)"
app="$(start_app "$APP")"
sleep 2
id="$(kept | jq_ 'd["id"]' | head -1)"
got() { # got TAG: runs the program in the kept shell, prints its answer.
  "$KEEPER" --dir "$KDIR" type "$id" "sh $S/ask.sh $1\n"
  for _ in $(seq 50); do
    line="$("$KEEPER" --dir "$KDIR" screen "$id" 50 | grep -o "GOT-$1\[[^]]*\]" | tail -1)"
    [[ -n "$line" ]] && { echo "$line"; return; }
    sleep 0.2
  done
  fail "no answer for $1"
}
say "clipboard: \"$mark\" (base64 $want)"
say "== the app (pid $app) shows kept shell $id: viewers $(kept_field "$id" viewers)"
a="$(got attached)"; say "$a"
[[ "$a" == "GOT-attached[$want]" ]] || fail "attached: expected the clipboard's text"
kill -9 "$app"
sleep 1
say "== the app is gone; the shell lives on: viewers $(kept_field "$id" viewers)"
b="$(got detached)"; say "$b"
[[ "$b" == "GOT-detached[]" ]] || fail "detached: expected an empty answer"
app2="$(start_app "$APP")"
sleep 3
say "== a new app (pid $app2) attaches again: viewers $(kept_field "$id" viewers)"
c="$(got again)"; say "$c"
[[ "$c" == "GOT-again[$want]" ]] || fail "attached again: expected the clipboard's text"
say "PASS"
