#!/usr/bin/env bash
# Card hearth#970 on a scratch debug build: Suzuri shows a shell another
# program keeps (Hearth's Open in Suzuri) through the open_kept tool. A
# stand-in for Hearth's host runs in a 0700 folder of its own, outside
# Suzuri's keeper folder, with a viewer of its own watching (Hearth's
# window). Then:
#   1. GET /tools lists open_kept; a bad path is refused
#   2. open_kept attaches a second viewer in a new tab under a pane id of
#      Suzuri's own, and answers 200 {ok, pane_id}
#   3. closing that tab lets go: the shell, its host and Hearth's viewer go
#      on, and Hearth's viewer still draws it; so does quitting Suzuri
#   4. the pane taking the keys sets the host's size to Suzuri's again
#   cargo build -p suzuri -p rsz-keeper && scripts/keeper/open-kept-check.sh
# RSZ_KEEPER_HOST=<program> runs Hearth's shell in another host program,
# such as the standalone rsz-keeper-host (hearth#190), in place of
# `suzuri --keeper-host`.
source "$(dirname "$0")/lib.sh"
APP="$ROOT/target/debug/suzuri"
HOST="${RSZ_KEEPER_HOST:-$APP}"
[[ -x "$APP" && -x "$KEEPER" ]] || fail "build first: cargo build -p suzuri -p rsz-keeper"
before="$(real_stamp)"
OUT="${SHOTS:-$S}"
# Hearth's keeper folder (its own, not Suzuri's), and its shell's id: 1,
# the same id as Suzuri's own first pane.
HD="$S/hearth"
mkdir -p "$HD" && chmod 700 "$HD"
HID=1
SOCK="$HD/$HID.sock"

hv=""
hearth_stop() {
  "$KEEPER" --dir "$HD" close "$HID" >/dev/null 2>&1 || true
  [[ -n "$hv" ]] && { kill "$hv" 2>/dev/null; wait "$hv" 2>/dev/null; } || true
}
trap 'hearth_stop; cleanup' EXIT

say "== a host for Hearth's shell, in $HD (not Suzuri's folder)"
# The host lives on after its "ready" line: read it from a file.
echo '{"cols":80,"rows":24,"command":["/bin/sh"],"version":"hearth-stand-in"}' | SHELL=/bin/sh "$HOST" --keeper-host "$HD" "$HID" >"$S/host.out" 2>>"$S/host.log" &
for _ in $(seq 100); do grep -q . "$S/host.out" 2>/dev/null && break; sleep 0.1; done
out="$(head -1 "$S/host.out")"
[[ "$out" == ready* ]] || fail "host said: $out"
host="${out#ready }"
hk() { "$KEEPER" --dir "$HD" ls | jq_ "d['$1']"; }
shell="$(hk shell_pid)"
say "host $host ($(hk exe)), shell $shell, socket $SOCK"

# Hearth's own window on it: a viewer that counts frames in a file and can
# set the size, as Hearth's terminal does (the keeper's wire format).
HV="$S/hearth-viewer"
python3 - "$SOCK" "$HV" <<'PY' &
import json, os, socket, struct, sys, threading, time
sock, base = sys.argv[1], sys.argv[2]
s = socket.socket(socket.AF_UNIX); s.connect(sock)
def send(o):
    b = json.dumps(o).encode(); s.sendall(struct.pack("<IB", len(b) + 1, 1) + b)
def read():
    h = b""
    while len(h) < 5:
        c = s.recv(5 - len(h))
        if not c: raise EOFError
        h += c
    n, k = struct.unpack("<IB", h); body = b""
    while len(body) < n - 1:
        c = s.recv(n - 1 - len(body))
        if not c: raise EOFError
        body += c
    return k, body
send({"t": "hello", "proto": 1, "role": "viewer", "client": "hearth-stand-in"})
read()
frames = 0
def cmds():
    # A line in <base>.cmd is a JSON message to send (a resize).
    seen = 0
    while True:
        try:
            lines = open(base + ".cmd").read().splitlines()
        except FileNotFoundError:
            lines = []
        for l in lines[seen:]:
            send(json.loads(l))
        seen = len(lines); time.sleep(0.1)
threading.Thread(target=cmds, daemon=True).start()
try:
    while True:
        k, _ = read()
        if k == 2:
            frames += 1
            open(base + ".frames", "w").write(str(frames))
except EOFError:
    pass
PY
hv=$!
hframes() { cat "$HV.frames" 2>/dev/null || echo 0; }
hsend() { echo "$1" >>"$HV.cmd"; }
for _ in $(seq 50); do [[ "$(hframes)" -gt 0 ]] && break; sleep 0.1; done
[[ "$(hframes)" -gt 0 ]] || fail "Hearth's viewer got no frame"
[[ "$(hk viewers)" == "1" ]] || fail "expected Hearth's viewer only"

say "== a cold start: open_kept straight after Suzuri starts, as Hearth sends it"
app="$(start_app "$APP")"
codes=""
for _ in $(seq 200); do
  code="$(curl -s -o "$S/open.json" -w '%{http_code}' -H "Authorization: Bearer $(bridge token)" -d "{\"tool\":\"open_kept\",\"args\":{\"socket\":\"$SOCK\",\"title\":\"from Hearth\",\"from\":\"hearth\"}}" "$(bridge url)/v1/call")"
  codes="$codes $code"
  [[ "$code" == "200" ]] && break
  [[ "$code" == "503" ]] || fail "before it was ready Suzuri answered $code (Hearth retries only 503): $(cat "$S/open.json")"
  sleep 0.1
done
[[ "$code" == "200" ]] || fail "open_kept never answered 200"
say "answers until ready: $(echo $codes | tr ' ' '\n' | uniq -c | awk '{printf "%s x%s  ", $2, $1}')"
front="$(osascript -e 'tell application "System Events" to get unix id of first process whose frontmost is true' 2>/dev/null || echo '?')"
say "frontmost process after the 200: $front (Suzuri is $app)"
[[ "$front" == "$app" ]] || fail "Suzuri's window was not brought to the front"
cold="$(jq_ 'd["pane_id"]' <"$S/open.json")"
sleep 1
[[ "$(hk viewers)" == "2" ]] || fail "cold start: expected two viewers"
call close "{\"pane_id\":$cold}" >/dev/null
sleep 1
[[ "$(hk viewers)" == "1" ]] || fail "cold start: the tab's close did not let go"
say "cold start: pane $cold shown, then its tab closed; the host is back to Hearth's viewer"

say "== 1. GET /tools (app $app)"
get /tools | jq_ '[t for t in d["tools"] if t["name"]=="open_kept"][0]'
get /tools | jq_ 'any(t["name"]=="open_kept" for t in d["tools"])' | grep -q True || fail "/tools has no open_kept"
code="$(curl -s -o "$S/bad.json" -w '%{http_code}' -H "Authorization: Bearer $(bridge token)" -d '{"tool":"open_kept","args":{"socket":"/etc/passwd","from":"hearth"}}' "$(bridge url)/v1/call")"
say "a path that is not a keeper socket: $code $(cat "$S/bad.json")"
[[ "$code" == "400" ]] || fail "a bad path was not refused"
code="$(curl -s -o "$S/bad.json" -w '%{http_code}' -H "Authorization: Bearer $(bridge token)" -d "{\"tool\":\"open_kept\",\"args\":{\"socket\":\"$KDIR/../keeper/../hearth/$HID.sock\"}}" "$(bridge url)/v1/call")"
say "a path with ..: $code $(cat "$S/bad.json")"
[[ "$code" == "400" ]] || fail "a .. path was not refused"
chmod 755 "$HD"
code="$(curl -s -o "$S/bad.json" -w '%{http_code}' -H "Authorization: Bearer $(bridge token)" -d "{\"tool\":\"open_kept\",\"args\":{\"socket\":\"$SOCK\"}}" "$(bridge url)/v1/call")"
chmod 700 "$HD"
say "its folder open to others (0755): $code $(cat "$S/bad.json")"
[[ "$code" == "400" ]] || fail "a socket in an open folder was not refused"
tabs0="$(get /v1/layout | jq_ 'len(d["windows"][0]["tabs"])')"

say "== 2. open_kept {socket, title, from: hearth}, with another app in front"
osascript -e 'tell application "Finder" to activate' >/dev/null 2>&1 || true
sleep 1
say "frontmost before: $(osascript -e 'tell application "System Events" to get name of first process whose frontmost is true' 2>/dev/null)"
code="$(curl -s -o "$S/open.json" -w '%{http_code}' -H "Authorization: Bearer $(bridge token)" -d "{\"tool\":\"open_kept\",\"args\":{\"socket\":\"$SOCK\",\"title\":\"from Hearth\",\"from\":\"hearth\"}}" "$(bridge url)/v1/call")"
say "answer: $code $(jq_ '{"ok": d.get("ok"), "pane_id": d.get("pane_id"), "tab_id": d.get("tab_id")}' <"$S/open.json")"
[[ "$code" == "200" ]] || fail "open_kept answered $code"
pid_="$(jq_ 'd["pane_id"]' <"$S/open.json")"
[[ "$(jq_ 'd["ok"]' <"$S/open.json")" == "True" ]] || fail "no ok"
front="$(osascript -e 'tell application "System Events" to get unix id of first process whose frontmost is true' 2>/dev/null || echo '?')"
say "frontmost after the 200: $front (Suzuri is $app)"
[[ "$front" == "$app" ]] || fail "Suzuri's window was not brought to the front"
[[ "$pid_" != "$HID" ]] || fail "the borrowed pane took the host's id $HID"
tabs1="$(get /v1/layout | jq_ 'len(d["windows"][0]["tabs"])')"
say "Suzuri's pane id for it: $pid_ (the host's id is $HID, Suzuri's own first pane is 1); tabs $tabs0 -> $tabs1"
[[ "$tabs1" -gt "$tabs0" ]] || fail "no new tab"
active="$(get /v1/layout | jq_ 'd["focus_pane"]')"
[[ "$active" == "$pid_" ]] || fail "the new pane does not have the keys ($active)"
sleep 1
[[ "$(hk viewers)" == "2" ]] || fail "expected two viewers, the host says $(hk viewers)"
say "the host has $(hk viewers) viewers: Hearth's and Suzuri's"
"$KEEPER" --dir "$HD" type "$HID" 'echo typed-in-hearth\n'
sleep 1
get "/v1/panes/$pid_?lines=20" | grep -o 'typed-in-hearth' | head -1 || fail "Suzuri does not show what Hearth typed"
post /v1/submit "{\"tab_id\":$pid_,\"line\":\"echo typed-in-suzuri\"}" >/dev/null
sleep 1
"$KEEPER" --dir "$HD" screen "$HID" 0 | grep -o 'typed-in-suzuri' | head -1 || fail "the shell did not get what Suzuri typed"
shot "$app" "$OUT/open-kept.png"

say "== 4. the window being typed in sets the size"
ssize="$(hk cols)x$(hk rows)"
say "host size with Suzuri's pane focused: $ssize"
hsend '{"t":"resize","cols":101,"rows":31}'
sleep 1
say "Hearth's window takes the keys and sets 101x31: host $(hk cols)x$(hk rows)"
[[ "$(hk cols)x$(hk rows)" == "101x31" ]] || fail "Hearth's resize did not reach the host"
call focus '{"pane_id":1}' >/dev/null
call focus "{\"pane_id\":$pid_}" >/dev/null
sleep 1
say "Suzuri's pane takes the keys again: host $(hk cols)x$(hk rows)"
[[ "$(hk cols)x$(hk rows)" == "$ssize" ]] || fail "Suzuri did not send its size again"

say "== 3. closing the tab in Suzuri lets go"
call close "{\"pane_id\":$pid_}" >/dev/null
sleep 1
alive "$host" || fail "the host died when the tab closed"
alive "$shell" || fail "the shell died when the tab closed"
[[ "$(hk viewers)" == "1" ]] || fail "expected Hearth's viewer only, the host says $(hk viewers)"
f0="$(hframes)"
"$KEEPER" --dir "$HD" type "$HID" 'echo after-the-tab-closed\n'
sleep 1
f1="$(hframes)"
"$KEEPER" --dir "$HD" screen "$HID" 0 | grep -o 'after-the-tab-closed' | head -1 || fail "the shell stopped"
[[ "$f1" -gt "$f0" ]] || fail "Hearth's viewer stopped drawing"
say "host $host and shell $shell alive, $(hk viewers) viewer (Hearth's), still drawing: frames $f0 -> $f1"
get /v1/layout | jq_ 'sorted(p["id"] for t in d["windows"][0]["tabs"] for p in t["panes"])'

say "== 3. quitting Suzuri lets go too"
call open_kept "{\"socket\":\"$SOCK\",\"title\":null,\"from\":\"hearth\"}" >/dev/null
sleep 1
[[ "$(hk viewers)" == "2" ]] || fail "not shown again"
kill -TERM "$app"
for _ in $(seq 50); do alive "$app" || break; sleep 0.1; done
alive "$app" && kill -9 "$app"
sleep 1
alive "$host" && alive "$shell" || fail "the shell went with Suzuri"
[[ "$(hk viewers)" == "1" ]] || fail "expected Hearth's viewer only after the quit"
f0="$(hframes)"
"$KEEPER" --dir "$HD" type "$HID" 'echo after-suzuri-quit\n'
sleep 1
[[ "$(hframes)" -gt "$f0" ]] || fail "Hearth's viewer stopped drawing"
say "Suzuri gone; host $host and shell $shell alive, Hearth's viewer still drawing"

say "== the next start does not take it as Suzuri's own"
app2="$(start_app "$APP")"
sleep 2
[[ "$(hk viewers)" == "1" ]] || fail "the next start attached to Hearth's shell"
say "panes after the restart: $(get /v1/layout | jq_ 'sorted(p["id"] for t in d["windows"][0]["tabs"] for p in t["panes"])'); Hearth's host still has 1 viewer"
[[ "$(real_stamp)" == "$before" ]] || fail "the installed suzuri's files changed"
say "PASS"
