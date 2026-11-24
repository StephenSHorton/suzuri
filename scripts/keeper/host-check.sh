#!/bin/sh
# The standalone keeper host, rsz-keeper-host (card hearth#190), on its
# own: no Suzuri app, no Suzuri folders. Copies the host and the rsz-keeper
# client into an empty folder, starts a shell there the way Suzuri and
# Hearth do (`--keeper-host <dir> <id>`, JSON on stdin), types into it with
# rsz-keeper, reads its screen back, and closes it. macOS and Linux.
#   scripts/keeper/host-check.sh [HOST] [RSZ-KEEPER]
# Defaults: target/release/rsz-keeper-host and target/release/rsz-keeper
# (cargo build --release -p rsz-keeper-host -p rsz-keeper).
set -eu
R=$(cd "$(dirname "$0")/../.." && pwd)
HOST_SRC=${1:-$R/target/release/rsz-keeper-host}
KEEPER_SRC=${2:-$R/target/release/rsz-keeper}
fail() { echo "FAIL: $*"; exit 1; }
[ -x "$HOST_SRC" ] || fail "no host program at $HOST_SRC"
[ -x "$KEEPER_SRC" ] || fail "no rsz-keeper at $KEEPER_SRC"

# Short: a socket path must fit in 104 bytes.
T=/tmp/rszh-$$
mkdir -p "$T/bin" "$T/k"
chmod 700 "$T/k"
trap '"$T/bin/rsz-keeper" --dir "$T/k" close 1 >/dev/null 2>&1 || true; rm -rf "$T"' EXIT
cp "$HOST_SRC" "$T/bin/rsz-keeper-host"
cp "$KEEPER_SRC" "$T/bin/rsz-keeper"
H="$T/bin/rsz-keeper-host"
K="$T/bin/rsz-keeper"

echo "== system: $(uname -srm)"
[ -r /etc/os-release ] && . /etc/os-release && echo "   $PRETTY_NAME"
echo "== the host program, alone in $T/bin"
ls -l "$T/bin" | sed 's/^/   /'
command -v file >/dev/null && file "$H" | sed "s|$T/bin/||; s/^/   /"
"$H" --version | sed 's/^/   /'
echo "== what it links"
case "$(uname -s)" in
  Darwin) otool -L "$H" | tail -n +2 | sed 's/^[[:space:]]*/   /' ;;
  *) ldd "$H" | sed 's/^[[:space:]]*/   /'
     command -v objdump >/dev/null && echo "   newest glibc symbol: $(objdump -T "$H" | grep -o 'GLIBC_[0-9.]*' | sort -t. -k2,2n -k3,3n | tail -1)" ;;
esac
command -v suzuri >/dev/null && echo "   (a suzuri is on PATH: $(command -v suzuri); not used)" || echo "   no suzuri on PATH"

echo "== start a kept shell: rsz-keeper-host --keeper-host $T/k 1"
echo '{"cols":80,"rows":24,"command":["/bin/sh"],"version":"host-check"}' |
  SHELL=/bin/sh "$H" --keeper-host "$T/k" 1 >"$T/out" 2>"$T/err" &
i=0
until grep -q . "$T/out" 2>/dev/null; do
  i=$((i + 1)); [ $i -lt 100 ] || fail "no ready line; stderr: $(cat "$T/err")"; sleep 0.1
done
read -r ready <"$T/out"
case "$ready" in ready*) echo "   host said: $ready" ;; *) fail "host said: $ready" ;; esac
pid=${ready#ready }

echo "== rsz-keeper ls (the host's hello)"
hello=$("$K" --dir "$T/k" ls)
echo "   $hello"
echo "$hello" | grep -q '"exe":"'"$H"'"' || fail "the hello names another program"
echo "$hello" | grep -q '"proto":' || fail "no proto in the hello"

echo "== type into it and read its screen"
"$K" --dir "$T/k" type 1 'echo kept-$((6*7)) on $(uname -s)
'
i=0
until "$K" --dir "$T/k" screen 1 | grep -q '^kept-42 on'; do
  i=$((i + 1)); [ $i -lt 50 ] || { "$K" --dir "$T/k" screen 1; fail "no kept-42 on the screen"; }; sleep 0.1
done
"$K" --dir "$T/k" screen 1 | grep -v '^[[:space:]]*$' | sed 's/^/   | /'

echo "== close it: the shell and its host end"
"$K" --dir "$T/k" close 1
i=0
while kill -0 "$pid" 2>/dev/null; do
  i=$((i + 1)); [ $i -lt 50 ] || fail "host $pid still runs"; sleep 0.1
done
[ ! -e "$T/k/1.sock" ] || fail "socket left behind"
echo "   host $pid ended, socket removed"
echo "PASS: rsz-keeper-host hosted a shell on its own"
