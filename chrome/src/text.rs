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

/// Primary caret color (jade), alpha applied by caller.
pub const CARET_RGB: [f32; 3] = [0.0, 0.90, 0.46];

/// System face for ☕ (Gohu has no U+2615).
#[cfg(target_os = "macos")]
const SYMBOLS_FAMILY: &str = "Apple Symbols";
#[cfg(target_os = "windows")]
const SYMBOLS_FAMILY: &str = "Segoe UI Symbol";
#[cfg(not(any(target_os = "macos", target_os = "windows")))]
const SYMBOLS_FAMILY: &str = "Noto Sans Symbols2";

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
    pub center_in: Option<[f32; 4]>,
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
            center_in: None,
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
            center_in: None,
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
            center_in: Some(rect),
        }
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
            center_in: Some(rect),
        }
    }
}

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
        let gohu_ok = {
            let mut probe = Buffer::new(&mut font_system, FontMetrics::new(14.0, 18.0));
            probe.set_size(&mut font_system, Some(200.0), Some(20.0));
            let attrs = Attrs::new()
                .family(Family::Name(gohu_family.as_str()))
                .weight(gohu_weight);
            probe.set_text(&mut font_system, "W", attrs, Shaping::Advanced);
            probe.shape_until_scroll(&mut font_system, false);
            let mut ok = false;
            for run in probe.layout_runs() {
                for g in run.glyphs.iter() {
                    // Advance ~8px at 14 for Gohu mono cells
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
            ok
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
            gohu_family,
            gohu_weight,
            gohu_ok,
        }
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

        let gohu_name = self.gohu_family.clone();
        let gohu_weight = self.gohu_weight;
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

            let buf = &mut self.buffers[i];
            buf.set_metrics(&mut self.font_system, metrics);
            buf.set_size(&mut self.font_system, Some(max_w), Some(line_height));

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
            } else if gohu_ok {
                // MUST set weight=500 or cosmic-text will not select Gohu.
                let attrs = Attrs::new()
                    .family(Family::Name(gohu_name.as_str()))
                    .weight(gohu_weight);
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            } else {
                let attrs = Attrs::new()
                    .family(Family::Monospace)
                    .weight(gohu_weight);
                buf.set_text(&mut self.font_system, text, attrs, Shaping::Advanced);
            }
            buf.shape_until_scroll(&mut self.font_system, false);
        }

        let bounds = TextBounds {
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
