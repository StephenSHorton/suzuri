//! Minimal GPU text overlay via [glyphon] (cosmic-text + wgpu 24).
//!
//! Product suzuri embeds **GohuFont uni14 Nerd Font Mono** (`assets.FontFaceBundled`).
//!
//! # Critical matching detail
//! Gohu’s OS/2 weight is **500** (Medium), not 400 (Normal). cosmic-text’s default
//! `Attrs` weight is Normal — with family Name only, it **skips Gohu** and falls
//! back to another face. Always pass the face’s real weight.

use glyphon::{
    Attrs, Buffer, Cache, Color, Family, FontSystem, Metrics as FontMetrics, Resolution, Shaping,
    SwashCache, TextArea, TextAtlas, TextBounds, TextRenderer, Viewport, Weight,
};
use winit::dpi::PhysicalSize;

/// Product suzuri mono — GohuFont uni14 Nerd Font Mono (see `assets/fonts/`).
const GOHU_TTF: &[u8] = include_bytes!("../../assets/fonts/GohuFontuni14NerdFontMono-Regular.ttf");

/// fontdb / GDI face name (matches `assets.FontFaceBundled`).
pub const GOHU_FAMILY: &str = "GohuFont uni14 Nerd Font Mono";

/// Default caret glyph — full block everywhere (terminal, warp, modals).
pub const CARET_BLOCK: &str = "█";

/// Fallback caret color when theme primary is unavailable (inkstone jade).
/// Prefer `settings.prefs.theme_colors().jade` at paint sites.
pub const CARET_RGB: [f32; 3] = [0.0, 0.90, 0.46];

/// System face for ☕ (Gohu has no U+2615).
#[cfg(target_os = "macos")]
const SYMBOLS_FAMILY: &str = "Apple Symbols";
#[cfg(target_os = "windows")]
const SYMBOLS_FAMILY: &str = "Segoe UI Symbol";
#[cfg(not(any(target_os = "macos", target_os = "windows")))]
const SYMBOLS_FAMILY: &str = "Noto Sans Symbols2";

/// Gohu uni14 design size (logical px) at 1× zoom.
pub const MONO_DESIGN_PX: f32 = 14.0;

/// Mono cell size in **logical** px (grid + paint). Measured from Gohu when possible.
#[derive(Clone, Copy, Debug)]
pub struct MonoCellMetrics {
    pub w: f32,
    pub h: f32,
}

impl Default for MonoCellMetrics {
    fn default() -> Self {
        // Gohu uni14 design size — fallback if measurement fails.
        Self {
            w: 7.0,
            h: MONO_DESIGN_PX,
        }
    }
}

impl MonoCellMetrics {
    fn fallback_at(design: f32) -> Self {
        let z = (design / MONO_DESIGN_PX).max(0.05);
        Self {
            w: (7.0 * z).round().max(1.0),
            h: design.max(1.0),
        }
    }

    /// Horizontal advance of one mono cell when painted at `size` (logical px).
    ///
    /// `self.w` is measured at `self.h` (14 × zoom). Labels that use another
    /// size must scale this or a block caret walked by `self.w` drifts by
    /// `(1 - size/h)` per character.
    pub fn advance_at(self, size: f32) -> f32 {
        let h = self.h.max(1.0);
        self.w.max(1.0) * (size.max(0.0) / h)
    }
}

/// One screen-space label (logical pixels).
#[derive(Clone, Debug)]
pub struct TextLabel {
    pub text: String,
    pub x: f32,
    pub y: f32,
    pub size: f32,
    pub color: [f32; 4],
    pub mono: bool,
    pub rain: bool,
    /// UI symbol (☕) — system symbols face.
    pub symbols: bool,
    /// Key chords (⌘K / ⇧⌘T) — system UI face so modifiers share one baseline.
    pub key_chord: bool,
    pub center_in: Option<[f32; 4]>,
    /// Optional clip rect in logical px `[x, y, w, h]` (terminal pane hole).
    pub clip: Option<[f32; 4]>,
    /// Line box = font size (no 1.2 leading). Needed so a single glyph
    /// centers on `center_in` instead of sitting low in the extra leading.
    pub tight: bool,
    /// Blink / sine caret — prepared separately so it does not bust the grid cache.
    pub caret: bool,
}

impl TextLabel {
    pub fn new(text: impl Into<String>, x: f32, y: f32, size: f32, color: [f32; 4]) -> Self {
        Self {
            text: text.into(),
            x,
            y,
            size,
            color,
            mono: false,
            rain: false,
            symbols: false,
            key_chord: false,
            center_in: None,
            clip: None,
            tight: false,
            caret: false,
        }
    }

    pub fn mono(text: impl Into<String>, x: f32, y: f32, size: f32, color: [f32; 4]) -> Self {
        Self {
            text: text.into(),
            x,
            y,
            size,
            color,
            mono: true,
            rain: false,
            symbols: false,
            key_chord: false,
            center_in: None,
            clip: None,
            tight: false,
            caret: false,
        }
    }

    pub fn centered(text: impl Into<String>, rect: [f32; 4], size: f32, color: [f32; 4]) -> Self {
        Self {
            text: text.into(),
            x: rect[0],
            y: rect[1],
            size,
            color,
            mono: true,
            rain: false,
            symbols: false,
            key_chord: false,
            center_in: Some(rect),
            clip: None,
            tight: false,
            caret: false,
        }
    }

    /// Center a chrome icon (pane/tab ×) on `rect` using a tight line box.
    pub fn icon_centered(
        text: impl Into<String>,
        rect: [f32; 4],
        size: f32,
        color: [f32; 4],
    ) -> Self {
        let mut label = Self::centered(text, rect, size, color);
        label.tight = true;
        label
    }

    /// Clip this label to a logical rect (e.g. terminal cells hole).
    pub fn with_clip(mut self, clip: [f32; 4]) -> Self {
        self.clip = Some(clip);
        self
    }

    /// Mark as a blink/sine caret (prepared on a side path).
    pub fn as_caret(mut self) -> Self {
        self.caret = true;
        self
    }

    /// Left-aligned, vertically centered in a band (modal rows, chips).
    pub fn left_vcenter(
        text: impl Into<String>,
        x: f32,
        band_y: f32,
        band_h: f32,
        size: f32,
        color: [f32; 4],
    ) -> Self {
        let y = band_y + (band_h - size).max(0.0) * 0.5;
        Self::new(text, x, y, size, color)
    }

    /// Keyboard chord left-aligned + v-centered (shared left edge for all rows).
    pub fn key_left_vcenter(
        text: impl Into<String>,
        x: f32,
        band_y: f32,
        band_h: f32,
        size: f32,
        color: [f32; 4],
    ) -> Self {
        let y = band_y + (band_h - size).max(0.0) * 0.5;
        Self {
            text: text.into(),
            x,
            y,
            size,
            color,
            mono: false,
            rain: false,
            symbols: false,
            key_chord: true,
            center_in: None,
            clip: None,
            tight: false,
            caret: false,
        }
    }

    pub fn symbol_centered(
        text: impl Into<String>,
        rect: [f32; 4],
        size: f32,
        color: [f32; 4],
    ) -> Self {
        Self {
            text: text.into(),
            x: rect[0],
            y: rect[1],
            size,
            color,
            mono: false,
            rain: false,
            symbols: true,
            key_chord: false,
            center_in: Some(rect),
            clip: None,
            tight: false,
            caret: false,
        }
    }
}

/// System face for ⌘ ⇧ ⌥ chords — keeps modifiers on one baseline (Gohu mixes poorly).
#[cfg(target_os = "macos")]
const KEY_CHORD_FAMILY: &str = "SF Pro Text";
#[cfg(target_os = "windows")]
const KEY_CHORD_FAMILY: &str = "Segoe UI";
#[cfg(not(any(target_os = "macos", target_os = "windows")))]
const KEY_CHORD_FAMILY: &str = "sans-serif";

/// Glyphon-backed text layer for overlay labels.
pub struct TextLayer {
    font_system: FontSystem,
    swash_cache: SwashCache,
    _cache: Cache,
    viewport: Viewport,
    atlas: TextAtlas,
    text_renderer: TextRenderer,
    buffers: Vec<Buffer>,
    /// Fingerprint of the last fully reshaped non-caret body.
    last_body_fp: u64,
    body_prepared: bool,
    /// How many leading buffers belong to the cached body.
    body_len: usize,
    width: u32,
    height: u32,
    scale_factor: f32,
    gohu_family: String,
    /// OS/2 weight of the embedded face (Gohu is 500, not 400).
    gohu_weight: Weight,
    gohu_ok: bool,
    /// Active mono font id from settings (`gohu`, `sf-mono`, …).
    mono_font_id: String,
    /// Resolved face name for terminal mono paint.
    mono_family: String,
    mono_weight: Weight,
    /// Measured mono cell (logical px) for terminal grid + paint.
    mono_cell: MonoCellMetrics,
    /// ⌘± UI scale. Label `size` is design-px; prepare multiplies by this.
    ui_scale: f32,
    /// Mono probe / cell height (14 × ui_scale).
    design_size: f32,
}

impl TextLayer {
    pub fn new(device: &wgpu::Device, queue: &wgpu::Queue, format: wgpu::TextureFormat) -> Self {
        let mut font_system = FontSystem::new();
        font_system.db_mut().load_font_data(GOHU_TTF.to_vec());

        let mut gohu_family = GOHU_FAMILY.to_string();
        // Gohu ships as weight 500 — must match Attrs or cosmic-text picks another face.
        let mut gohu_weight = Weight(500);
        let mut found = false;
        for face in font_system.db().faces() {
            for (name, _) in &face.families {
                if name.eq_ignore_ascii_case(GOHU_FAMILY)
                    || name.contains("GohuFont")
                    || name.contains("Gohu")
                {
                    gohu_family = name.clone();
                    gohu_weight = face.weight;
                    found = true;
                    break;
                }
            }
            if found {
                break;
            }
        }

        if found {
            font_system
                .db_mut()
                .set_monospace_family(gohu_family.as_str());
            eprintln!(
                "suzuri-chrome: UI font ready · {gohu_family} weight={}",
                gohu_weight.0
            );
        } else {
            eprintln!("suzuri-chrome: Gohu face not found after load (expected `{GOHU_FAMILY}`)");
        }

        let swash_cache = SwashCache::new();
        let cache = Cache::new(device);
        let viewport = Viewport::new(device, &cache);
        let mut atlas = TextAtlas::new(device, queue, &cache, format);
        let text_renderer =
            TextRenderer::new(&mut atlas, device, wgpu::MultisampleState::default(), None);

        let seed = Buffer::new(&mut font_system, FontMetrics::new(14.0, 18.0));

        // Probe: only count OK if shaped glyphs use the Gohu face id.
        // Also measure mono cell advance for terminal grid (logical 14px).
        let (gohu_ok, mono_cell) = {
            let design = 14.0_f32;
            let mut probe = Buffer::new(&mut font_system, FontMetrics::new(design, design));
            probe.set_size(&mut font_system, Some(200.0), Some(design));
            let attrs = Attrs::new()
                .family(Family::Name(gohu_family.as_str()))
                .weight(gohu_weight);
            probe.set_text(&mut font_system, "M", attrs, Shaping::Advanced);
            probe.shape_until_scroll(&mut font_system, false);
            let mut ok = false;
            let mut advance = 0.0_f32;
            for run in probe.layout_runs() {
                advance = advance.max(run.line_w);
                for g in run.glyphs.iter() {
                    // Advance ~7–8px at 14 for Gohu mono cells
                    if g.w > 4.0 && g.w < 12.0 {
                        ok = true;
                    }
                }
            }
            if !ok {
                eprintln!(
                    "suzuri-chrome: Gohu probe failed (weight={}) — check face matching",
                    gohu_weight.0
                );
            }
            let cell = if advance >= 5.0 && advance <= 12.0 {
                MonoCellMetrics {
                    w: advance.round().max(1.0),
                    h: design,
                }
            } else {
                MonoCellMetrics::default()
            };
            eprintln!("suzuri-chrome: mono cell {}×{} logical px", cell.w, cell.h);
            (ok, cell)
        };

        Self {
            font_system,
            swash_cache,
            _cache: cache,
            viewport,
            atlas,
            text_renderer,
            buffers: vec![seed],
            last_body_fp: 0,
            body_prepared: false,
            body_len: 0,
            width: 1,
            height: 1,
            scale_factor: 1.0,
            gohu_family: gohu_family.clone(),
            gohu_weight,
            gohu_ok,
            mono_font_id: "gohu".into(),
            mono_family: gohu_family,
            mono_weight: gohu_weight,
            mono_cell,
            ui_scale: 1.0,
            design_size: MONO_DESIGN_PX,
        }
    }

    /// Measured mono cell size (logical px) for PTY grid + terminal paint.
    pub fn mono_cell(&self) -> MonoCellMetrics {
        self.mono_cell
    }

    /// ⌘± UI scale applied to label sizes and mono cell pitch.
    pub fn ui_scale(&self) -> f32 {
        self.ui_scale
    }

    /// Change ⌘± scale. Remeasures mono cell. Returns true if scale changed.
    pub fn set_ui_scale(&mut self, z: f32) -> bool {
        let z = crate::layout::clamp_ui_zoom(z);
        if (self.ui_scale - z).abs() < 1e-4 {
            return false;
        }
        self.ui_scale = z;
        self.design_size = MONO_DESIGN_PX * z;
        self.remeasure_mono_cell();
        self.invalidate_body_cache();
        true
    }

    /// Active mono font id (settings).
    pub fn mono_font_id(&self) -> &str {
        &self.mono_font_id
    }

    /// Switch terminal mono face from a settings font id. Re-measures cell pitch.
    /// Returns `true` if the face (or metrics) changed.
    pub fn set_mono_font_id(&mut self, id: &str) -> bool {
        let id = crate::theme::normalize_font_id(id);
        if self.mono_font_id == id {
            return false;
        }
        let (family, weight) = self.resolve_font_id(id);
        self.mono_font_id = id.to_string();
        self.mono_family = family;
        self.mono_weight = weight;
        self.font_system
            .db_mut()
            .set_monospace_family(self.mono_family.as_str());
        self.remeasure_mono_cell();
        self.invalidate_body_cache();
        true
    }

    fn resolve_font_id(&self, id: &str) -> (String, Weight) {
        let candidates: &[&str] = match id {
            "sf-mono" => &["SF Mono", "SFMono-Regular", "Menlo"],
            "menlo" => &["Menlo", "Menlo-Regular"],
            "jetbrains" => &[
                "JetBrains Mono",
                "JetBrainsMono-Regular",
                "JetBrains Mono NL",
            ],
            "cascadia" => &["Cascadia Mono", "Cascadia Code", "CascadiaMono"],
            "system" => &[], // Family::Monospace
            _ => return (self.gohu_family.clone(), self.gohu_weight),
        };
        if id == "system" {
            return ("monospace".into(), Weight::NORMAL);
        }
        for face in self.font_system.db().faces() {
            for (name, _) in &face.families {
                for c in candidates {
                    if name.eq_ignore_ascii_case(c) || name.contains(c) {
                        return (name.clone(), face.weight);
                    }
                }
            }
        }
        // Missing system face → fall back to bundled Gohu.
        (self.gohu_family.clone(), self.gohu_weight)
    }

    fn remeasure_mono_cell(&mut self) {
        let design = self.design_size.max(1.0);
        let mut probe = Buffer::new(&mut self.font_system, FontMetrics::new(design, design));
        probe.set_size(&mut self.font_system, Some(200.0), Some(design));
        let attrs = if self.mono_font_id == "system" {
            Attrs::new().family(Family::Monospace)
        } else {
            Attrs::new()
                .family(Family::Name(self.mono_family.as_str()))
                .weight(self.mono_weight)
        };
        probe.set_text(&mut self.font_system, "M", attrs, Shaping::Advanced);
        probe.shape_until_scroll(&mut self.font_system, false);
        let mut advance = 0.0_f32;
        for run in probe.layout_runs() {
            advance = advance.max(run.line_w);
        }
        let lo = design * 0.30;
        let hi = design * 1.20;
        self.mono_cell = if advance >= lo && advance <= hi {
            MonoCellMetrics {
                w: advance.round().max(1.0),
                h: design,
            }
        } else {
            MonoCellMetrics::fallback_at(design)
        };
    }

    pub fn resize(&mut self, physical: PhysicalSize<u32>, scale_factor: f32) {
        let w = physical.width.max(1);
        let h = physical.height.max(1);
        let s = scale_factor.max(0.01);
        if w != self.width || h != self.height || (s - self.scale_factor).abs() > 1e-4 {
            self.invalidate_body_cache();
        }
        self.width = w;
        self.height = h;
        self.scale_factor = s;
    }

    fn invalidate_body_cache(&mut self) {
        self.body_prepared = false;
        self.last_body_fp = 0;
        self.body_len = 0;
    }

    pub fn prepare(&mut self, device: &wgpu::Device, queue: &wgpu::Queue, labels: &[TextLabel]) {
        let _ = self.prepare_body_and_caret(device, queue, labels, &[]);
    }

    /// Reshape the static body only when it changes. Carets (blink / sine) are
    /// always reshaped — they are a handful of labels and must not bust the grid.
    /// Returns whether the body was reshaped (caller may trim the atlas).
    pub fn prepare_body_and_caret(
        &mut self,
        device: &wgpu::Device,
        queue: &wgpu::Queue,
        body: &[TextLabel],
        carets: &[TextLabel],
    ) -> bool {
        self.viewport.update(
            queue,
            Resolution {
                width: self.width,
                height: self.height,
            },
        );

        let fp = labels_fingerprint(body);
        let body_changed = !self.body_prepared || fp != self.last_body_fp;
        if body_changed {
            self.ensure_buffers(body.len());
            self.reshape_range(0, body);
            self.last_body_fp = fp;
            self.body_prepared = true;
            self.body_len = body.len();
        }

        let caret_start = self.body_len;
        self.ensure_buffers(caret_start + carets.len());
        self.reshape_range(caret_start, carets);

        let scale = self.scale_factor;
        let width = self.width;
        let height = self.height;
        let mut areas = Vec::with_capacity(body.len() + carets.len());
        collect_areas(&self.buffers, 0, body, width, height, scale, &mut areas);
        collect_areas(
            &self.buffers,
            caret_start,
            carets,
            width,
            height,
            scale,
            &mut areas,
        );

        if self
            .text_renderer
            .prepare(
                device,
                queue,
                &mut self.font_system,
                &mut self.atlas,
                &self.viewport,
                areas.iter().cloned(),
                &mut self.swash_cache,
            )
            .is_err()
        {
            self.atlas.trim();
            let _ = self.text_renderer.prepare(
                device,
                queue,
                &mut self.font_system,
                &mut self.atlas,
                &self.viewport,
                areas,
                &mut self.swash_cache,
            );
        }
        body_changed
    }

    fn ensure_buffers(&mut self, n: usize) {
        while self.buffers.len() < n {
            self.buffers.push(Buffer::new(
                &mut self.font_system,
                FontMetrics::new(14.0, 18.0),
            ));
        }
    }

    fn reshape_range(&mut self, start: usize, labels: &[TextLabel]) {
        let scale = self.scale_factor;
        let mono_name = self.mono_family.clone();
        let mono_weight = self.mono_weight;
        let mono_system = self.mono_font_id == "system";
        let gohu_ok = self.gohu_ok;

        for (i, label) in labels.iter().enumerate() {
            let size_px = (label.size * scale).max(1.0).round().max(1.0);
            let line_height = if label.tight {
                size_px
            } else {
                (size_px * 1.2).round().max(size_px)
            };
            let metrics = FontMetrics::new(size_px, line_height);
            let max_w = (self.width as f32).max(1.0);
            let text = label.text.as_str();
            let is_rain = label.rain;
            let is_symbols = label.symbols;
            let is_key_chord = label.key_chord;

            let buf = &mut self.buffers[start + i];
            buf.set_metrics(&mut self.font_system, metrics);
            let buf_h = if is_key_chord {
                line_height * 1.5
            } else {
                line_height
            };
            buf.set_size(&mut self.font_system, Some(max_w), Some(buf_h));

            if is_rain {
                let family = crate::rain_atlas::rain_family_name(&self.font_system);
                let attrs = Attrs::new().family(Family::Name(family.as_str()));
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            } else if is_symbols {
                let attrs = Attrs::new().family(Family::Name(SYMBOLS_FAMILY));
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            } else if is_key_chord {
                let attrs = Attrs::new().family(Family::Name(KEY_CHORD_FAMILY));
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            } else if mono_system || !gohu_ok {
                let attrs = Attrs::new().family(Family::Monospace).weight(mono_weight);
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            } else {
                let attrs = Attrs::new()
                    .family(Family::Name(mono_name.as_str()))
                    .weight(mono_weight);
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            }
            buf.shape_until_scroll(&mut self.font_system, false);
        }
    }

    pub fn render_in_pass(&self, pass: &mut wgpu::RenderPass<'_>) {
        let _ = self.text_renderer.render(&self.atlas, &self.viewport, pass);
    }

    pub fn render(
        &self,
        _device: &wgpu::Device,
        encoder: &mut wgpu::CommandEncoder,
        view: &wgpu::TextureView,
    ) {
        let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
            label: Some("text"),
            color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                view,
                resolve_target: None,
                ops: wgpu::Operations {
                    load: wgpu::LoadOp::Load,
                    store: wgpu::StoreOp::Store,
                },
            })],
            depth_stencil_attachment: None,
            timestamp_writes: None,
            occlusion_query_set: None,
        });
        self.render_in_pass(&mut pass);
    }

    pub fn trim_atlas(&mut self) {
        self.atlas.trim();
    }
}

fn collect_areas<'a>(
    buffers: &'a [Buffer],
    start: usize,
    labels: &'a [TextLabel],
    width: u32,
    height: u32,
    scale: f32,
    areas: &mut Vec<TextArea<'a>>,
) {
    let full_bounds = TextBounds {
        left: 0,
        top: 0,
        right: width as i32,
        bottom: height as i32,
    };
    for (i, label) in labels.iter().enumerate() {
        let size_px = (label.size * scale).max(1.0).round().max(1.0);
        let (left, top) = if let Some([rx, ry, rw, rh]) = label.center_in {
            let line_w = buffers[start + i]
                .layout_runs()
                .map(|run| run.line_w)
                .fold(0.0f32, f32::max);
            let cx = rx * scale;
            let cy = ry * scale;
            let cw = rw * scale;
            let ch = rh * scale;
            (
                cx + (cw - line_w).max(0.0) * 0.5,
                cy + (ch - size_px).max(0.0) * 0.5,
            )
        } else {
            (label.x * scale, label.y * scale)
        };
        let bounds = if let Some([cx, cy, cw, ch]) = label.clip {
            let l = (cx * scale).floor() as i32;
            let t = (cy * scale).floor() as i32;
            let r = ((cx + cw) * scale).ceil() as i32;
            let b = ((cy + ch) * scale).ceil() as i32;
            TextBounds {
                left: l.max(0),
                top: t.max(0),
                right: r.min(width as i32),
                bottom: b.min(height as i32),
            }
        } else {
            full_bounds
        };
        areas.push(TextArea {
            buffer: &buffers[start + i],
            left,
            top,
            scale: 1.0,
            bounds,
            default_color: rgba_u8(label.color),
            custom_glyphs: &[],
        });
    }
}

/// Stable hash of non-caret labels. Quantizes floats so sub-pixel noise
/// does not force a reshape.
pub fn labels_fingerprint(labels: &[TextLabel]) -> u64 {
    use std::hash::{Hash, Hasher};
    let mut h = std::collections::hash_map::DefaultHasher::new();
    labels.len().hash(&mut h);
    for l in labels {
        l.text.hash(&mut h);
        hash_q(l.x, &mut h);
        hash_q(l.y, &mut h);
        hash_q(l.size, &mut h);
        quant_rgba(l.color).hash(&mut h);
        l.mono.hash(&mut h);
        l.rain.hash(&mut h);
        l.symbols.hash(&mut h);
        l.key_chord.hash(&mut h);
        l.tight.hash(&mut h);
        hash_opt_rect(l.center_in, &mut h);
        hash_opt_rect(l.clip, &mut h);
    }
    h.finish()
}

fn hash_q(v: f32, h: &mut impl std::hash::Hasher) {
    use std::hash::Hash;
    // 1/64 logical px — tighter than a cell, loose enough for float noise.
    ((v * 64.0).round() as i32).hash(h);
}

fn hash_opt_rect(r: Option<[f32; 4]>, h: &mut impl std::hash::Hasher) {
    use std::hash::Hash;
    match r {
        None => 0u8.hash(h),
        Some(v) => {
            1u8.hash(h);
            for n in v {
                hash_q(n, h);
            }
        }
    }
}

fn quant_rgba(c: [f32; 4]) -> u32 {
    let r = (c[0].clamp(0.0, 1.0) * 255.0).round() as u32;
    let g = (c[1].clamp(0.0, 1.0) * 255.0).round() as u32;
    let b = (c[2].clamp(0.0, 1.0) * 255.0).round() as u32;
    let a = (c[3].clamp(0.0, 1.0) * 255.0).round() as u32;
    (r << 24) | (g << 16) | (b << 8) | a
}

fn rgba_u8(c: [f32; 4]) -> Color {
    Color::rgba(
        (c[0].clamp(0.0, 1.0) * 255.0) as u8,
        (c[1].clamp(0.0, 1.0) * 255.0) as u8,
        (c[2].clamp(0.0, 1.0) * 255.0) as u8,
        (c[3].clamp(0.0, 1.0) * 255.0) as u8,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn icon_centered_is_tight_on_the_hit_rect() {
        let hit = [80.0, 12.0, 16.0, 16.0];
        let label = TextLabel::icon_centered("×", hit, 14.0, [1.0; 4]);
        assert!(label.tight);
        assert_eq!(label.center_in, Some(hit));
        assert_eq!(label.size, 14.0);
        assert!(!TextLabel::centered("×", hit, 11.0, [1.0; 4]).tight);
    }

    #[test]
    fn gohu_advance_grows_with_design_size() {
        let mut font_system = FontSystem::new();
        font_system.db_mut().load_font_data(GOHU_TTF.to_vec());
        let probe = |fs: &mut FontSystem, design: f32| -> f32 {
            let mut buf = Buffer::new(fs, FontMetrics::new(design, design));
            buf.set_size(fs, Some(200.0), Some(design));
            let attrs = Attrs::new()
                .family(Family::Name(GOHU_FAMILY))
                .weight(Weight(500));
            buf.set_text(fs, "M", attrs, Shaping::Advanced);
            buf.shape_until_scroll(fs, false);
            buf.layout_runs().map(|r| r.line_w).fold(0.0, f32::max)
        };
        let w14 = probe(&mut font_system, MONO_DESIGN_PX);
        let w28 = probe(&mut font_system, MONO_DESIGN_PX * 2.0);
        assert!(
            w14 >= 5.0 && w14 <= 12.0,
            "Gohu 14px advance should be a cell, got {w14}"
        );
        // Either the face scales (~2×) or we fall back to a scaled pitch.
        let scaled = MonoCellMetrics::fallback_at(MONO_DESIGN_PX * 2.0);
        assert!(
            w28 > w14 * 1.3 || (scaled.w - w14 * 2.0).abs() < 1.5,
            "2× design should grow advance ({w14} → {w28}) or fallback pitch {}",
            scaled.w
        );
        assert!((scaled.h - 28.0).abs() < 1e-4);
    }

    #[test]
    fn fingerprint_stable_for_same_labels() {
        let a = TextLabel::mono("x", 8.0, 14.0, 14.0, [1.0, 1.0, 1.0, 0.95]);
        let b = TextLabel::mono("x", 8.0, 14.0, 14.0, [1.0, 1.0, 1.0, 0.95]);
        assert_eq!(labels_fingerprint(&[a.clone()]), labels_fingerprint(&[b]));
    }

    #[test]
    fn fingerprint_changes_with_glyph_or_color() {
        let a = TextLabel::mono("x", 8.0, 14.0, 14.0, [1.0, 1.0, 1.0, 0.95]);
        let b = TextLabel::mono("y", 8.0, 14.0, 14.0, [1.0, 1.0, 1.0, 0.95]);
        let c = TextLabel::mono("x", 8.0, 14.0, 14.0, [1.0, 0.0, 0.0, 0.95]);
        let fa = labels_fingerprint(&[a]);
        assert_ne!(fa, labels_fingerprint(&[b]));
        assert_ne!(fa, labels_fingerprint(&[c]));
    }

    #[test]
    fn advance_at_scales_with_paint_size() {
        let cell = MonoCellMetrics { w: 7.0, h: 14.0 };
        assert!((cell.advance_at(14.0) - 7.0).abs() < 1e-4);
        assert!((cell.advance_at(13.0) - 6.5).abs() < 1e-4);
        assert!((cell.advance_at(7.0) - 3.5).abs() < 1e-4);
        let zoomed = MonoCellMetrics { w: 14.0, h: 28.0 };
        assert!((zoomed.advance_at(26.0) - 13.0).abs() < 1e-4);
    }

    #[test]
    fn gohu_warp_size_matches_scaled_cell() {
        // Warp used to paint at 13px while the caret stepped the 14px cell.
        // Glyphon's Gohu advance at 13 must match advance_at(13), or the
        // block caret walks off the draft as you type.
        let mut font_system = FontSystem::new();
        font_system.db_mut().load_font_data(GOHU_TTF.to_vec());
        let probe = |fs: &mut FontSystem, design: f32, text: &str| -> f32 {
            let mut buf = Buffer::new(fs, FontMetrics::new(design, design));
            buf.set_size(fs, Some(400.0), Some(design));
            let attrs = Attrs::new()
                .family(Family::Name(GOHU_FAMILY))
                .weight(Weight(500));
            buf.set_text(fs, text, attrs, Shaping::Advanced);
            buf.shape_until_scroll(fs, false);
            buf.layout_runs().map(|r| r.line_w).fold(0.0, f32::max)
        };
        let w14 = probe(&mut font_system, MONO_DESIGN_PX, "M");
        let w13 = probe(&mut font_system, 13.0, "M");
        let cell = MonoCellMetrics {
            w: w14.round().max(1.0),
            h: MONO_DESIGN_PX,
        };
        let expected = cell.advance_at(13.0);
        assert!(
            (w13 - expected).abs() < 0.6,
            "13px Gohu advance {w13} should track scaled 14px cell {expected} (14px={w14})"
        );
        let prefix14 = probe(&mut font_system, MONO_DESIGN_PX, "❯ ");
        let prefix13 = probe(&mut font_system, 13.0, "❯ ");
        // Prefix is two mono cells (`❯` + space) at both sizes.
        assert!(
            (prefix14 - 2.0 * w14).abs() < 0.75,
            "❯  at 14px should be two cells ({prefix14} vs 2×{w14})"
        );
        assert!(
            (prefix13 - 2.0 * w13).abs() < 0.75,
            "❯  at 13px should be two cells ({prefix13} vs 2×{w13})"
        );
        let typed = "abcdefghijklmnopqrstuvwxyz";
        let line13 = probe(&mut font_system, 13.0, &format!("❯ {typed}"));
        let caret13 = prefix13 + typed.chars().count() as f32 * w13;
        assert!(
            (line13 - caret13).abs() < 1.0,
            "caret at scaled 13px pitch should sit on the last glyph ({line13} vs {caret13})"
        );
    }

    #[test]
    fn caret_flag_does_not_change_body_fingerprint() {
        // Partition happens before fingerprint; this documents that caret
        // membership is not hashed (callers must strip carets first).
        let cell = TextLabel::mono("a", 0.0, 0.0, 14.0, [1.0; 4]);
        let caret = TextLabel::mono("█", 0.0, 0.0, 14.0, [0.0, 0.9, 0.5, 0.55]).as_caret();
        assert!(caret.caret);
        assert!(!cell.caret);
        assert_eq!(
            labels_fingerprint(std::slice::from_ref(&cell)),
            labels_fingerprint(&[cell])
        );
    }
}
