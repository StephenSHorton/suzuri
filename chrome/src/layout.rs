//! Fixed layout constants — keep chrome + terminal hole in sync.
//! Same contract as the web surface spike; pure Rust, no flex runtime required yet.

/// Window chrome metrics (CSS-px-equivalent logical pixels).
#[derive(Clone, Copy, Debug)]
pub struct Metrics {
    pub title_h: f32,
    pub tab_h: f32,
    pub pad: f32,
    pub gap: f32,
    pub warp_h: f32,
    pub radius: f32,
}

impl Default for Metrics {
    fn default() -> Self {
        Self {
            title_h: 44.0,
            tab_h: 36.0,
            pad: 12.0,
            gap: 10.0,
            warp_h: 92.0,
            radius: 16.0,
        }
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
    pub fn compute(width: f32, height: f32, m: Metrics, tab_count: usize) -> Self {
        let title = Rect::new(0.0, 0.0, width, m.title_h);
        let tabs = Rect::new(0.0, m.title_h, width, m.tab_h);

        let term_x = m.pad;
        let term_y = m.title_h + m.tab_h + m.pad + m.gap;
        let term_w = (width - m.pad * 2.0).max(80.0);
        let term_h =
            (height - m.title_h - m.tab_h - m.warp_h - m.pad * 3.0 - m.gap * 2.0).max(80.0);
        let terminal = Rect::new(term_x, term_y, term_w, term_h);

        let warp_y = term_y + term_h + m.gap;
        let warp = Rect::new(m.pad, warp_y, term_w, m.warp_h);

        // Tab strip: logo · chips · new  …………  settings
        let chip_h = 30.0;
        let chip_y = m.title_h + (m.tab_h - chip_h) * 0.5;
        let logo_w = 28.0;
        let logo = Rect::new(m.pad, chip_y, logo_w, chip_h);

        let chip_w = 88.0;
        let chip_gap = 8.0;
        let mut x = m.pad + logo_w + 8.0;
        let mut tab_chips = Vec::with_capacity(tab_count);
        for _ in 0..tab_count {
            tab_chips.push(Rect::new(x, chip_y, chip_w, chip_h));
            x += chip_w + chip_gap;
        }
        let tab_new = Rect::new(x, chip_y + 1.0, 28.0, 28.0);

        let settings_w = 88.0;
        let settings = Rect::new(width - m.pad - settings_w, chip_y, settings_w, chip_h);

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
        let pack = |r: Rect, radius: f32, kind: PanelKind| PanelInstance {
            rect: [r.x, r.y, r.w, r.h],
            radius,
            kind: kind as u32 as f32,
            _pad: [0.0; 2],
        };

        let mut out = Vec::with_capacity(8 + self.tab_chips.len());

        if let Some(lights) = traffic_lights {
            // radius = half side → circle for 12×12 dots
            let r = lights[0].w * 0.5;
            out.push(pack(lights[0], r, PanelKind::SolidClose));
            out.push(pack(lights[1], r, PanelKind::SolidMin));
            out.push(pack(lights[2], r, PanelKind::SolidZoom));
        }

        out.push(pack(self.terminal, m.radius, PanelKind::Terminal));
        out.push(pack(self.warp, m.radius, PanelKind::Warp));

        for (i, chip) in self.tab_chips.iter().enumerate() {
            let kind = if i == active_tab_index {
                PanelKind::ChipActive
            } else {
                PanelKind::ChipIdle
            };
            out.push(pack(*chip, m.radius - 4.0, kind));
        }

        out.push(pack(self.tab_new, m.radius - 4.0, PanelKind::NewTab));
        out.push(pack(self.settings, m.radius - 4.0, PanelKind::Settings));
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
}

/// GPU-ready panel instance.
#[derive(Clone, Copy, Debug)]
#[repr(C)]
pub struct PanelInstance {
    /// x, y, w, h in logical px
    pub rect: [f32; 4],
    /// corner radius
    pub radius: f32,
    pub kind: f32,
    pub _pad: [f32; 2],
}

unsafe impl bytemuck::Pod for PanelInstance {}
unsafe impl bytemuck::Zeroable for PanelInstance {}
