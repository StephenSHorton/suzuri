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

/// Mono cell size in **logical** px (grid + paint). Measured from Gohu when possible.
#[derive(Clone, Copy, Debug)]
pub struct MonoCellMetrics {
    pub w: f32,
    pub h: f32,
}

impl Default for MonoCellMetrics {
    fn default() -> Self {
        // Gohu uni14 design size — fallback if measurement fails.
        Self { w: 7.0, h: 14.0 }
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
        }
    }

    pub fn centered(
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
            mono: true,
            rain: false,
            symbols: false,
            key_chord: false,
            center_in: Some(rect),
            clip: None,
        }
    }

    /// Clip this label to a logical rect (e.g. terminal cells hole).
    pub fn with_clip(mut self, clip: [f32; 4]) -> Self {
        self.clip = Some(clip);
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
            eprintln!(
                "suzuri-chrome: Gohu face not found after load (expected `{GOHU_FAMILY}`)"
            );
        }

        let swash_cache = SwashCache::new();
        let cache = Cache::new(device);
        let viewport = Viewport::new(device, &cache);
        let mut atlas = TextAtlas::new(device, queue, &cache, format);
        let text_renderer = TextRenderer::new(
            &mut atlas,
            device,
            wgpu::MultisampleState::default(),
            None,
        );

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
            eprintln!(
                "suzuri-chrome: mono cell {}×{} logical px",
                cell.w, cell.h
            );
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
        }
    }

    /// Measured mono cell size (logical px) for PTY grid + terminal paint.
    pub fn mono_cell(&self) -> MonoCellMetrics {
        self.mono_cell
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
        true
    }

    fn resolve_font_id(&self, id: &str) -> (String, Weight) {
        let candidates: &[&str] = match id {
            "sf-mono" => &["SF Mono", "SFMono-Regular", "Menlo"],
            "menlo" => &["Menlo", "Menlo-Regular"],
            "jetbrains" => &["JetBrains Mono", "JetBrainsMono-Regular", "JetBrains Mono NL"],
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
        let design = 14.0_f32;
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
        self.mono_cell = if advance >= 5.0 && advance <= 16.0 {
            MonoCellMetrics {
                w: advance.round().max(1.0),
                h: design,
            }
        } else {
            MonoCellMetrics::default()
        };
    }

    pub fn resize(&mut self, physical: PhysicalSize<u32>, scale_factor: f32) {
        self.width = physical.width.max(1);
        self.height = physical.height.max(1);
        self.scale_factor = scale_factor.max(0.01);
    }

    pub fn prepare(
        &mut self,
        device: &wgpu::Device,
        queue: &wgpu::Queue,
        labels: &[TextLabel],
    ) {
        self.viewport.update(
            queue,
            Resolution {
                width: self.width,
                height: self.height,
            },
        );

        let scale = self.scale_factor;

        while self.buffers.len() < labels.len() {
            self.buffers.push(Buffer::new(
                &mut self.font_system,
                FontMetrics::new(14.0, 18.0),
            ));
        }

        let mono_name = self.mono_family.clone();
        let mono_weight = self.mono_weight;
        let mono_system = self.mono_font_id == "system";
        let gohu_ok = self.gohu_ok;

        for (i, label) in labels.iter().enumerate() {
            // Prefer integer physical px (bitmap-friendly).
            let size_px = (label.size * scale).max(1.0).round().max(1.0);
            let line_height = (size_px * 1.2).round().max(size_px);
            let metrics = FontMetrics::new(size_px, line_height);
            let max_w = (self.width as f32).max(1.0);
            let text = label.text.as_str();
            let is_rain = label.rain;
            let is_symbols = label.symbols;
            let is_key_chord = label.key_chord;

            let buf = &mut self.buffers[i];
            buf.set_metrics(&mut self.font_system, metrics);
            // Key chords: give the buffer a wide single-line box so multi-glyph
            // modifiers (⇧⌘T) never wrap into a second line.
            let buf_h = if is_key_chord {
                line_height * 1.5
            } else {
                line_height
            };
            buf.set_size(&mut self.font_system, Some(max_w), Some(buf_h));

            if is_rain {
                #[cfg(target_os = "macos")]
                {
                    let attrs = Attrs::new().family(Family::Name("Hiragino Sans"));
                    buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
                }
                #[cfg(not(target_os = "macos"))]
                {
                    let attrs = Attrs::new().family(Family::SansSerif);
                    buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
                }
            } else if is_symbols {
                let attrs = Attrs::new().family(Family::Name(SYMBOLS_FAMILY));
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            } else if is_key_chord {
                // One system UI face for the whole chord → shared baseline / advances.
                let attrs = Attrs::new().family(Family::Name(KEY_CHORD_FAMILY));
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            } else if mono_system || !gohu_ok {
                let attrs = Attrs::new()
                    .family(Family::Monospace)
                    .weight(mono_weight);
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            } else {
                // Named mono face (Gohu weight 500, system faces usually 400).
                let attrs = Attrs::new()
                    .family(Family::Name(mono_name.as_str()))
                    .weight(mono_weight);
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            }
            buf.shape_until_scroll(&mut self.font_system, false);
        }

        let full_bounds = TextBounds {
            left: 0,
            top: 0,
            right: self.width as i32,
            bottom: self.height as i32,
        };

        let areas: Vec<TextArea> = labels
            .iter()
            .enumerate()
            .map(|(i, label)| {
                let size_px = (label.size * scale).max(1.0).round().max(1.0);
                let (left, top) = if let Some([rx, ry, rw, rh]) = label.center_in {
                    let line_w = self.buffers[i]
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
                // Optional clip to a logical rect (terminal cells hole).
                let bounds = if let Some([cx, cy, cw, ch]) = label.clip {
                    let l = (cx * scale).floor() as i32;
                    let t = (cy * scale).floor() as i32;
                    let r = ((cx + cw) * scale).ceil() as i32;
                    let b = ((cy + ch) * scale).ceil() as i32;
                    TextBounds {
                        left: l.max(0),
                        top: t.max(0),
                        right: r.min(self.width as i32),
                        bottom: b.min(self.height as i32),
                    }
                } else {
                    full_bounds
                };
                TextArea {
                    buffer: &self.buffers[i],
                    left,
                    top,
                    scale: 1.0,
                    bounds,
                    default_color: rgba_u8(label.color),
                    custom_glyphs: &[],
                }
            })
            .collect();

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

fn rgba_u8(c: [f32; 4]) -> Color {
    Color::rgba(
        (c[0].clamp(0.0, 1.0) * 255.0) as u8,
        (c[1].clamp(0.0, 1.0) * 255.0) as u8,
        (c[2].clamp(0.0, 1.0) * 255.0) as u8,
        (c[3].clamp(0.0, 1.0) * 255.0) as u8,
    )
}
