# Crush design patterns (extracted from source)

Charm does **not** ship a stylesheet. **Crush** does — in `internal/ui/styles/quickstyle.go` and `internal/ui/dialog/`. These are the numbers and rules that make Crush look intentional.

## Semantic palette (not random hex)

Crush builds every style from a **role map** (`quickStyleOpts`):

| Role | Use |
|------|-----|
| `primary` | Borders, focus, title, accent chrome |
| `secondary` | Gradients, spinners, secondary focus |
| `onPrimary` | Text **on** primary fills (contrast pair) |
| `fgBase` / `fgMoreSubtle` / `fgMostSubtle` | Text ladder (body → mute → whisper) |
| `bgBase` / `bgLessVisible` / `bgLeastVisible` / `bgMostVisible` | Surface ladder |
| `separator` | Rules, help separators, scrollbar track |
| status: `error`, `success`, `warning`, … | Tags and destructive chrome |

**Rule:** never pick a hex for a widget — pick a **role**. Theme swaps only change the role map.

## Dialog chrome (the “pretty card”)

From `quickstyle.go` + `dialog/common.go` + `commands.go`:

| Property | Crush value |
|----------|-------------|
| Outer border | `lipgloss.RoundedBorder()` |
| Border color | **`primary`** (not a random neon, not mute grey) |
| Frame padding | **`Padding(1, 2)`** on quit frame; list items **`Padding(0, 1)`** |
| Max width | **`defaultDialogMaxWidth = 70`** cells (clamp to window − border) |
| Models dialog | max **73** |
| Notifications | max **50×12** |
| Title | `Padding(0, 1)` + `Foreground(primary)` |
| Help row | `Padding(0, 1)`, keys = `fgMoreSubtle`, desc = `fgMostSubtle` |
| Normal list row | `Padding(0, 1)` + `fgBase` |
| **Selected row** | `Padding(0, 1)` + **`Background(primary)` + `Foreground(onPrimary)`** (full invert, not a thin side bar) |
| Filter input | `Margin(1, 1)` around prompt |
| List block | `Margin(0, 0, 1, 0)` (space above help) |
| Content panel | `Background(bgLessVisible)` + `Padding(1, 2)` |
| Compact breakpoint | width **≥ 120** before some chrome appears |

**Inner width math (critical):**

```
dialogWidth  = min(70, windowWidth - borderHorizontal)
innerWidth   = dialogWidth - View.GetHorizontalFrameSize()  // border + padding
listHeight   = dialogHeight - title - input - help - frames
```

Crush never sets `Width(n)` on a bordered box without sizing **content** to the **inner** width. Lip Gloss v2: `Width` is **total** including border/padding.

**Info column:** hide shortcuts/timestamps when they exceed **25%** (commands) or **35%** (sessions) of row width.

## Selection & contrast

- Selected = **filled primary bar**, text **onPrimary** (high contrast).
- Not: dim text + thin left border only (reads as “broken list”).
- Tags: `Padding(0, 1)` + solid status background + onPrimary (or bgBase on warning).

## Borders by purpose

| Surface | Border |
|---------|--------|
| Dialog / quit / compact details | **Rounded** + **primary** |
| Focused message / shell bar | **Left edge only** + primary (or success) — not a full box |
| Pills focused | Rounded + `bgMostVisible` (quiet) |
| Blurred focus chrome | Same geometry, **fgMoreSubtle** instead of primary |

## Tabs (Crush’s question-form tabs, UV)

- Rounded outer path.
- **Active tab: open bottom** so it merges into the content panel (no double line).
- Inactive: closed bottom.
- Active label: `fgBase`; inactive: `fgMoreSubtle`.
- All tab borders use **primary** (blurred: `fgMoreSubtle`).

## Spacing ladder (cells)

| Token | Typical |
|-------|---------|
| Inline chip padding | `(0, 1)` |
| Dialog body padding | `(1, 2)` or `(1)` |
| Nested indent | `PaddingLeft(2)` |
| Section margin | `MarginBottom(1)`, list `Margin(0,0,1,0)` |
| Icon margin | `MarginRight(1)` / `MarginLeft(2)` |

## Color of chrome vs content

- Dialog **title / border / selected row** → primary.
- Body text → fgBase; meta → fgMoreSubtle / fgMostSubtle.
- Destructive dialogs flip border/title to **destructive**, selected to destructive + onPrimary.

## What suzuri got wrong (and should copy)

| Crush | We often did |
|-------|----------------|
| Primary border on dialogs | Mute grey border (looked dead) or hot pink (looked loud) |
| Selected = primary fill + onPrimary | Side bar / soft sel only |
| Padding `(1,2)` frame, `(0,1)` rows | Inconsistent / cramped |
| Max width ~70, clamp to window | Arbitrary 44–52 without frame math |
| Role palette | One-off hex per widget |
| Left-edge focus for content | Full boxes on tabs (cell-grid hell) |

## Apply checklist for suzuri chrome

1. Dialog: rounded + **accent/primary** border + `Padding(1, 2)`.
2. Selected list/settings row: **full primary background + onPrimary text**.
3. Title: primary (or soft accent), `Padding(0, 1)`.
4. Hints: `fgMostSubtle`; keys slightly stronger than descriptions.
5. Max dialog width **min(56, cols-4)** (host is narrower than Crush’s 70 often).
6. Tabs: no full rounded boxes at 1-cell height — use **pill fill** (selected) or **left accent**; leave rounded borders for dialogs only.
7. One `Styles`/token apply path so theme switch rebuilds all of the above.

Source of truth in clone:  
`C:\Users\4step\projects\crush\internal\ui\styles\quickstyle.go`  
`C:\Users\4step\projects\crush\internal\ui\dialog\common.go`
