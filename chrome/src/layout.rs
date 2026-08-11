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
//! | `stack` | 8 (1×) | nav ↔ terminal, terminal ↔ warp (internal chrome gaps) |
//! | `inset` | 8 (1×) | padding **inside** glass (terminal text, warp field) |
//! | `cluster` | 8 (1×) | gap between sibling nav chips |
//!
//! Edge is the only larger token; everything between regions is `stack`/`inset`.

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
            title_h: s.unit * 4.0,      // 32 — slim drag bar (lights + title only)
            tab_h: s.unit * 5.0,        // 40
            // divider + path + input (~3 mono rows), on the 8pt grid
            input_strip_h: s.unit * 6.0, // 48
            radius: s.unit * 2.0,       // 16
            chip_radius: s.unit,        // 8
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
    /// Logo slot left of the tab strip (character 「硯」).
    pub logo: Rect,
    /// Dynamic tab chips (left → right).
    pub tab_chips: Vec<Rect>,
    pub tab_active: Rect,
    pub tab_idle: Rect,
    pub tab_new: Rect,
    pub settings: Rect,
}

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
        let stack = m.stack();

        let title = Rect::new(0.0, 0.0, width, m.title_h);
        let tabs = Rect::new(0.0, m.title_h, width, m.tab_h);

        let term_x = edge;
        let term_y = m.title_h + m.tab_h + stack;
        let term_w = (width - edge * 2.0).max(80.0);
        let term_h = (height - m.title_h - m.tab_h - stack - edge).max(80.0);
        let workspace = Rect::new(term_x, term_y, term_w, term_h);

        // Default: one pane fills the workspace.
        let specs = if pane_specs.is_empty() {
            vec![(1u64, true)]
        } else {
            pane_specs.to_vec()
        };
        let mut panes = Vec::with_capacity(specs.len());
        if specs.len() == 1 {
            panes.push(pane_layout_in_glass(specs[0].0, workspace, m, specs[0].1));
        } else {
            // Equal columns fallback if caller didn't apply tree layout.
            let gap = m.stack();
            let n = specs.len() as f32;
            let usable = (term_w - gap * (n - 1.0)).max(40.0);
            let pw = usable / n;
            for (i, (id, foc)) in specs.iter().enumerate() {
                let x = term_x + i as f32 * (pw + gap);
                let glass = Rect::new(x, term_y, pw, term_h);
                panes.push(pane_layout_in_glass(*id, glass, m, *foc));
            }
        }

        let focused = panes
            .iter()
            .find(|p| p.focused)
            .cloned()
            .unwrap_or_else(|| panes[0].clone());

        // Tab strip
        let chip_h = m.spacing.unit * 4.0;
        let chip_y = m.title_h + (m.tab_h - chip_h) * 0.5;
        let logo_w = m.spacing.unit * 4.0;
        let logo = Rect::new(edge, chip_y, logo_w, chip_h);

        let chip_w = m.spacing.unit * 12.0;
        let cluster = m.cluster();
        let mut x = edge + logo_w + cluster;
        let mut tab_chips = Vec::with_capacity(tab_count);
        for _ in 0..tab_count {
            tab_chips.push(Rect::new(x, chip_y, chip_w, chip_h));
            x += chip_w + cluster;
        }
        let tab_new = Rect::new(x, chip_y, chip_h, chip_h);

        let settings_w = chip_w;
        let settings = Rect::new(width - edge - settings_w, chip_y, settings_w, chip_h);

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
            tab_chips,
            tab_active,
            tab_idle,
            tab_new,
            settings,
        }
    }

    /// Replace pane glass rects from a split-tree layout pass.
    pub fn apply_pane_rects(&mut self, m: Metrics, leaf_rects: &[(u64, Rect)], focus: u64) {
        self.panes.clear();
        for (id, glass) in leaf_rects {
            self.panes
                .push(pane_layout_in_glass(*id, *glass, m, *id == focus));
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
    pub fn glass_panels(
        &self,
        m: Metrics,
        active_tab_index: usize,
        traffic_lights: Option<[Rect; 3]>,
    ) -> Vec<PanelInstance> {
        let mut out = Vec::with_capacity(8 + self.tab_chips.len() + self.panes.len());

        if let Some(lights) = traffic_lights {
            let r = lights[0].w * 0.5;
            out.push(PanelInstance::glass(lights[0], r, PanelKind::SolidClose));
            out.push(PanelInstance::glass(lights[1], r, PanelKind::SolidMin));
            out.push(PanelInstance::glass(lights[2], r, PanelKind::SolidZoom));
        }

        for pl in &self.panes {
            out.push(PanelInstance::glass(pl.glass, m.radius, PanelKind::Terminal));
        }

        for (i, chip) in self.tab_chips.iter().enumerate() {
            let kind = if i == active_tab_index {
                PanelKind::ChipActive
            } else {
                PanelKind::ChipIdle
            };
            out.push(PanelInstance::glass(*chip, m.chip_radius, kind));
        }

        out.push(PanelInstance::glass(
            self.tab_new,
            m.chip_radius,
            PanelKind::NewTab,
        ));
        out.push(PanelInstance::glass(
            self.settings,
            m.chip_radius,
            PanelKind::Settings,
        ));
        out
    }
}

fn pane_layout_in_glass(pane_id: u64, glass: Rect, m: Metrics, focused: bool) -> PaneLayout {
    let inset = m.inset();
    let strip = m.input_strip_h.min((glass.h - inset * 2.0).max(0.0));
    let inner_x = glass.x + inset;
    let inner_w = (glass.w - inset * 2.0).max(40.0);
    let inner_top = glass.y + inset;
    let inner_bottom = glass.y + glass.h - inset;
    let cells_h = (inner_bottom - inner_top - strip).max(CELL_H_MIN);
    let cells = Rect::new(inner_x, inner_top, inner_w, cells_h);

    let div_h = (strip * 0.22).max(m.spacing.half());
    let path_h = (strip * 0.34).max(m.spacing.unit);
    let input_h = (strip - div_h - path_h).max(m.spacing.unit);
    let divider = Rect::new(inner_x, cells.y + cells.h, inner_w, div_h);
    let path = Rect::new(inner_x, divider.y + divider.h, inner_w, path_h);
    let warp = Rect::new(inner_x, path.y + path.h, inner_w, input_h);

    PaneLayout {
        pane_id,
        glass,
        cells,
        divider,
        path,
        warp,
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
    /// `_pad[0]` = opacity for Scrim / Modal; otherwise 0.
    pub _pad: [f32; 2],
}

impl PanelInstance {
    pub fn glass(rect: Rect, radius: f32, kind: PanelKind) -> Self {
        Self {
            rect: [rect.x, rect.y, rect.w, rect.h],
            radius,
            kind: kind as u32 as f32,
            _pad: [1.0, 0.0],
        }
    }

    pub fn with_opacity(mut self, opacity: f32) -> Self {
        self._pad[0] = opacity.clamp(0.0, 1.0);
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
        assert_eq!(s.stack, 8.0, "between regions");
        assert_eq!(s.inset, 8.0, "inside glass");
        assert_eq!(s.cluster, 8.0, "chip cluster");
    }

    #[test]
    fn edge_vs_stack_gutters() {
        let m = Metrics::default();
        let edge = m.edge();
        let stack = m.stack();
        let l = FrameLayout::compute(800.0, 600.0, m, 2);

        // nav → workspace == stack (8), not edge
        let under_nav = l.workspace.y - (m.title_h + m.tab_h);
        assert!((under_nav - stack).abs() < 0.01, "nav→workspace {under_nav}");

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
}
