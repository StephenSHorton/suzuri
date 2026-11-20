"""Bakes the outline of 硯 (U+786F) from the font files into the SVGs beside
this file, so the logo is plain shapes and needs no font.

    python bake.py "NotoSansJP[wght].ttf" ZenOldMincho-Black.ttf

Needs fonttools (pip install fonttools). Where the fonts come from, and their
licence, is in ../README.md. The fonts themselves are not kept in this repo.
"""
import os
import sys

from fontTools.pens.basePen import BasePen
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

HERE = os.path.dirname(os.path.abspath(__file__))
CHAR = 0x786F


class Plain(BasePen):
    """The outline as M/L/Q/C/Z with y pointing down, in whole font units."""

    def __init__(self, glyphs):
        super().__init__(glyphs)
        self.d = []

    def put(self, letter, *points):
        self.d.append(letter + " ".join(f"{round(x)} {round(-y)}" for x, y in points))

    def _moveTo(self, p):
        self.put("M", p)

    def _lineTo(self, p):
        self.put("L", p)

    def _qCurveToOne(self, c, p):
        self.put("Q", c, p)

    def _curveToOne(self, a, b, p):
        self.put("C", a, b, p)

    def _closePath(self):
        self.d.append("Z")


def bake(path, name, label, weight=None):
    font = TTFont(path)
    if weight is not None:
        font = instancer.instantiateVariableFont(font, {"wght": weight})
    if font["head"].unitsPerEm != 1000:
        raise SystemExit(f"{path}: expected 1000 units to the em")
    glyphs = font.getGlyphSet()
    pen = Plain(glyphs)
    glyphs[font.getBestCmap()[CHAR]].draw(pen)
    with open(os.path.join(HERE, f"{name}.svg"), "w", encoding="utf-8") as out:
        out.write(
            '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 -880 1000 1000" fill="currentColor" stroke="none">'
            f"<!-- U+786F from {label}. SIL Open Font License 1.1: see ../README.md and the OFL files here. -->"
            "<style>svg{color:#1c1917}@media(prefers-color-scheme:dark){svg{color:#f5f5f4}}</style>\n"
            f'<path d="{"".join(pen.d)}"/>\n</svg>\n'
        )


if len(sys.argv) != 3:
    raise SystemExit(__doc__)
noto, zen = sys.argv[1], sys.argv[2]
bake(noto, "noto-sans-jp-700", "Noto Sans JP, weight 700 (instanced from the variable font)", 700)
bake(zen, "zen-old-mincho-900", "Zen Old Mincho Black")
