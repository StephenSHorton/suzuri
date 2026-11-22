#!/usr/bin/env bash
# Card hearth#916 on a scratch debug build: after a "reboot" (the app and
# every kept shell killed with -9), the next start shows the same tabs and
# splits, a fresh shell in each old folder, the old text above it marked
# restored, and a pane that was running Claude Code offers the resume in its
# input bar without running it. Claude Code is a stand-in script here that
# writes the session file the real one does (~/.claude/sessions/<pid>.json),
# in the scratch HOME; a second stand-in is a dev server (not Claude Code).
#   cargo build -p suzuri -p rsz-keeper && scripts/keeper/reboot-check.sh
source "$(dirname "$0")/lib.sh"
APP="$ROOT/target/debug/suzuri"
[[ -x "$APP" && -x "$KEEPER" ]] || fail "build first: cargo build -p suzuri -p rsz-keeper"
before="$(real_stamp)"
OUT="${SHOTS:-$S}"
SID="7f3c2a10-5b6d-4e8f-9a01-23456789abcd"

mkdir -p "$S/work/alpha" "$S/work/beta" "$S/work/gamma" "$S/bin"
cat >"$S/bin/claude" <<EOF
#!/bin/bash
# Stand-in for Claude Code: its session file, then it waits.
if [[ "\$1" == "--resume" ]]; then echo "RESUMED \$2" >>"$S/resumed.log"; fi
mkdir -p "\$HOME/.claude/sessions"
printf '{"pid":%d,"sessionId":"$SID","cwd":"%s"}' \$\$ "\$PWD" >"\$HOME/.claude/sessions/\$\$.json"
echo \$\$ >>"$S/stand.pids"
echo "Claude Code (stand-in) in \$PWD, conversation $SID"
exec sleep 100000
EOF
cat >"$S/bin/devserver" <<EOF
#!/bin/bash
# A long-running program that is not Claude Code.
echo \$\$ >>"$S/stand.pids"
echo "dev server (stand-in) serving \$PWD"
exec sleep 100000
EOF
chmod +x "$S/bin/claude" "$S/bin/devserver"

say "== scratch: $S"
app1="$(start_app "$APP" PATH="$S/bin:$PATH")"
# Each app process (window) keeps its own session.
RDIR="$S/cfg/restore/$app1"
sleep 2
p1="$(kept | jq_ 'd["id"]' | head -1)"
[[ -n "$p1" ]] || fail "no pane after start"
post /v1/submit "{\"tab_id\":$p1,\"line\":\"cd $S/work/alpha && for i in \$(seq 1 300); do echo alpha-line-\$i; done\"}" >/dev/null
sleep 1
p2="$(call split "{\"pane_id\":$p1,\"axis\":\"right\"}" | jq_ 'd["pane_id"]')"
sleep 1.5
post /v1/submit "{\"tab_id\":$p2,\"line\":\"cd $S/work/beta && echo beta-before-claude && claude\"}" >/dev/null
p3="$(call new_tab "{}" | jq_ 'd["pane_id"]')"
sleep 1.5
post /v1/submit "{\"tab_id\":$p3,\"line\":\"cd $S/work/gamma && echo gamma-here && devserver\"}" >/dev/null
call focus "{\"pane_id\":$p1}" >/dev/null
sleep 5
say "panes: $p1 | $p2 (Claude Code) in tab 1; $p3 (a dev server, cd on the same line) in tab 2"
[[ -s "$RDIR/session.json" ]] || fail "no session saved in $RDIR"
say "saved: $(stat -f '%Sp %N' "$S/cfg/restore" "$RDIR" "$RDIR/session.json" "$RDIR/$p1.txt" | tr '\n' ' ')"
python3 - "$RDIR/session.json" "$SID" <<'PY' || fail "the saved session is not what was open"
import json, sys
s = json.load(open(sys.argv[1]))
t = s["tabs"]
assert len(t) == 2, t
tree = t[0]["tree"]
assert tree["dir"] == "h" and tree["b"]["claude"]["session"] == sys.argv[2], tree
assert tree["a"]["cwd"].endswith("/work/alpha"), tree["a"]
assert t[1]["tree"]["cwd"].endswith("/work/gamma"), f"a busy pane's folder is the shell's live one: {t[1]['tree']}"
print("session.json:", json.dumps(t)[:500])
PY
shot "$app1" "$OUT/reboot-before.png"

say "== reboot: kill -9 the app, every keeper host and every shell"
hosts="$(kept | python3 -c 'import json,sys
for l in sys.stdin:
  d=json.loads(l); print(d["pid"], d.get("shell_pid") or "")')"
kill -9 "$app1"
for p in $hosts; do kill -9 -- "-$p" 2>/dev/null; kill -9 "$p" 2>/dev/null || true; done
for p in $(cat "$S/stand.pids" 2>/dev/null); do kill -9 "$p" 2>/dev/null || true; done
rm -f "$S/stand.pids"
sleep 2
alive "$app1" && fail "app still alive"
# Their sockets stay behind, as after a real reboot; none answers.
kept | grep -v '"error"' | grep -q . && fail "kept shells left: $(kept)"
rm -f "$S/home/.claude/sessions/"*.json
say "nothing left running; restore files: $(cd "$S/cfg/restore" && ls -d */* | tr '\n' ' ')"

say "== start again"
app2="$(start_app "$APP" PATH="$S/bin:$PATH")"
sleep 5
call layout "{}" >"$S/layout.json"
python3 - "$S/layout.json" "$S" "$SID" <<'PY' || fail "not restored as it was"
import json, sys
L = json.load(open(sys.argv[1])); S = sys.argv[2]; sid = sys.argv[3]
tabs = L["windows"][0]["tabs"]
assert len(tabs) == 2, f"2 tabs, got {len(tabs)}"
t1 = tabs[0]["tree"]
assert t1.get("axis") == "right" and "pane" in t1["a"] and "pane" in t1["b"], f"tab 1 is a split right: {t1}"
assert tabs[1]["tree"].get("pane"), f"tab 2 is one pane: {tabs[1]['tree']}"
assert tabs[0]["active"], "tab 1 in front"
panes = {p["id"]: p for t in tabs for p in t["panes"]}
a, b = (panes[t1["a"]["pane"]], panes[t1["b"]["pane"]])
c = panes[tabs[1]["tree"]["pane"]]
print("1. tabs and splits:", json.dumps([t["tree"] for t in tabs]))
for p, d in ((a, "alpha"), (b, "beta"), (c, "gamma")):
    assert p["cwd"].endswith("/work/" + d), f"pane {p['id']} starts in {d}: {p['cwd']!r}"
    print(f"2. pane {p['id']}: a new shell in {p['cwd']}")
print("4. input bar of the Claude Code pane:", repr(b["input"]), "| the others:", repr(a["input"]), repr(c["input"]))
assert b["input"] == f"claude --resume {sid}", b["input"]
assert a["input"] == "" and c["input"] == ""
PY
leaf() { python3 -c 'import json,sys; L=json.load(open(sys.argv[1])); t=L["windows"][0]["tabs"]; print(eval(sys.argv[2]))' "$S/layout.json" "$1"; }
pa="$(leaf 't[0]["tree"]["a"]["pane"]')"; pb="$(leaf 't[0]["tree"]["b"]["pane"]')"; pc="$(leaf 't[1]["tree"]["pane"]')"
txt="$(get "/v1/panes/$pa?lines=400")"
echo "$txt" | grep -q 'alpha-line-300' || fail "old text not above pane $pa"
echo "$txt" | grep -q 'alpha-line-1\b\|alpha-line-1"' || fail "old text not all there"
echo "$txt" | grep -q '─── restored · saved' || fail "no restored mark in pane $pa"
say "3. pane $pa: old lines alpha-line-1..300, then: $(echo "$txt" | grep -o '─── restored[^─]*───' | head -1)"
txtc="$(get "/v1/panes/$pc?lines=400")"
echo "$txtc" | grep -q 'dev server (stand-in) serving' || fail "old text not above pane $pc (the tab not in front)"
echo "$txtc" | grep -q '─── restored · saved' || fail "no restored mark in pane $pc"
say "3. pane $pc (the tab not in front): its old text and the restored mark too"
txtb="$(get "/v1/panes/$pb?lines=400")"
echo "$txtb" | grep -q 'Claude Code was running here' || fail "no resume offer in pane $pb"
sleep 2
[[ ! -e "$S/resumed.log" ]] || fail "claude was run by itself: $(cat "$S/resumed.log")"
[[ -s "$S/stand.pids" ]] && fail "a program started by itself: $(cat "$S/stand.pids")"
say "4. nothing ran by itself: no Claude Code, no dev server, no resume"
shot "$app2" "$OUT/reboot-after.png"

say "== the offered line, submitted in that pane, resumes the conversation"
post /v1/submit "{\"tab_id\":$pb,\"line\":\"claude --resume $SID\"}" >/dev/null
sleep 2
grep -q "RESUMED $SID" "$S/resumed.log" || fail "the resume did not run"
say "ran: $(cat "$S/resumed.log")"

say "== a second window (its own app process) opens fresh: it takes nothing from the first"
env HOME="$S/home" LOCALAPPDATA="$S/local" RSZ_CONFIG_DIR="$S/cfg" RSZ_KEEPER_DIR="$KDIR" PATH="$S/bin:$PATH" "$APP" >>"$S/app.log" 2>&1 &
app3=$!
echo "$app3" >>"$S/app.pids"
sleep 7
python3 - "$S/cfg/restore" "$app2" "$app3" <<'PY' || fail "the second window took the first one's session"
import json, sys, os
root, a2, a3 = sys.argv[1:]
n = lambda pid: len(json.load(open(os.path.join(root, pid, "session.json")))["tabs"])
assert n(a2) == 2 and n(a3) == 1, (n(a2), n(a3))
print(f"window {a2}: 2 tabs; window {a3}: 1 fresh tab; session folders: {sorted(os.listdir(root))}")
PY

say "== a logout kills the shells before the app: the saved session is kept whole"
for id in $pa $pb $pc; do
  h="$(kept_field "$id" pid)"
  [[ -n "$h" && "$h" != "None" ]] && kill -9 -- "-$h" 2>/dev/null; kill -9 "$h" 2>/dev/null || true
done
for p in $(cat "$S/stand.pids" 2>/dev/null); do kill -9 "$p" 2>/dev/null || true; done
sleep 4
alive "$app2" && kill -9 "$app2"
python3 - "$S/cfg/restore/$app2/session.json" <<'PY' || fail "the shells dying first shrank or cleared the session"
import json, sys
s = json.load(open(sys.argv[1]))
def leaves(n): return [n] if "id" in n else leaves(n["a"]) + leaves(n["b"])
n = [len(leaves(t["tree"])) for t in s["tabs"]]
assert n == [2, 1], n
print(f"session after the shells died first: tabs with {n} panes")
PY

[[ "$(real_stamp)" == "$before" ]] || fail "the installed suzuri's files changed"
say "5. the installed suzuri's files are untouched: ${before:-none of them exist}"
say "PASS"
