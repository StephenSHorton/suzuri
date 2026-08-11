//! Minimal GPU text overlay via [glyphon] (cosmic-text + wgpu 24).
//!
//! # How to draw a string
//!
//! ```ignore
//! // Once, after device/queue/format are ready:
//! let mut text = TextLayer::new(&device, &queue, surface_format);
//! text.resize(physical_size, scale_factor);
//!
//! // Each frame (logical px match layout.rs):
//! text.prepare(
//!     &device,
//!     &queue,
//!     &[TextLabel {
//!         text: "hello".into(),
//!         x: 12.0,
//!         y: 10.0,
//!         size: 14.0,
//!         color: [1.0, 1.0, 1.0, 0.9],
//!     }],
//! );
//!
//! // In a render pass that loads the existing surface (do not clear):
//! text.render_in_pass(&mut pass);
//! // or:
//! text.render(&device, &mut encoder, &view);
//! text.trim_atlas(); // call after submit, once per frame is fine
//! ```
//!
//! Label `x` / `y` / `size` are **logical** pixels (CSS-px style), same as
//! [`crate::layout`]. They are scaled by the last `resize` scale factor.

use glyphon::{
    Attrs, Buffer, Cache, Color, Family, FontSystem, Metrics as FontMetrics, Resolution, Shaping,
    SwashCache, TextArea, TextAtlas, TextBounds, TextRenderer, Viewport,
};
use winit::dpi::PhysicalSize;

/// One screen-space label (logical pixels).
#[derive(Clone, Debug)]
pub struct TextLabel {
    pub text: String,
    /// Top-left of the layout box, logical px (used when `center_in` is None).
    pub x: f32,
    pub y: f32,
    /// Font size in logical px.
    pub size: f32,
    /// RGBA in 0..=1.
    pub color: [f32; 4],
    /// Use monospace family (terminal cell lines).
    pub mono: bool,
    /// Digital-rain glyph — prefer a CJK face that covers half-width katakana.
    pub rain: bool,
    /// If set `[x, y, w, h]`, center the shaped glyph box in this rect (logical px).
    /// Overrides `x` / `y` after measuring line width.
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
            center_in: None,
        }
    }

    /// Center this label inside a logical rect (nav chips, icon buttons, etc.).
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
            mono: false,
            rain: false,
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
    /// Reused shape buffers, one per label prepared last frame.
    buffers: Vec<Buffer>,
    width: u32,
    height: u32,
    scale_factor: f32,
}

impl TextLayer {
    /// Construct with the surface format used for the composite target.
    pub fn new(device: &wgpu::Device, queue: &wgpu::Queue, format: wgpu::TextureFormat) -> Self {
        let mut font_system = FontSystem::new();
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

        // Seed one buffer so prepare always has a metrics template.
        let seed = Buffer::new(&mut font_system, FontMetrics::new(14.0, 18.0));

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
        }
    }

    /// Update physical resolution and DPI scale (logical → physical).
    pub fn resize(&mut self, physical: PhysicalSize<u32>, scale_factor: f32) {
        self.width = physical.width.max(1);
        self.height = physical.height.max(1);
        self.scale_factor = scale_factor.max(0.01);
    }

    /// Shape and upload `labels` for the next draw. Call once per frame before render.
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

        // Ensure we have one buffer per label.
        while self.buffers.len() < labels.len() {
            self.buffers.push(Buffer::new(
                &mut self.font_system,
                FontMetrics::new(14.0, 18.0),
            ));
        }

        for (i, label) in labels.iter().enumerate() {
            let size_px = (label.size * scale).max(1.0);
            let line_height = size_px * 1.25;
            let metrics = FontMetrics::new(size_px, line_height);

            let buf = &mut self.buffers[i];
            buf.set_metrics(&mut self.font_system, metrics);
            // Single-line chrome labels: generous width, one line of height.
            let max_w = (self.width as f32).max(1.0);
            buf.set_size(
                &mut self.font_system,
                Some(max_w),
                Some(line_height * 2.0),
            );
            // Rain needs half-width katakana — product suzuri uses a CJK face.
            // Hiragino Sans covers FF61–FF9F on macOS; SansSerif fallback otherwise.
            let attrs = if label.rain {
                #[cfg(target_os = "macos")]
                {
                    Attrs::new().family(Family::Name("Hiragino Sans"))
                }
                #[cfg(not(target_os = "macos"))]
                {
                    Attrs::new().family(Family::SansSerif)
                }
            } else if label.mono {
                Attrs::new().family(Family::Monospace)
            } else {
                Attrs::new().family(Family::SansSerif)
            };
            buf.set_text(
                &mut self.font_system,
                &label.text,
                attrs,
                Shaping::Advanced,
            );
            buf.shape_until_scroll(&mut self.font_system, false);
        }

        // Build areas that borrow the buffers.
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
                let size_px = (label.size * scale).max(1.0);
                // Must match set_metrics line_height above.
                let line_h = size_px * 1.25;
                let (left, top) = if let Some([rx, ry, rw, rh]) = label.center_in {
                    // Shaped line width in physical px (glyphon buffer is physical).
                    let line_w = self.buffers[i]
                        .layout_runs()
                        .map(|run| run.line_w)
                        .fold(0.0f32, f32::max);
                    let cx = rx * scale;
                    let cy = ry * scale;
                    let cw = rw * scale;
                    let ch = rh * scale;
                    // Optical nudge: sans caps sit slightly high in the EM box.
                    let optical_y = 0.5 * scale;
                    (
                        cx + (cw - line_w).max(0.0) * 0.5,
                        cy + (ch - line_h).max(0.0) * 0.5 + optical_y,
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

        // Retry once if the atlas fills up mid-prepare.
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

    /// Draw prepared text into an existing render pass (load, don't clear).
    pub fn render_in_pass(&self, pass: &mut wgpu::RenderPass<'_>) {
        let _ = self
            .text_renderer
            .render(&self.atlas, &self.viewport, pass);
    }

    /// Begin a load-preserving pass on `view` and draw prepared text.
    pub fn render(
        &self,
        _device: &wgpu::Device,
        encoder: &mut wgpu::CommandEncoder,
        view: &wgpu::TextureView,
    ) {
        let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
            label: Some("text overlay"),
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

    /// Drop unused atlas pages (call after `queue.submit`).
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
