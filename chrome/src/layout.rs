//! Fixed layout constants — keep chrome + terminal hole in sync.
//!
//! # Spacing system (8-point)
//!
//! All chrome gutters come from [`Spacing`]. Do **not** invent one-off 10/12/14
//! gaps. The rule is:
//!
//! | Token | Default | Use for |
//! |-------|---------|---------|
//! | `unit` | 8 | base grid |
//! | `edge` | 16 (2×) | **window edges only** — left, right, bottom |
//! | `stack` | 8 (1×) | pane ↔ pane (splits); not chrome→workspace (that is flush) |
//! | `inset` | 8 (1×) | padding **inside** glass (terminal text, warp field) |
//! | `cluster` | 8 (1×) | gap between sibling nav chips |
//!
//! Chrome bar → workspace is flush (`top_pad = 0`) so the jelly bridge stays
//! short without sliding nav chips down. Edge is the only larger perimeter token.

/// 8-point spacing tokens. Prefer these over magic numbers in layout/paint.
#[derive(Clone, Copy, Debug)]
pub struct Spacing {
    /// Base unit (always 8).
    pub unit: f32,
    /// Window edge inset (left / right / bottom).
    pub edge: f32,
    /// Gap between stacked chrome regions (nav→terminal, terminal→warp).
    pub stack: f32,
    /// Inner padding inside glass panels.
    pub inset: f32,
    /// Gap between sibling controls in a row (tab chips, +).
    pub cluster: f32,
}

impl Default for Spacing {
    fn default() -> Self {
        let unit = 8.0;
        Self {
            unit,
            edge: unit * 2.0, // 16 — window perimeter only
            stack: unit,      // 8 — between regions
            inset: unit,      // 8 — inside glass
            cluster: unit,    // 8 — chip-to-chip
        }
    }
}

impl Spacing {
    /// Half-unit (4) — optical only (e.g. icon nudge). Not for layout gutters.
    pub fn half(self) -> f32 {
        self.unit * 0.5
    }
}

/// Window chrome metrics (logical CSS-px). Structural sizes + spacing tokens.
#[derive(Clone, Copy, Debug)]
pub struct Metrics {
    pub title_h: f32,
    pub tab_h: f32,
    /// Reserved height **inside** the single glass pane for ASCII separator +
    /// local command input (not a second glass pane).
    pub input_strip_h: f32,
    /// Corner radius for the primary glass well.
    pub radius: f32,
    /// Corner radius for nav chips (tabs / + / settings).
    pub chip_radius: f32,
    pub spacing: Spacing,
}

impl Default for Metrics {
    fn default() -> Self {
        let s = Spacing::default();
        Self {
            // Single chrome bar: traffic lights + tabs + logo (no separate tab strip).
            title_h: s.unit * 5.0, // 40
            tab_h: 0.0,            // tabs live in the title bar
            // divider + path + input = 3 mono cell rows (14 each) so text never overflows
            input_strip_h: 14.0 * 3.0, // 42
            radius: s.unit * 2.0,        // 16
            chip_radius: s.unit,         // 8
            spacing: s,
        }
    }
}

impl Metrics {
    /// Window edge inset (left / right / bottom).
    #[inline]
    pub fn edge(self) -> f32 {
        self.spacing.edge
    }

    /// Gap between stacked regions (nav→pane, pane→pane).
    #[inline]
    pub fn stack(self) -> f32 {
        self.spacing.stack
    }

    /// Inner glass content inset.
    #[inline]
    pub fn inset(self) -> f32 {
        self.spacing.inset
    }

    /// Chip cluster gap.
    #[inline]
    pub fn cluster(self) -> f32 {
        self.spacing.cluster
    }
}

/// Axis-aligned rect in top-left origin, logical pixels.
#[derive(Clone, Copy, Debug, Default)]
pub struct Rect {
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
}

impl Rect {
    pub fn new(x: f32, y: f32, w: f32, h: f32) -> Self {
        Self { x, y, w, h }
    }

    /// Inclusive min, exclusive max (logical px, top-left origin).
    #[inline]
    pub fn contains(self, x: f32, y: f32) -> bool {
        x >= self.x && y >= self.y && x < self.x + self.w && y < self.y + self.h
    }

    /// Axis-aligned overlap (used to clip terminal glyphs under overlay cards).
    #[inline]
    pub fn intersects(self, other: Rect) -> bool {
        self.x < other.x + other.w
            && other.x < self.x + self.w
            && self.y < other.y + other.h
            && other.y < self.y + self.h
    }
}

/// Per-pane geometry inside the workspace (one glass + footer per leaf).
#[derive(Clone, Debug)]
pub struct PaneLayout {
    pub pane_id: u64,
    pub glass: Rect,
    pub cells: Rect,
    pub divider: Rect,
    pub path: Rect,
    pub warp: Rect,
    /// Top chrome strip (title + close).
    pub header: Rect,
    pub title_pill: Rect,
    pub close: Rect,
    pub focused: bool,
}

/// One chrome frame’s computed geometry.
#[derive(Clone, Debug)]
#[allow(dead_code)] // tabs / tab_active / tab_idle kept for layout contract + tests
pub struct FrameLayout {
    pub title: Rect,
    pub tabs: Rect,
    /// Full workspace rectangle (all panes live inside).
    pub workspace: Rect,
    /// Focused pane regions (compat aliases for single-pane callers).
    pub terminal: Rect,
    pub cells: Rect,
    pub warp: Rect,
    pub divider: Rect,
    pub path: Rect,
    /// One entry per visible leaf pane.
    pub panes: Vec<PaneLayout>,
    /// Logo glass button (top-right) — opens settings.
    pub logo: Rect,
    /// Caffeine (☕) glass chip — left of the logo.
    pub caffeine: Rect,
    /// Dynamic tab chips (left → right) in the title bar.
    pub tab_chips: Vec<Rect>,
    /// Close (×) hit target inside each tab chip — same length as [`tab_chips`].
    pub tab_closes: Vec<Rect>,
    pub tab_active: Rect,
    pub tab_idle: Rect,
    pub tab_new: Rect,
    /// Same as [`logo`] — settings is the logo button.
    pub settings: Rect,
    /// Split sashes for the active tab (filled by the app after tree layout).
    pub sashes: Vec<crate::panes::SashHit>,
}

impl FrameLayout {
    /// Collapse chip `index` and pull later chips over. `t` is 1 (full) → 0 (gone).
    pub fn apply_tab_exit(&mut self, index: usize, t: f32) {
        if index >= self.tab_chips.len() {
            return;
        }
        let t = t.clamp(0.0, 1.0);
        let ease = 1.0 - t; // 0 start, 1 end
        let cluster = if self.tab_chips.len() >= 2 {
            (self.tab_chips[1].x - (self.tab_chips[0].x + self.tab_chips[0].w)).max(0.0)
        } else {
            8.0
        };
        let lift = 12.0 * ease;
        let start_x = self.tab_chips[0].x;
        let chip_y = self.tab_chips[0].y;
        let chip_h = self.tab_chips[0].h;
        let mut x = start_x;
        for i in 0..self.tab_chips.len() {
            let full = TAB_CHIP_W;
            let w = if i == index {
                (full * t).max(0.0)
            } else {
                full
            };
            let y = if i == index { chip_y - lift } else { chip_y };
            let h = if i == index {
                (chip_h * (0.78 + 0.22 * t)).max(1.0)
            } else {
                chip_h
            };
            if w > 0.5 {
                self.tab_chips[i] = Rect::new(x, y, w, h);
                if let Some(close) = self.tab_closes.get_mut(i) {
                    let cx = x + w - TAB_CLOSE_TRAIL - TAB_CLOSE_SZ;
                    let cy = y + (h - TAB_CLOSE_SZ) * 0.5;
                    *close = Rect::new(cx, cy, TAB_CLOSE_SZ, TAB_CLOSE_SZ);
                }
                let gap = if i == index { cluster * t } else { cluster };
                x += w + gap;
            } else {
                self.tab_chips[i] = Rect::new(x, y, 0.0, h);
                if let Some(close) = self.tab_closes.get_mut(i) {
                    *close = Rect::new(x, y, 0.0, 0.0);
                }
            }
        }
        let new_size = self.tab_new.w;
        let new_y = chip_y + (chip_h - new_size) * 0.5;
        self.tab_new = Rect::new(x, new_y, new_size, new_size);
    }
}

/// Height of the pane header strip (title + close).
pub const PANE_HEADER_H: f32 = 22.0;
/// Title text band height inside the header.
pub const PANE_PILL_H: f32 = 16.0;
/// Ghost close button size (matches the + chip feel).
pub const PANE_CLOSE_SZ: f32 = 16.0;

/// Tab chip width (room for title + gap + close ×).
pub const TAB_CHIP_W: f32 = 112.0;
/// Close × size inside a tab chip.
pub const TAB_CLOSE_SZ: f32 = 14.0;
/// Inset from the chip’s right edge to the close ×.
pub const TAB_CLOSE_TRAIL: f32 = 10.0;
/// Gap between title text area and close ×.
pub const TAB_CLOSE_GAP: f32 = 6.0;

impl FrameLayout {
    /// Compute chrome chrome for `tab_count` strip tabs (single full-workspace pane).
    pub fn compute(width: f32, height: f32, m: Metrics, tab_count: usize) -> Self {
        Self::compute_with_panes(width, height, m, tab_count, &[(1, true)])
    }

    /// `pane_specs`: (pane_id, focused) in layout order — used when tree layout
    /// is applied externally via [`Self::apply_pane_rects`].
    pub fn compute_with_panes(
        width: f32,
        height: f32,
        m: Metrics,
        tab_count: usize,
        pane_specs: &[(u64, bool)],
    ) -> Self {
        let edge = m.edge();
        let _stack = m.stack(); // pane↔pane gaps applied in apply_pane_rects / split layout
        let chrome_h = m.title_h; // single bar (tabs + logo live here)

        let title = Rect::new(0.0, 0.0, width, chrome_h);
        // Compat: no second strip — tabs share the title bar.
        let tabs = Rect::new(0.0, 0.0, width, chrome_h);

        // Nav chips first — still centered in the chrome bar (traffic lights too).
        // Pane rises to the chip bottoms so nav↔pane air is closed without
        // sliding chips down.
        let chip_h = m.spacing.unit * 4.0; // 32
        let cluster = m.cluster();
        let chip_w = TAB_CHIP_W; // title + air + close ×
        let logo_w = chip_h; // square glass
        let chip_y = ((chrome_h - chip_h) * 0.5).max(0.0);
        let chip_bottom = chip_y + chip_h;

        let term_x = edge;
        // Flush under chip bottoms (was flush under full chrome_h → ~4px air).
        let term_y = chip_bottom;
        let term_w = (width - edge * 2.0).max(80.0);
        let term_h = (height - term_y - edge).max(80.0);
        let workspace = Rect::new(term_x, term_y, term_w, term_h);

        // Default: one pane fills the workspace.
        let specs = if pane_specs.is_empty() {
            vec![(1u64, true)]
        } else {
            pane_specs.to_vec()
        };
        let mut panes = Vec::with_capacity(specs.len());
        if specs.len() == 1 {
            panes.push(pane_layout_in_glass(
                specs[0].0,
                workspace,
                m,
                specs[0].1,
                false,
            ));
        } else {
            // Equal columns fallback if caller didn't apply tree layout.
            let gap = m.stack();
            let n = specs.len() as f32;
            let usable = (term_w - gap * (n - 1.0)).max(40.0);
            let pw = usable / n;
            for (i, (id, foc)) in specs.iter().enumerate() {
                let x = term_x + i as f32 * (pw + gap);
                let glass = Rect::new(x, term_y, pw, term_h);
                panes.push(pane_layout_in_glass(*id, glass, m, *foc, false));
            }
        }

        let focused = panes
            .iter()
            .find(|p| p.focused)
            .cloned()
            .unwrap_or_else(|| panes[0].clone());

        // Tabs start just after mac traffic lights (or left edge).
        let tabs_left = if cfg!(target_os = "macos") {
            // lights: 16 + 3*12 + 2*8 ≈ 60; keep extra air before first tab
            // (was 72 — felt tight against the traffic lights).
            80.0 // a bit more than 72, less than 88
        } else {
            edge
        };

        // Right cluster: [ ☕ caffeine ] [ 硯 logo/settings ]
        let logo = Rect::new(width - edge - logo_w, chip_y, logo_w, chip_h);
        let settings = logo; // same hit target
        let caffeine = Rect::new(logo.x - cluster - logo_w, chip_y, logo_w, chip_h);
        let tabs_right = caffeine.x - cluster;

        let mut x = tabs_left;
        let mut tab_chips = Vec::with_capacity(tab_count);
        let mut tab_closes = Vec::with_capacity(tab_count);
        for _ in 0..tab_count {
            if x + chip_w > tabs_right {
                break; // overflow — stop adding chips
            }
            let chip = Rect::new(x, chip_y, chip_w, chip_h);
            tab_chips.push(chip);
            // Close ×: right side of chip with trail padding (not jammed in the corner).
            let cx = chip.x + chip.w - TAB_CLOSE_TRAIL - TAB_CLOSE_SZ;
            let cy = chip.y + (chip.h - TAB_CLOSE_SZ) * 0.5;
            tab_closes.push(Rect::new(cx, cy, TAB_CLOSE_SZ, TAB_CLOSE_SZ));
            x += chip_w + cluster;
        }
        // Ghost + control: smaller hit/glass shell; icon stays full size in paint.
        let new_size = m.spacing.unit * 3.0; // 24 — was full chip 32
        let new_y = chip_y + (chip_h - new_size) * 0.5;
        let tab_new = if x + new_size <= tabs_right {
            Rect::new(x, new_y, new_size, new_size)
        } else {
            Rect::new(tabs_right - new_size, new_y, new_size, new_size)
        };

        let tab_active = tab_chips.first().copied().unwrap_or_default();
        let tab_idle = tab_chips.get(1).copied().unwrap_or_default();

        Self {
            title,
            tabs,
            workspace,
            terminal: focused.glass,
            cells: focused.cells,
            warp: focused.warp,
            divider: focused.divider,
            path: focused.path,
            panes,
            logo,
            caffeine,
            tab_chips,
            tab_closes,
            tab_active,
            tab_idle,
            tab_new,
            settings,
            sashes: Vec::new(),
        }
    }

    /// Title label area inside a tab chip (excludes close × + gaps).
    pub fn tab_title_rect(chip: Rect) -> Rect {
        let right = TAB_CLOSE_TRAIL + TAB_CLOSE_SZ + TAB_CLOSE_GAP;
        Rect::new(
            chip.x + 10.0,
            chip.y,
            (chip.w - 10.0 - right).max(8.0),
            chip.h,
        )
    }

    /// Replace pane glass rects from a split-tree layout pass.
    ///
    /// `fullscreen` is true for panes on the VT alt screen (hide path/warp strip).
    pub fn apply_pane_rects(
        &mut self,
        m: Metrics,
        leaf_rects: &[(u64, Rect)],
        focus: u64,
        fullscreen: &dyn Fn(u64) -> bool,
    ) {
        self.panes.clear();
        for (id, glass) in leaf_rects {
            self.panes.push(pane_layout_in_glass(
                *id,
                *glass,
                m,
                *id == focus,
                fullscreen(*id),
            ));
        }
        if let Some(f) = self.panes.iter().find(|p| p.focused).cloned() {
            self.terminal = f.glass;
            self.cells = f.cells;
            self.warp = f.warp;
            self.divider = f.divider;
            self.path = f.path;
        } else if let Some(f) = self.panes.first().cloned() {
            self.terminal = f.glass;
            self.cells = f.cells;
            self.warp = f.warp;
            self.divider = f.divider;
            self.path = f.path;
        }
    }

    /// Glass / solid panel instances for the composite pass.
    ///
    /// `chip_ui` drives hover scale + press wash. `tab_jelly` draws the active
    /// tab as a continuous piece that melts into the workspace.
    pub fn glass_panels(
        &self,
        m: Metrics,
        active_tab_index: usize,
        traffic_lights: Option<[Rect; 3]>,
        chip_ui: &crate::chrome_ui::ChipUi,
        tab_jelly: &crate::chrome_ui::TabJelly,
    ) -> Vec<PanelInstance> {
        use crate::chrome_ui::{scale_rect, ChipId};

        let mut out = Vec::with_capacity(10 + self.tab_chips.len() + self.panes.len());

        if let Some(lights) = traffic_lights {
            let r = lights[0].w * 0.5;
            out.push(PanelInstance::glass(lights[0], r, PanelKind::SolidClose));
            out.push(PanelInstance::glass(lights[1], r, PanelKind::SolidMin));
            out.push(PanelInstance::glass(lights[2], r, PanelKind::SolidZoom));
        }

        // Workspace panes — smooth-unioned in the shader with the active tab
        // (surface tension / stretchy glass, not separate cubes).
        for pl in &self.panes {
            out.push(PanelInstance::glass(pl.glass, m.radius, PanelKind::Terminal));
            {
                let id = ChipId::PaneClose(pl.pane_id);
                if pl.close.w > 2.0 && chip_ui.ghost_shell_visible(id) {
                    let r = scale_rect(pl.close, chip_ui.scale_for(id));
                    let rr = (m.chip_radius * 0.75).max(4.0);
                    out.push(
                        PanelInstance::glass(r, rr, PanelKind::NewTab)
                            .with_press(chip_ui.press_light(id)),
                    );
                }
            }
            // Header rule — same hairline as the warp strip (no glass on the name).
            if pl.header.h > 2.0 && pl.header.w > 4.0 {
                let line = Rect::new(
                    pl.header.x,
                    pl.header.y + pl.header.h - 1.0,
                    pl.header.w,
                    1.5,
                );
                out.push(
                    PanelInstance::glass(line, 0.5, PanelKind::Hairline).with_opacity(0.9),
                );
            }
            // Footer hairline only when the command strip is present (not alt-screen).
            if pl.divider.h >= 1.0 {
                let mid_y = pl.divider.y + pl.divider.h * 0.5 - 0.75;
                let line = Rect::new(pl.divider.x, mid_y, pl.divider.w, 1.5);
                out.push(
                    PanelInstance::glass(line, 0.5, PanelKind::Hairline).with_opacity(0.9),
                );
            }
            if pl.focused && pl.glass.w > 4.0 && pl.glass.h > 4.0 {
                let rim = Rect::new(
                    pl.glass.x + 1.0,
                    pl.glass.y + 1.0,
                    (pl.glass.w - 2.0).max(2.0),
                    (pl.glass.h - 2.0).max(2.0),
                );
                out.push(
                    PanelInstance::glass(rim, (m.radius - 1.0).max(2.0), PanelKind::PaneFocus)
                        .with_opacity(0.32),
                );
            }
        }

        // Active goo — slides under tabs via jelly (unscaled so switch = pure slide).
        // Smooth-unions with Terminal panes into one surface.
        if !self.tab_chips.is_empty() && active_tab_index < self.tab_chips.len() {
            let chip_h = self.tab_chips[0].h;
            let chip_y = self.tab_chips[0].y;
            let mut active_r = tab_jelly.active_chip_rect(chip_y, chip_h);
            // Grow down into the well so smin can join (geometry, not scale).
            let target_bottom = self.workspace.y + m.chip_radius * 2.0; // a bit deeper for soft light
            let bottom = active_r.y + active_r.h;
            if target_bottom > bottom {
                active_r.h = target_bottom - active_r.y;
            }
            let id = ChipId::Tab(active_tab_index);
            out.push(
                PanelInstance::glass(active_r, m.chip_radius, PanelKind::ChipActive)
                    .with_press(chip_ui.press_light(id)),
            );
        }

        // Tab chips stay at layout positions. Suppress idle glass only when the
        // jelly is already covering that chip (so mid-slide doesn't leave a hole
        // on the destination or a double-stack on the source).
        for (i, chip) in self.tab_chips.iter().enumerate() {
            let cx = chip.x + chip.w * 0.5;
            let covered = (tab_jelly.x - cx).abs() < chip.w * 0.45;
            if covered {
                continue;
            }
            let id = ChipId::Tab(i);
            let r = scale_rect(*chip, chip_ui.scale_for(id));
            out.push(
                PanelInstance::glass(r, m.chip_radius, PanelKind::ChipIdle)
                    .with_press(chip_ui.press_light(id)),
            );
        }

        // Ghost + : no idle glass — shell fades in with animated press wash.
        {
            let id = ChipId::NewTab;
            if chip_ui.ghost_shell_visible(id) {
                let r = scale_rect(self.tab_new, chip_ui.scale_for(id));
                let rr = (m.chip_radius * 0.75).max(4.0);
                out.push(
                    PanelInstance::glass(r, rr, PanelKind::NewTab)
                        .with_press(chip_ui.press_light(id)),
                );
            }
        }
        {
            let id = ChipId::Caffeine;
            let r = scale_rect(self.caffeine, chip_ui.scale_for(id));
            out.push(
                PanelInstance::glass(r, m.chip_radius, PanelKind::Caffeine)
                    .with_press(chip_ui.press_light(id)),
            );
        }
        {
            let id = ChipId::Logo;
            let r = scale_rect(self.logo, chip_ui.scale_for(id));
            out.push(
                PanelInstance::glass(r, m.chip_radius, PanelKind::Settings)
                    .with_press(chip_ui.press_light(id)),
            );
        }
        out
    }
}

/// Layout cells + optional command strip inside a pane glass.
///
/// When `fullscreen` is true (VT alt screen), path/warp/divider collapse and the
/// cell grid fills the glass — full-screen TUIs (vim, grok, etc.).
fn pane_header_rects(glass: Rect, inset: f32) -> (Rect, Rect, Rect) {
    let inner_x = glass.x + inset;
    let inner_w = (glass.w - inset * 2.0).max(24.0);
    let header = Rect::new(inner_x, glass.y + 3.0, inner_w, PANE_HEADER_H);
    let close = Rect::new(
        header.x + header.w - PANE_CLOSE_SZ,
        header.y + (header.h - PANE_CLOSE_SZ) * 0.5,
        PANE_CLOSE_SZ,
        PANE_CLOSE_SZ,
    );
    let title_w = (header.w - PANE_CLOSE_SZ - 8.0).max(20.0);
    let title_pill = Rect::new(
        header.x + 2.0,
        header.y + (header.h - PANE_PILL_H) * 0.5,
        title_w,
        PANE_PILL_H,
    );
    (header, title_pill, close)
}

fn pane_layout_in_glass(
    pane_id: u64,
    glass: Rect,
    m: Metrics,
    focused: bool,
    fullscreen: bool,
) -> PaneLayout {
    let inset = m.inset();
    let (header, title_pill, close) = pane_header_rects(glass, inset);
    let inner_x = glass.x + inset;
    let inner_w = (glass.w - inset * 2.0).max(40.0);
    let inner_top = (header.y + header.h + 2.0).max(glass.y + inset);
    let inner_bottom = glass.y + glass.h - inset;
    let avail = (inner_bottom - inner_top).max(0.0);

    if fullscreen {
        let cells_h = avail.max(CELL_H_MIN);
        let cells = Rect::new(inner_x, inner_top, inner_w, cells_h);
        // Zero-size strip so paint/hit-test skip path & warp.
        let empty = Rect::new(inner_x, cells.y + cells.h, inner_w, 0.0);
        return PaneLayout {
            pane_id,
            glass,
            cells,
            divider: empty,
            path: empty,
            warp: empty,
            header,
            title_pill,
            close,
            focused,
        };
    }

    // Three fixed mono rows (divider / path / input) — each = one cell height so
    // 14px Gohu never overflows its band or the glass bottom.
    let row: f32 = 14.0;
    let strip_want: f32 = row * 3.0;
    let strip: f32 = strip_want.min(avail);
    let row_h: f32 = if strip >= strip_want {
        row
    } else {
        (strip / 3.0).max(1.0)
    };
    let strip: f32 = row_h * 3.0;

    let cells_h = (avail - strip).max(CELL_H_MIN);
    let cells = Rect::new(inner_x, inner_top, inner_w, cells_h);

    let divider = Rect::new(inner_x, cells.y + cells.h, inner_w, row_h);
    let path = Rect::new(inner_x, divider.y + divider.h, inner_w, row_h);
    let warp = Rect::new(inner_x, path.y + path.h, inner_w, row_h);

    // Footer must end at or above the glass inner bottom (no overflow).
    debug_assert!(warp.y + warp.h <= inner_bottom + 0.5);

    PaneLayout {
        pane_id,
        glass,
        cells,
        divider,
        path,
        warp,
        header,
        title_pill,
        close,
        focused,
    }
}

/// Minimum cell-area height so grid math never collapses.
const CELL_H_MIN: f32 = 40.0;

/// Panel kinds drawn with the glass pass.
#[derive(Clone, Copy, Debug)]
#[repr(u32)]
pub enum PanelKind {
    Terminal = 0,
    /// Reserved (input is no longer a glass pane; kept for shader kind table).
    #[allow(dead_code)]
    Warp = 1,
    ChipActive = 2,
    ChipIdle = 3,
    Settings = 4,
    NewTab = 5,
    /// Caffeine cup chip (☕).
    Caffeine = 12,
    /// Solid macOS close (#ff5f57).
    SolidClose = 6,
    /// Solid macOS minimize (#febc2e).
    SolidMin = 7,
    /// Solid macOS zoom (#28c840).
    SolidZoom = 8,
    /// Full-window dim scrim behind modal (`_pad[0]` = alpha).
    Scrim = 9,
    /// Settings glass modal (same optics as panes; `_pad[0]` = opacity).
    Modal = 10,
    /// Active-tab jelly neck bridging chrome → workspace.
    TabConnect = 11,
    /// Nested modal field — heavier frost (search / notes body).
    ModalFrost = 13,
    /// Modal option / action glass button.
    ModalButton = 14,
    /// Selected modal option button.
    ModalButtonActive = 15,
    /// Thin solid rule (pane footer, list separators).
    Hairline = 16,
    /// Dim primary glow / smoke on the focused pane.
    PaneFocus = 17,
}

/// GPU-ready panel instance.
#[derive(Clone, Copy, Debug)]
#[repr(C)]
pub struct PanelInstance {
    /// x, y, w, h in logical px
    pub rect: [f32; 4],
    /// corner radius (or scrim unused)
    pub radius: f32,
    pub kind: f32,
    /// `_pad[0]` = opacity (Scrim/Modal) or 1; `_pad[1]` = press light 0..1 for chips.
    pub _pad: [f32; 2],
    /// Optional glass face tint: rgb + strength 0..1 (workspace bubbles, etc.).
    pub tint: [f32; 4],
}

impl PanelInstance {
    pub fn glass(rect: Rect, radius: f32, kind: PanelKind) -> Self {
        Self {
            rect: [rect.x, rect.y, rect.w, rect.h],
            radius,
            kind: kind as u32 as f32,
            _pad: [1.0, 0.0],
            tint: [0.0, 0.0, 0.0, 0.0],
        }
    }

    pub fn with_opacity(mut self, opacity: f32) -> Self {
        self._pad[0] = opacity.clamp(0.0, 1.0);
        self
    }

    pub fn with_press(mut self, press: f32) -> Self {
        self._pad[1] = press.clamp(0.0, 1.0);
        self
    }

    /// Soft color wash on glass (`strength` 0..1; 0 = no tint).
    pub fn with_tint(mut self, rgb: [f32; 3], strength: f32) -> Self {
        self.tint = [
            rgb[0].clamp(0.0, 1.0),
            rgb[1].clamp(0.0, 1.0),
            rgb[2].clamp(0.0, 1.0),
            strength.clamp(0.0, 1.0),
        ];
        self
    }
}

unsafe impl bytemuck::Pod for PanelInstance {}
unsafe impl bytemuck::Zeroable for PanelInstance {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn spacing_tokens_on_8pt() {
        let s = Spacing::default();
        assert_eq!(s.unit, 8.0);
        for v in [s.edge, s.stack, s.inset, s.cluster] {
            assert_eq!(v % 8.0, 0.0, "{v} not on 8pt");
        }
    }

    #[test]
    fn edge_16_everything_else_8() {
        let s = Spacing::default();
        assert_eq!(s.edge, 16.0, "window edges");
        assert_eq!(s.stack, 8.0, "pane↔pane / cluster spacing");
        assert_eq!(s.inset, 8.0, "inside glass");
        assert_eq!(s.cluster, 8.0, "chip cluster");
    }

    #[test]
    fn edge_vs_stack_gutters() {
        let m = Metrics::default();
        let edge = m.edge();
        let l = FrameLayout::compute(800.0, 600.0, m, 2);

        // Tabs stay centered in the chrome bar — not slid down toward the pane.
        let chip_h = l.tab_chips[0].h;
        let chip_y = l.tab_chips[0].y;
        let expected_y = (m.title_h - chip_h) * 0.5;
        assert!((chip_y - expected_y).abs() < 0.01, "chips centered {chip_y}");

        // Pane is flush under chip bottoms (zero air between nav and well).
        let chip_bottom = chip_y + chip_h;
        assert!(
            (l.workspace.y - chip_bottom).abs() < 0.01,
            "chip→workspace {}",
            l.workspace.y - chip_bottom
        );

        assert!(l.cells.y >= l.terminal.y);
        assert!(l.warp.y + l.warp.h <= l.terminal.y + l.terminal.h + 0.01);
        assert!(l.path.y >= l.divider.y + l.divider.h - 0.5);
        assert!(l.warp.y >= l.path.y + l.path.h - 0.5);
        assert!((l.cells.y + l.cells.h - l.divider.y).abs() < 0.5);

        assert!((l.workspace.x - edge).abs() < 0.01);
        assert!((800.0 - l.workspace.x - l.workspace.w - edge).abs() < 0.01);
        assert!((600.0 - (l.workspace.y + l.workspace.h) - edge).abs() < 0.01);
    }

    #[test]
    fn single_glass_no_second_pane_gap() {
        let m = Metrics::default();
        let l = FrameLayout::compute(1120.0, 740.0, m, 1);
        assert!(l.warp.y > l.terminal.y);
        assert!(l.warp.y + l.warp.h <= l.terminal.y + l.terminal.h + 0.01);
        assert!(l.cells.h > 100.0);
        assert_eq!(l.panes.len(), 1);
    }

    #[test]
    fn chip_cluster_uses_cluster_token() {
        let m = Metrics::default();
        let l = FrameLayout::compute(800.0, 600.0, m, 2);
        let gap = l.tab_chips[1].x - (l.tab_chips[0].x + l.tab_chips[0].w);
        assert!((gap - m.cluster()).abs() < 0.01);
    }

    #[test]
    fn rect_intersects() {
        let a = Rect::new(0.0, 0.0, 10.0, 10.0);
        let b = Rect::new(5.0, 5.0, 10.0, 10.0);
        let c = Rect::new(20.0, 20.0, 4.0, 4.0);
        assert!(a.intersects(b));
        assert!(b.intersects(a));
        assert!(!a.intersects(c));
        assert!(a.contains(0.0, 0.0));
        assert!(!a.contains(10.0, 10.0));
    }

    #[test]
    fn focused_pane_emits_dim_primary_rim() {
        let m = Metrics::default();
        let l = FrameLayout::compute(800.0, 600.0, m, 1);
        let chip = crate::chrome_ui::ChipUi::default();
        let jelly = crate::chrome_ui::TabJelly::default();
        let panels = l.glass_panels(m, 0, None, &chip, &jelly);
        let rims = panels
            .iter()
            .filter(|p| (p.kind - PanelKind::PaneFocus as u32 as f32).abs() < 0.1)
            .count();
        assert_eq!(rims, 1);
        let rim = panels
            .iter()
            .find(|p| (p.kind - PanelKind::PaneFocus as u32 as f32).abs() < 0.1)
            .unwrap();
        assert!(rim._pad[0] < 0.40, "rim should stay dim");
    }

    #[test]
    fn pane_header_has_title_pill_and_close() {
        let m = Metrics::default();
        let l = FrameLayout::compute(800.0, 600.0, m, 1);
        let pl = &l.panes[0];
        assert!(pl.header.h > 10.0);
        assert!(pl.title_pill.x < pl.close.x);
        assert!(pl.close.x + pl.close.w <= pl.header.x + pl.header.w + 0.5);
        assert!(pl.cells.y >= pl.header.y + pl.header.h);
    }

    #[test]
    fn pane_title_is_hairline_not_glass_pill() {
        let m = Metrics::default();
        let l = FrameLayout::compute(800.0, 600.0, m, 1);
        let chip = crate::chrome_ui::ChipUi::default();
        let jelly = crate::chrome_ui::TabJelly::default();
        let panels = l.glass_panels(m, 0, None, &chip, &jelly);
        let pl = &l.panes[0];
        let title_glass = panels.iter().any(|p| {
            (p.kind - PanelKind::ChipIdle as u32 as f32).abs() < 0.1
                && (p.rect[0] - pl.title_pill.x).abs() < 1.0
                && (p.rect[1] - pl.title_pill.y).abs() < 1.0
        });
        assert!(!title_glass, "pane name should not sit in a glass chip");
        let hair = panels
            .iter()
            .filter(|p| (p.kind - PanelKind::Hairline as u32 as f32).abs() < 0.1)
            .count();
        assert!(
            hair >= 2,
            "header + footer hairlines expected, got {hair}"
        );
    }

    #[test]
    fn tab_exit_collapses_and_pulls_neighbors() {
        let m = Metrics::default();
        let mut l = FrameLayout::compute(1120.0, 740.0, m, 3);
        let x1 = l.tab_chips[1].x;
        l.apply_tab_exit(0, 0.0);
        assert!(l.tab_chips[0].w < 1.0);
        assert!(
            (l.tab_chips[1].x - l.tab_chips[0].x).abs() < 1.0,
            "next chip should pull into the closed slot"
        );
        assert!(l.tab_chips[2].x < x1 + TAB_CHIP_W);
    }
}
