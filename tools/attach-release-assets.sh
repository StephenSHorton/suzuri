#!/usr/bin/env bash
# Attach this platform's artifacts to a GitHub Release as soon as they exist.
# Other platforms may attach later; SHA256SUMS is merged with a short retry
# so two publishers do not clobber each other.
set -euo pipefail

tag="${1:-}"
if [[ -z "$tag" ]]; then
  echo "usage: $0 <tag> [files...]" >&2
  exit 2
fi
shift

if [[ $# -gt 0 ]]; then
  files=("$@")
else
  shopt -s nullglob
  files=(suzuri-*)
fi
if [[ ${#files[@]} -eq 0 ]]; then
  echo "no artifacts to attach" >&2
  exit 1
fi
for f in "${files[@]}"; do
  if [[ ! -f "$f" ]]; then
    echo "not a file: $f" >&2
    exit 1
  fi
done

if ! command -v gh >/dev/null; then
  echo "gh is required" >&2
  exit 1
fi

repo="${GH_REPO:-${GITHUB_REPOSITORY:-}}"
gh_rel() {
  if [[ -n "$repo" ]]; then
    gh release "$@" -R "$repo"
  else
    gh release "$@"
  fi
}

python=python3
command -v "$python" >/dev/null 2>&1 || python=python

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
ours="$workdir/ours.sums"
sums="$workdir/SHA256SUMS"

"$python" - "$ours" "${files[@]}" <<'PY'
import hashlib, os, sys

out = sys.argv[1]
paths = sys.argv[2:]
lines = []
for p in paths:
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    lines.append(f"{h.hexdigest()}  {os.path.basename(p)}")
with open(out, "w", encoding="utf-8") as f:
    f.write("\n".join(lines) + ("\n" if lines else ""))
PY

if gh_rel view "$tag" >/dev/null 2>&1; then
  gh_rel upload "$tag" "${files[@]}" --clobber
else
  if ! gh_rel create "$tag" --title "suzuri $tag" --generate-notes "${files[@]}"; then
    gh_rel upload "$tag" "${files[@]}" --clobber
  fi
fi

for attempt in 1 2 3 4 5 6; do
  existing="$workdir/SHA256SUMS.existing"
  rm -f "$existing"
  gh_rel download "$tag" -p SHA256SUMS -O "$existing" 2>/dev/null || true

  "$python" - "$ours" "$existing" "$sums" <<'PY'
import os, sys

ours, existing, dest = sys.argv[1], sys.argv[2], sys.argv[3]

def load(path):
    d = {}
    if not os.path.isfile(path) or os.path.getsize(path) == 0:
        return d
    with open(path, encoding="utf-8") as f:
        for line in f:
            fields = line.split()
            if len(fields) >= 2:
                name = os.path.basename(fields[-1].lstrip("*"))
                d[name] = fields[0].lower()
    return d

d = load(existing)
d.update(load(ours))
with open(dest, "w", encoding="utf-8") as f:
    for name in sorted(d):
        f.write(f"{d[name]}  {name}\n")
PY

  gh_rel upload "$tag" "$sums" --clobber

  check="$workdir/SHA256SUMS.check"
  rm -f "$check"
  gh_rel download "$tag" -p SHA256SUMS -O "$check"
  if "$python" - "$ours" "$check" <<'PY'
import os, sys

def load(path):
    d = set()
    if not os.path.isfile(path):
        return d
    with open(path, encoding="utf-8") as f:
        for line in f:
            fields = line.split()
            if len(fields) >= 2:
                d.add(os.path.basename(fields[-1].lstrip("*")))
    return d

ours, combined = load(sys.argv[1]), load(sys.argv[2])
missing = sorted(ours - combined)
raise SystemExit(1 if missing else 0)
PY
  then
    echo "attached ${#files[@]} files to $tag (SHA256SUMS merge attempt $attempt)"
    exit 0
  fi
  echo "SHA256SUMS race on $tag, retry $attempt" >&2
  sleep $((attempt * 2))
done

echo "failed to merge SHA256SUMS for $tag after retries" >&2
exit 1
