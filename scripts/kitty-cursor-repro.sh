#!/bin/sh
# Kitty graphics placement check for a suzuri pane. Run it as the pane's
# command (RSZ_CMD=scripts/kitty-cursor-repro.sh cargo run -p suzuri) or
# inside a pane. Needs python3 (it makes the 160x96 PNG itself).
#
# Scene A: an image placed at (row 3, col 5), text at explicit rows: both
#          land where asked.
# Scene B: text printed right after the image, no cursor move. Kitty moves
#          the cursor past the image; suzuri leaves it on the image's top-left
#          cell, so the text overwrites that cell, which is the image's
#          anchor (an OSC 8 mark), and the image disappears.
# Scene C: the same, but the text starts one column over: the image stays.
B=$(python3 - <<'PY'
import base64, struct, zlib
w, h = 160, 96
raw = b""
for y in range(h):
    raw += b"\x00"
    for x in range(w):
        edge = x < 3 or y < 3 or x >= w - 3 or y >= h - 3
        raw += bytes((255, 255, 255)) if edge else bytes((int(255 * x / w), 80, int(255 * y / h)))
def chunk(t, d):
    return struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")
print(base64.b64encode(png).decode())
PY
)
printf '\033[2J\033[H'
printf '\033[1;1HSCENE A: image at row 3 col 5, text at explicit rows'
printf '\033[3;5H'
printf '\033_Ga=T,i=1,f=100,t=d;%s\033\\' "$B"
printf '\033[12;1HROW12 explicit: text below the image'
printf '\033[16;1HSCENE B: image at row 17 col 5, text printed right after it (image vanishes)'
printf '\033[17;5H'
printf '\033_Ga=T,i=2,f=100,t=d;%s\033\\' "$B"
printf 'AFTER-IMAGE-TEXT'
printf '\033[21;40HSCENE C: image at row 22 col 40, text at row 22 col 41 (image stays)'
printf '\033[22;40H'
printf '\033_Ga=T,i=3,f=100,t=d;%s\033\\' "$B"
printf '\033[22;41HTEXT-ON-IMAGE-FIRST-ROW'
printf '\033[30;1HROW30 explicit end'
exec sleep 3600
