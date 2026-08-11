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
    pub warp_h: f32,
    /// Corner radius for primary panes (terminal / warp).
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
            warp_h: s.unit * 8.0,       // 64
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

/// One chrome frame’s computed geometry.
#[derive(Clone, Debug)]
#[allow(dead_code)] // tabs / tab_active / tab_idle kept for layout contract + tests
pub struct FrameLayout {
    pub title: Rect,
    pub tabs: Rect,
    pub terminal: Rect,
    pub warp: Rect,
    /// Logo slot left of the tab strip (character 「硯」).
    pub logo: Rect,
    /// Dynamic tab chips (left → right).
    pub tab_chips: Vec<Rect>,
    /// Compat: first chip (or zero rect if none).
    pub tab_active: Rect,
    /// Compat: second chip (or zero rect if none).
    pub tab_idle: Rect,
    pub tab_new: Rect,
    pub settings: Rect,
}

impl FrameLayout {
    /// Compute chrome geometry for `tab_count` open tabs.
    ///
    /// ```text
    /// [ title ]
    /// [ tabs  ]
    /// [ stack 8 ]   ← internal
    /// [ terminal ]
    /// [ stack 8 ]
    /// [ warp  ]
    /// [ edge 16 ]   ← window bottom
    /// ← edge 16 → panes ← edge 16 →
    /// ```
    pub fn compute(width: f32, height: f32, m: Metrics, tab_count: usize) -> Self {
        let edge = m.edge();
        let stack = m.stack();

        let title = Rect::new(0.0, 0.0, width, m.title_h);
        let tabs = Rect::new(0.0, m.title_h, width, m.tab_h);

        let term_x = edge;
        let term_y = m.title_h + m.tab_h + stack;
        let term_w = (width - edge * 2.0).max(80.0);
        // 2× stack (under nav + between panes) + 1× edge (bottom)
        let term_h =
            (height - m.title_h - m.tab_h - m.warp_h - stack * 2.0 - edge).max(80.0);
        let terminal = Rect::new(term_x, term_y, term_w, term_h);

        let warp_y = term_y + term_h + stack;
        let warp = Rect::new(edge, warp_y, term_w, m.warp_h);

        // Tab strip: logo · chips · +  …………  settings
        let chip_h = m.spacing.unit * 4.0; // 32
        let chip_y = m.title_h + (m.tab_h - chip_h) * 0.5;
        let logo_w = m.spacing.unit * 4.0; // 32
        let logo = Rect::new(edge, chip_y, logo_w, chip_h);

        let chip_w = m.spacing.unit * 12.0; // 96
        let cluster = m.cluster();
        let mut x = edge + logo_w + cluster;
        let mut tab_chips = Vec::with_capacity(tab_count);
        for _ in 0..tab_count {
            tab_chips.push(Rect::new(x, chip_y, chip_w, chip_h));
            x += chip_w + cluster;
        }
        let tab_new = Rect::new(x, chip_y, chip_h, chip_h); // 32×32

        let settings_w = chip_w;
        let settings = Rect::new(width - edge - settings_w, chip_y, settings_w, chip_h);

        let tab_active = tab_chips.first().copied().unwrap_or_default();
        let tab_idle = tab_chips.get(1).copied().unwrap_or_default();

        Self {
            title,
            tabs,
            terminal,
            warp,
            logo,
            tab_chips,
            tab_active,
            tab_idle,
            tab_new,
            settings,
        }
    }

    /// Glass / solid panel instances for the composite pass.
    ///
    /// `active_tab_index` selects which chip gets the active (bright) style.
    /// `traffic_lights` are optional solid circle rects (close / min / zoom).
    pub fn glass_panels(
        &self,
        m: Metrics,
        active_tab_index: usize,
        traffic_lights: Option<[Rect; 3]>,
    ) -> Vec<PanelInstance> {
        let mut out = Vec::with_capacity(8 + self.tab_chips.len());

        if let Some(lights) = traffic_lights {
            let r = lights[0].w * 0.5;
            out.push(PanelInstance::glass(lights[0], r, PanelKind::SolidClose));
            out.push(PanelInstance::glass(lights[1], r, PanelKind::SolidMin));
            out.push(PanelInstance::glass(lights[2], r, PanelKind::SolidZoom));
        }

        out.push(PanelInstance::glass(
            self.terminal,
            m.radius,
            PanelKind::Terminal,
        ));
        out.push(PanelInstance::glass(self.warp, m.radius, PanelKind::Warp));

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

/// Panel kinds drawn with the glass pass.
#[derive(Clone, Copy, Debug)]
#[repr(u32)]
pub enum PanelKind {
    Terminal = 0,
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

        // nav → terminal == stack (8), not edge
        let under_nav = l.terminal.y - (m.title_h + m.tab_h);
        assert!((under_nav - stack).abs() < 0.01, "nav→terminal {under_nav}");

        // terminal → warp == stack
        let between = l.warp.y - (l.terminal.y + l.terminal.h);
        assert!((between - stack).abs() < 0.01, "terminal→warp {between}");
        assert!((between - under_nav).abs() < 0.01);

        // left / right / bottom == edge (16)
        assert!((l.terminal.x - edge).abs() < 0.01);
        assert!((800.0 - l.terminal.x - l.terminal.w - edge).abs() < 0.01);
        assert!((600.0 - (l.warp.y + l.warp.h) - edge).abs() < 0.01);
    }

    #[test]
    fn chip_cluster_uses_cluster_token() {
        let m = Metrics::default();
        let l = FrameLayout::compute(800.0, 600.0, m, 2);
        let gap = l.tab_chips[1].x - (l.tab_chips[0].x + l.tab_chips[0].w);
        assert!((gap - m.cluster()).abs() < 0.01);
    }
}
