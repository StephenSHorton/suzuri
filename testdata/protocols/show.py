#!/usr/bin/env python3
"""Print the notice and image sequences suzuri and Go suzuri both take
(card #288), so the two can be compared side by side. Run it in the terminal
under test:

    python3 testdata/protocols/show.py notices   # OSC 9, 777, 99 cards
    python3 testdata/protocols/show.py inline    # OSC 1337, OSC 7879, a printed path
    python3 testdata/protocols/show.py kitty     # Kitty a=T, chunked, raw RGBA, a=p, a=d
    python3 testdata/protocols/show.py progress  # ConEmu OSC 9;4
    python3 testdata/protocols/show.py all

Works the same on macOS and Windows (py -3 on the PC).
"""
import base64
import os
import sys
import time
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
SAMPLE = os.path.join(HERE, "sample.png")
SMALL = os.path.join(HERE, "small.png")
ESC = "\x1b"
ST = ESC + "\\"


def out(s):
    sys.stdout.write(s)
    sys.stdout.flush()


def b64(data):
    return base64.b64encode(data).decode()


def notices():
    out("OSC 9 (iTerm2):\n")
    out(f"{ESC}]9;OSC 9: build finished\a")
    time.sleep(0.6)
    out("OSC 777 (urxvt / WezTerm / Ghostty):\n")
    out(f"{ESC}]777;notify;OSC 777 title;the body under it\a")
    time.sleep(0.6)
    out("OSC 99 (kitty), in two chunks, critical with the error sound:\n")
    out(f"{ESC}]99;i=job1:d=0:u=2:s=error;OSC 99 failed{ST}")
    out(f"{ESC}]99;i=job1:p=body;step 3 of 7 broke{ST}")
    time.sleep(0.6)
    out("OSC 99 warning icon, base64 title, stays until clicked (w=0):\n")
    out(f"{ESC}]99;i=sticky:d=0:e=1:n=warn:w=0;{b64('OSC 99 sticky warning'.encode())}{ST}")
    out(f"{ESC}]99;i=sticky:d=1:p=body;right-click to dismiss{ST}")
    out("\n")


def inline7879():
    out("OSC 7879 image=<path> (suzuri's own):\n")
    out(f"{ESC}]7879;image={SMALL}\a")


def inlinepath():
    out("A path printed as text (Go's path scan):\n")
    out(f"saved {SAMPLE}\n")


def inline():
    data = open(SAMPLE, "rb").read()
    out("OSC 1337 (iTerm2 inline image):\n")
    out(f"{ESC}]1337;File=name={b64(b'sample.png')};size={len(data)};inline=1:{b64(data)}\a")
    out("OSC 7879 image=<path> (suzuri's own):\n")
    out(f"{ESC}]7879;image={SMALL}\a")
    out("A path printed as text (Go's path scan):\n")
    out(f"saved {SAMPLE}\n")


def kitty():
    png = open(SMALL, "rb").read()
    out("Kitty a=T, f=100 (PNG), 8 x 4 cells, at the cursor:\n")
    out(f"{ESC}_Ga=T,i=31,f=100,c=8,r=4;{b64(png)}{ST}")
    out("\n" * 5)
    big = open(SAMPLE, "rb").read()
    enc = b64(big)
    out("Kitty a=t in 4096-byte chunks (m=1), then a=p 24 x 6 cells:\n")
    first = True
    for i in range(0, len(enc), 4096):
        part = enc[i:i + 4096]
        more = 1 if i + 4096 < len(enc) else 0
        head = "a=t,i=32,f=100," if first else ""
        out(f"{ESC}_G{head}m={more};{part}{ST}")
        first = False
    out(f"{ESC}_Ga=p,i=32,c=24,r=6{ST}")
    out("\n" * 7)
    w, h = 64, 16
    rgba = bytes(v for y in range(h) for x in range(w) for v in (x * 4, 255 - x * 4, y * 16, 255))
    out("Kitty raw RGBA (f=32), zlib (o=z), 16 x 2 cells:\n")
    out(f"{ESC}_Ga=T,i=33,f=32,s={w},v={h},o=z,c=16,r=2;{b64(zlib.compress(rgba))}{ST}")
    out("\n" * 3)
    out("Deleting image 31 in 3 s (a=d)...\n")
    time.sleep(3)
    out(f"{ESC}_Ga=d,d=i,i=31{ST}")
    out("done.\n")


def progress():
    for pct in range(0, 101, 20):
        out(f"{ESC}]9;4;1;{pct}\a\rprogress {pct}%   ")
        time.sleep(0.3)
    out(f"{ESC}]9;4;0;0\a\n")


def main():
    what = sys.argv[1] if len(sys.argv) > 1 else "all"
    steps = {"notices": [notices], "inline": [inline], "inline7879": [inline7879], "inlinepath": [inlinepath], "kitty": [kitty], "progress": [progress], "all": [notices, inline, kitty, progress]}
    for f in steps.get(what, []):
        f()


if __name__ == "__main__":
    main()
