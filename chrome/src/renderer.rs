//! wgpu renderer: rain pass → glass composite → surface.

use std::sync::Arc;

use bytemuck::{Pod, Zeroable};
use wgpu::util::DeviceExt;
use winit::window::Window;

use crate::chrome_ui::{ChipUi, TabJelly};
use crate::commands::{filter_commands, Command, HelpState, PaletteState};
use crate::confirm::ConfirmState;
use crate::input::{is_mac, traffic_light_rects};
use crate::layout::{FrameLayout, Metrics, PaneLayout, PanelInstance};
use crate::rain_atlas::RainAtlas;
use crate::rain_sim;
use crate::selection::Selection;
use crate::session::ChromeSession;
use crate::caffeine::Caffeine;
use crate::notes::NotesState;
use crate::settings::SettingsState;
use crate::transfer_ui::TransferUi;
use crate::workspace_ui::WorkspaceUi;
use crate::text::{TextLabel, TextLayer, CARET_BLOCK, CARET_RGB};

/// Jade selection wash alpha for full-block underlay (`█` mono labels).
const SELECTION_ALPHA: f32 = 0.32;

/// Mono cell metrics for GohuFont uni14 (product design size 14px).
/// Width ≈ half em of a 14px bitmap mono; height matches line box used in text layer.
pub const CELL_W: f32 = 7.0;
pub const CELL_H: f32 = 14.0;
/// Default inner glass inset — prefer [`Metrics::inset`] at call sites.
/// Kept for callers that don't have Metrics yet; matches Spacing::inset.
pub const TERM_PAD: f32 = 8.0; // matches Spacing::inset (inside); outside is Spacing::space

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct RainUniforms {
    /// xy = fb size, z = time, w = unused
    res_time: [f32; 4],
    /// x=cell, y=speed, z=speedVar, w=density
    params: [f32; 4],
    /// x=trail, y=glow, z=mutate, w=flicker
    params2: [f32; 4],
    /// x=layers, y=glyphCount, z=atlasGrid, w=unused
    params3: [f32; 4],
    color: [f32; 4],
    head_color: [f32; 4],
}

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct FrameUniforms {
    size: [f32; 4],
    /// x=time, y=dpr, z=panel_count, w=glass face darken (0..1)
    misc: [f32; 4],
    /// Canvas UI Glass defaults: ior, edge, bevel, depth
    glass: [f32; 4],
    /// aberration, blur, reflection, shine
    glass2: [f32; 4],
    /// xy = pointer logical, z = spotlight radius, w = 1 if pointer inside
    hover: [f32; 4],
}

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct LensUniforms {
    size: [f32; 4],
    lens: [f32; 4],
    glass: [f32; 4],
    glass2: [f32; 4],
}

/// Canvas UI `GlassVanilla` DEFAULTS — https://github.com/DavidHDev/canvas-ui
const GLASS_IOR: f32 = 1.5;
const GLASS_EDGE: f32 = 0.7;
const GLASS_BEVEL: f32 = 4.0;
const GLASS_DEPTH: f32 = 250.0;
const GLASS_ABERRATION: f32 = 1.0;
const GLASS_BLUR: f32 = 0.0;
const GLASS_REFLECTION: f32 = 1.0;
const GLASS_SHINE: f32 = 0.01;
/// Magnifier bubble — radius at full “level 1” (logical px). Can grow beyond.
const MAG_RADIUS_BASE: f32 = 110.0;
/// Max magnifier level (pinch / scroll accumulate into this).
const MAG_LEVEL_MAX: f32 = 2.8;
/// Canvas UI follow feel when the bubble is alive (~follow 0.2).
const LENS_FOLLOW: f32 = 0.2;

pub struct Renderer {
    surface: wgpu::Surface<'static>,
    device: wgpu::Device,
    queue: wgpu::Queue,
    config: wgpu::SurfaceConfiguration,
    size: winit::dpi::PhysicalSize<u32>,
    scale_factor: f32,

    rain_tex: wgpu::Texture,
    rain_view: wgpu::TextureView,
    rain_pipeline: wgpu::RenderPipeline,
    rain_bgl: wgpu::BindGroupLayout,
    rain_uniform_buf: wgpu::Buffer,
    rain_atlas: RainAtlas,
    /// Canvas UI GlyphRain starts at t=7.3
    rain_time: f32,

    composite_pipeline: wgpu::RenderPipeline,
    composite_bgl: wgpu::BindGroupLayout,
    frame_uniform_buf: wgpu::Buffer,
    panel_buf: wgpu::Buffer,
    panel_capacity: u64,
    sampler: wgpu::Sampler,

    /// Full-scene RT (composite + text) sampled by the cursor lens.
    scene_tex: wgpu::Texture,
    scene_view: wgpu::TextureView,
    lens_pipeline: wgpu::RenderPipeline,
    lens_bgl: wgpu::BindGroupLayout,
    lens_uniform_buf: wgpu::Buffer,

    metrics: Metrics,
    start: std::time::Instant,
    last_frame: std::time::Instant,

    /// Chrome + terminal text labels.
    text: TextLayer,

    /// Magnifying-glass bubble (pinch / Ctrl·Cmd+scroll). Not always-on.
    lens_pos: [f32; 2],
    lens_target: [f32; 2],
    /// Target power 0..MAG_LEVEL_MAX from gestures.
    mag_level: f32,
    /// Smoothed power for bubble grow/shrink + zoom.
    mag_level_smooth: f32,
    /// Smoothed radius / presence / magnify (derived each frame).
    lens_radius: f32,
    lens_presence: f32,
    lens_magnify: f32,
    pointer_inside: bool,

    /// Active-tab jelly connector (shared with app tick).
    pub tab_jelly: TabJelly,
    surface_format: wgpu::TextureFormat,
}

impl Renderer {
    pub async fn new(window: Arc<Window>) -> Self {
        let size = window.inner_size();
        let scale_factor = window.scale_factor() as f32;

        let instance = wgpu::Instance::new(&wgpu::InstanceDescriptor {
            backends: wgpu::Backends::PRIMARY,
            ..Default::default()
        });
        let surface = instance
            .create_surface(window.clone())
            .expect("create surface");

        let adapter = instance
            .request_adapter(&wgpu::RequestAdapterOptions {
                power_preference: wgpu::PowerPreference::HighPerformance,
                compatible_surface: Some(&surface),
                force_fallback_adapter: false,
            })
            .await
            .expect("no suitable GPU adapter");

        let (device, queue) = adapter
            .request_device(
                &wgpu::DeviceDescriptor {
                    label: Some("suzuri-chrome"),
                    required_features: wgpu::Features::empty(),
                    required_limits: wgpu::Limits::default(),
                    memory_hints: Default::default(),
                },
                None,
            )
            .await
            .expect("request_device");

        let caps = surface.get_capabilities(&adapter);
        let format = caps
            .formats
            .iter()
            .copied()
            .find(|f| f.is_srgb())
            .unwrap_or(caps.formats[0]);

        // Prefer premultiplied so transparent window corners (macOS rounded)
        // composite correctly over the desktop.
        let alpha_mode = caps
            .alpha_modes
            .iter()
            .copied()
            .find(|m| *m == wgpu::CompositeAlphaMode::PreMultiplied)
            .or_else(|| {
                caps.alpha_modes
                    .iter()
                    .copied()
                    .find(|m| *m == wgpu::CompositeAlphaMode::PostMultiplied)
            })
            .unwrap_or(caps.alpha_modes[0]);

        let config = wgpu::SurfaceConfiguration {
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
            format,
            width: size.width.max(1),
            height: size.height.max(1),
            present_mode: wgpu::PresentMode::AutoVsync,
            alpha_mode,
            view_formats: vec![],
            desired_maximum_frame_latency: 2,
        };
        surface.configure(&device, &config);

        let rain_shader = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("rain"),
            source: wgpu::ShaderSource::Wgsl(include_str!("shaders/rain.wgsl").into()),
        });
        let composite_shader = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("composite"),
            source: wgpu::ShaderSource::Wgsl(include_str!("shaders/composite.wgsl").into()),
        });

        let rain_atlas = RainAtlas::build(&device, &queue);

        let rain_uniform_buf = device.create_buffer_init(&wgpu::util::BufferInitDescriptor {
            label: Some("rain uniforms"),
            contents: bytemuck::bytes_of(&RainUniforms {
                res_time: [1.0, 1.0, 7.3, 0.0],
                params: [
                    rain_sim::CELL,
                    rain_sim::SPEED,
                    rain_sim::SPEED_VARIANCE,
                    rain_sim::DENSITY,
                ],
                params2: [
                    rain_sim::TRAIL,
                    rain_sim::GLOW,
                    rain_sim::MUTATE,
                    rain_sim::FLICKER,
                ],
                params3: [rain_sim::LAYERS, rain_atlas.count, rain_atlas.grid, 0.0],
                color: [
                    rain_sim::COLOR[0],
                    rain_sim::COLOR[1],
                    rain_sim::COLOR[2],
                    1.0,
                ],
                head_color: [
                    rain_sim::HEAD_COLOR[0],
                    rain_sim::HEAD_COLOR[1],
                    rain_sim::HEAD_COLOR[2],
                    1.0,
                ],
            }),
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        });

        let rain_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("rain bgl"),
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Buffer {
                        ty: wgpu::BufferBindingType::Uniform,
                        has_dynamic_offset: false,
                        min_binding_size: None,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: true },
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 2,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                    count: None,
                },
            ],
        });

        let rain_pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
            label: Some("rain pipeline"),
            layout: Some(
                &device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
                    label: Some("rain pl"),
                    bind_group_layouts: &[&rain_bgl],
                    push_constant_ranges: &[],
                }),
            ),
            vertex: wgpu::VertexState {
                module: &rain_shader,
                entry_point: Some("vs"),
                buffers: &[],
                compilation_options: Default::default(),
            },
            fragment: Some(wgpu::FragmentState {
                module: &rain_shader,
                entry_point: Some("fs"),
                targets: &[Some(wgpu::ColorTargetState {
                    format: wgpu::TextureFormat::Rgba8UnormSrgb,
                    blend: None,
                    write_mask: wgpu::ColorWrites::ALL,
                })],
                compilation_options: Default::default(),
            }),
            primitive: wgpu::PrimitiveState::default(),
            depth_stencil: None,
            multisample: wgpu::MultisampleState::default(),
            multiview: None,
            cache: None,
        });

        let (rain_tex, rain_view) = create_rain_target(&device, config.width, config.height);

        let sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            label: Some("rain sampler"),
            mag_filter: wgpu::FilterMode::Linear,
            min_filter: wgpu::FilterMode::Linear,
            ..Default::default()
        });

        let frame_uniform_buf = device.create_buffer_init(&wgpu::util::BufferInitDescriptor {
            label: Some("frame uniforms"),
            contents: bytemuck::bytes_of(&FrameUniforms {
                size: [1.0, 1.0, 1.0, 1.0],
                misc: [0.0, 1.0, 0.0, crate::settings::GLASS_DARKEN_DEFAULT],
                glass: [GLASS_IOR, GLASS_EDGE, GLASS_BEVEL, GLASS_DEPTH],
                glass2: [
                    GLASS_ABERRATION,
                    GLASS_BLUR,
                    GLASS_REFLECTION,
                    GLASS_SHINE,
                ],
                hover: [0.0, 0.0, 28.0, 0.0],
            }),
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        });

        let panel_capacity = 32u64;
        let panel_buf = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("panels"),
            size: panel_capacity * std::mem::size_of::<PanelInstance>() as u64,
            usage: wgpu::BufferUsages::STORAGE | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });

        let composite_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("composite bgl"),
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Buffer {
                        ty: wgpu::BufferBindingType::Uniform,
                        has_dynamic_offset: false,
                        min_binding_size: None,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: true },
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 2,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 3,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Buffer {
                        ty: wgpu::BufferBindingType::Storage { read_only: true },
                        has_dynamic_offset: false,
                        min_binding_size: None,
                    },
                    count: None,
                },
            ],
        });

        let composite_pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
            label: Some("composite pipeline"),
            layout: Some(
                &device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
                    label: Some("composite pl"),
                    bind_group_layouts: &[&composite_bgl],
                    push_constant_ranges: &[],
                }),
            ),
            vertex: wgpu::VertexState {
                module: &composite_shader,
                entry_point: Some("vs"),
                buffers: &[],
                compilation_options: Default::default(),
            },
            fragment: Some(wgpu::FragmentState {
                module: &composite_shader,
                entry_point: Some("fs"),
                targets: &[Some(wgpu::ColorTargetState {
                    format,
                    blend: None,
                    write_mask: wgpu::ColorWrites::ALL,
                })],
                compilation_options: Default::default(),
            }),
            primitive: wgpu::PrimitiveState::default(),
            depth_stencil: None,
            multisample: wgpu::MultisampleState::default(),
            multiview: None,
            cache: None,
        });

        let mut text = TextLayer::new(&device, &queue, format);
        text.resize(size, scale_factor);

        let (scene_tex, scene_view) =
            create_scene_target(&device, format, config.width, config.height);

        let lens_shader = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("lens"),
            source: wgpu::ShaderSource::Wgsl(include_str!("shaders/lens.wgsl").into()),
        });
        let lens_uniform_buf = device.create_buffer_init(&wgpu::util::BufferInitDescriptor {
            label: Some("lens uniforms"),
            contents: bytemuck::bytes_of(&LensUniforms {
                size: [1.0, 1.0, 1.0, 1.0],
                lens: [0.0, 0.0, 0.0, 0.0],
                glass: [GLASS_IOR, GLASS_EDGE, GLASS_BEVEL, GLASS_DEPTH],
                glass2: [
                    GLASS_ABERRATION,
                    GLASS_BLUR,
                    GLASS_REFLECTION,
                    GLASS_SHINE,
                ],
            }),
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        });
        let lens_bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("lens bgl"),
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Buffer {
                        ty: wgpu::BufferBindingType::Uniform,
                        has_dynamic_offset: false,
                        min_binding_size: None,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: true },
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 2,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                    count: None,
                },
            ],
        });
        let lens_pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
            label: Some("lens pipeline"),
            layout: Some(
                &device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
                    label: Some("lens pl"),
                    bind_group_layouts: &[&lens_bgl],
                    push_constant_ranges: &[],
                }),
            ),
            vertex: wgpu::VertexState {
                module: &lens_shader,
                entry_point: Some("vs"),
                buffers: &[],
                compilation_options: Default::default(),
            },
            fragment: Some(wgpu::FragmentState {
                module: &lens_shader,
                entry_point: Some("fs"),
                targets: &[Some(wgpu::ColorTargetState {
                    format,
                    blend: None,
                    write_mask: wgpu::ColorWrites::ALL,
                })],
                compilation_options: Default::default(),
            }),
            primitive: wgpu::PrimitiveState::default(),
            depth_stencil: None,
            multisample: wgpu::MultisampleState::default(),
            multiview: None,
            cache: None,
        });

        let cx = (size.width as f32 / scale_factor) * 0.5;
        let cy = (size.height as f32 / scale_factor) * 0.5;

        Self {
            surface,
            device,
            queue,
            config,
            size,
            scale_factor,
            rain_tex,
            rain_view,
            rain_pipeline,
            rain_bgl,
            rain_uniform_buf,
            rain_atlas,
            rain_time: 7.3,
            composite_pipeline,
            composite_bgl,
            frame_uniform_buf,
            panel_buf,
            panel_capacity,
            sampler,
            scene_tex,
            scene_view,
            lens_pipeline,
            lens_bgl,
            lens_uniform_buf,
            metrics: Metrics::default(),
            start: std::time::Instant::now(),
            last_frame: std::time::Instant::now(),
            text,
            lens_pos: [cx, cy],
            lens_target: [cx, cy],
            mag_level: 0.0,
            mag_level_smooth: 0.0,
            lens_radius: 0.0,
            lens_presence: 0.0,
            lens_magnify: 1.0,
            pointer_inside: false,
            tab_jelly: TabJelly::default(),
            surface_format: format,
        }
    }

    /// Track pointer for magnifier center (logical px, top-left origin).
    pub fn set_pointer(&mut self, x: f32, y: f32, inside: bool) {
        if inside && x.is_finite() && y.is_finite() {
            self.lens_target = [x, y];
            // Snap when bubble is just appearing so it grows from the cursor.
            if self.mag_level_smooth < 0.05 {
                self.lens_pos = [x, y];
            }
        }
        self.pointer_inside = inside;
    }

    /// Apply a magnifier delta (pinch or Ctrl/Cmd+scroll). Positive = embiggen.
    pub fn magnify_delta(&mut self, delta: f32) {
        if !delta.is_finite() || delta.abs() < 1e-6 {
            return;
        }
        // Pinch deltas are small (~0.01–0.1); scroll is pre-scaled by the app.
        self.mag_level = (self.mag_level + delta).clamp(0.0, MAG_LEVEL_MAX);
        // Grow from cursor when first appearing
        if self.mag_level > 0.02 && self.mag_level_smooth < 0.02 {
            self.lens_pos = self.lens_target;
        }
    }

    /// True while the bubble is visible (for skipping terminal scroll, etc.).
    pub fn magnifier_active(&self) -> bool {
        self.mag_level > 0.02 || self.mag_level_smooth > 0.02
    }

    pub fn metrics(&self) -> Metrics {
        self.metrics
    }

    pub fn scale_factor(&self) -> f32 {
        self.scale_factor
    }

    pub fn logical_size(&self) -> (f32, f32) {
        (
            self.size.width as f32 / self.scale_factor,
            self.size.height as f32 / self.scale_factor,
        )
    }

    /// Compute layout for the current surface size and tab count.
    pub fn layout(&self, tab_count: usize) -> FrameLayout {
        let (w, h) = self.logical_size();
        FrameLayout::compute(w, h, self.metrics, tab_count)
    }

    pub fn resize(&mut self, new_size: winit::dpi::PhysicalSize<u32>, scale_factor: f32) {
        if new_size.width == 0 || new_size.height == 0 {
            return;
        }
        self.size = new_size;
        self.scale_factor = scale_factor;
        self.config.width = new_size.width;
        self.config.height = new_size.height;
        self.surface.configure(&self.device, &self.config);

        let (tex, view) = create_rain_target(&self.device, self.config.width, self.config.height);
        self.rain_tex = tex;
        self.rain_view = view;
        let (st, sv) = create_scene_target(
            &self.device,
            self.surface_format,
            self.config.width,
            self.config.height,
        );
        self.scene_tex = st;
        self.scene_view = sv;
        self.text.resize(new_size, scale_factor);
    }

    pub fn render(
        &mut self,
        session: &ChromeSession,
        settings: &SettingsState,
        palette: &PaletteState,
        help: &HelpState,
        confirm: &ConfirmState,
        notes: &NotesState,
        workspace_ui: &WorkspaceUi,
        transfer: &TransferUi,
        caffeine: &Caffeine,
        commands: &[Command],
        layout: &FrameLayout,
        pty_active: bool,
        // PTY block cursor in history cells; smooth caret alpha on the ❯ line.
        terminal_cursor_visible: bool,
        input_caret_alpha: f32,
        pointer: Option<(f32, f32)>,
        chip_ui: &ChipUi,
        // Active terminal selection (focused pane only; empty is a no-op).
        term_selection: &Selection,
    ) -> Result<(), wgpu::SurfaceError> {
        let frame = self.surface.get_current_texture()?;
        let view = frame
            .texture
            .create_view(&wgpu::TextureViewDescriptor::default());

        let now = std::time::Instant::now();
        let dt = (now - self.last_frame).as_secs_f32().min(1.0 / 30.0);
        self.last_frame = now;
        let t = self.start.elapsed().as_secs_f32();
        let fw = self.size.width as f32;
        let fh = self.size.height as f32;
        let logical_w = fw / self.scale_factor;
        let logical_h = fh / self.scale_factor;

        // Pointer tracks magnifier center; bubble only from pinch / Ctrl·Cmd+scroll.
        let lens_on = settings.prefs.lens;
        match pointer {
            Some((px, py)) => self.set_pointer(px, py, true),
            None => self.set_pointer(self.lens_target[0], self.lens_target[1], false),
        }
        if !lens_on {
            self.mag_level = 0.0;
        }

        // Smooth magnifier level (bubble grow / shrink)
        let k_mag = 1.0 - (-dt * 14.0).exp();
        self.mag_level_smooth += (self.mag_level - self.mag_level_smooth) * k_mag;
        if self.mag_level_smooth < 0.001 && self.mag_level < 0.001 {
            self.mag_level_smooth = 0.0;
        }

        // Bubble radius: ease-out from 0 so it grows out of the cursor.
        // level 0 → 0; level 1 → MAG_RADIUS_BASE; higher → larger.
        let lv = self.mag_level_smooth;
        let t_r = (lv / 1.15).clamp(0.0, 1.0);
        let ease = 1.0 - (1.0 - t_r).powi(3); // ease-out cubic
        let extra = ((lv - 1.15).max(0.0) * 55.0).min(160.0);
        self.lens_radius = ease * MAG_RADIUS_BASE + extra;

        // Presence: fully on once the bubble has a bit of size; fades when shrinking.
        self.lens_presence = ((lv - 0.02) / 0.14).clamp(0.0, 1.0);

        // Magnification factor grows with level (1× hidden → ~2.2× at 1 → ~4× at max).
        self.lens_magnify = 1.0 + lv * 1.15;

        // Follow cursor only while the bubble is alive
        if self.lens_presence > 0.01 {
            let k_pos = 1.0 - (-dt * (4.0 + LENS_FOLLOW * 26.0)).exp();
            self.lens_pos[0] += (self.lens_target[0] - self.lens_pos[0]) * k_pos;
            self.lens_pos[1] += (self.lens_target[1] - self.lens_pos[1]) * k_pos;
        } else {
            self.lens_pos = self.lens_target;
        }

        if settings.prefs.rain {
            self.rain_time += dt;
        }
        self.queue.write_buffer(
            &self.rain_uniform_buf,
            0,
            bytemuck::bytes_of(&RainUniforms {
                res_time: [fw, fh, self.rain_time, 0.0],
                params: [
                    rain_sim::CELL * self.scale_factor, // cell in framebuffer px (Canvas UI * dpr)
                    rain_sim::SPEED,
                    rain_sim::SPEED_VARIANCE,
                    rain_sim::DENSITY,
                ],
                params2: [
                    rain_sim::TRAIL,
                    rain_sim::GLOW,
                    rain_sim::MUTATE,
                    rain_sim::FLICKER,
                ],
                params3: [
                    rain_sim::LAYERS,
                    self.rain_atlas.count,
                    self.rain_atlas.grid,
                    0.0,
                ],
                color: {
                    // Theme jade body; brighter head from same accent.
                    let j = settings.prefs.theme_colors().jade;
                    [j[0], j[1], j[2], 1.0]
                },
                head_color: {
                    let j = settings.prefs.theme_colors().jade;
                    [
                        (j[0] + 0.15).min(1.0),
                        (j[1] + 0.12).min(1.0),
                        (j[2] + 0.10).min(1.0),
                        1.0,
                    ]
                },
            }),
        );
        let _ = t; // wall-clock still used by composite glass time

        let active_idx = session
            .tabs
            .iter()
            .position(|tab| tab.id == session.active_id)
            .unwrap_or(0);
        let lights = if is_mac() {
            Some(traffic_light_rects(&self.metrics))
        } else {
            None
        };
        // Tick jelly toward active tab (also ticked in app; extra settle is fine)
        let active_chip = layout.tab_chips.get(active_idx).copied();
        self.tab_jelly.tick(dt, active_chip);

        let mut panels = layout.glass_panels(
            self.metrics,
            active_idx,
            lights,
            chip_ui,
            &self.tab_jelly,
        );
        // Modal overlays (settings / palette / help / notes)
        {
            use crate::layout::{PanelInstance, PanelKind, Rect};
            let any_overlay = settings.visible()
                || palette.visible()
                || help.visible()
                || confirm.visible()
                || notes.visible()
                || workspace_ui.visible()
                || transfer.visible();
            if any_overlay {
                let scrim_a = settings
                    .scrim_alpha()
                    .max(palette.scrim_alpha())
                    .max(help.scrim_alpha())
                    .max(confirm.scrim_alpha())
                    .max(notes.scrim_alpha())
                    .max(workspace_ui.scrim_alpha())
                    .max(transfer.scrim_alpha());
                panels.push(
                    PanelInstance::glass(
                        Rect::new(0.0, 0.0, logical_w, logical_h),
                        0.0,
                        PanelKind::Scrim,
                    )
                    .with_opacity(scrim_a),
                );
            }
            push_modal_glass(
                &mut panels,
                self.metrics,
                logical_w,
                logical_h,
                settings,
                palette,
                help,
                confirm,
                notes,
                workspace_ui,
                transfer,
                commands,
            );
        }
        let count = panels.len() as u32;

        let need = (panels.len() as u64) * std::mem::size_of::<PanelInstance>() as u64;
        if need > self.panel_capacity * std::mem::size_of::<PanelInstance>() as u64 {
            self.panel_capacity = panels.len() as u64 + 4;
            self.panel_buf = self.device.create_buffer(&wgpu::BufferDescriptor {
                label: Some("panels"),
                size: self.panel_capacity * std::mem::size_of::<PanelInstance>() as u64,
                usage: wgpu::BufferUsages::STORAGE | wgpu::BufferUsages::COPY_DST,
                mapped_at_creation: false,
            });
        }
        self.queue
            .write_buffer(&self.panel_buf, 0, bytemuck::cast_slice(&panels));

        self.queue.write_buffer(
            &self.frame_uniform_buf,
            0,
            bytemuck::bytes_of(&FrameUniforms {
                size: [logical_w, logical_h, fw, fh],
                misc: [
                    t,
                    self.scale_factor,
                    count as f32,
                    settings.prefs.glass_darken.clamp(0.0, 0.95),
                ],
                // Same glass constants as the cursor lens (panes match lens look)
                glass: [GLASS_IOR, GLASS_EDGE, GLASS_BEVEL, GLASS_DEPTH],
                glass2: [
                    GLASS_ABERRATION,
                    GLASS_BLUR,
                    GLASS_REFLECTION,
                    GLASS_SHINE.max(0.1),
                ],
                // Spotlight: animated strength (fades out after leaving chips).
                // xy = last chip cursor, z = radius, w = strength 0..1
                hover: {
                    let p = chip_ui.spotlight_pos();
                    let s = chip_ui.spotlight();
                    // Keep feeding last pos while fading even if pointer is None.
                    let _ = pointer;
                    [p[0], p[1], 32.0, s]
                },
            }),
        );

        self.queue.write_buffer(
            &self.lens_uniform_buf,
            0,
            bytemuck::bytes_of(&LensUniforms {
                size: [logical_w, logical_h, fw, fh],
                lens: [
                    self.lens_pos[0],
                    self.lens_pos[1],
                    self.lens_radius,
                    self.lens_presence,
                ],
                glass: [GLASS_IOR, GLASS_EDGE, GLASS_BEVEL, GLASS_DEPTH],
                glass2: [
                    GLASS_ABERRATION,
                    self.lens_magnify, // magnify factor (was blur; lens uses mag)
                    GLASS_REFLECTION,
                    GLASS_SHINE.max(0.1),
                ],
            }),
        );

        let composite_bind = self.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("composite bg"),
            layout: &self.composite_bgl,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: self.frame_uniform_buf.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::TextureView(&self.rain_view),
                },
                wgpu::BindGroupEntry {
                    binding: 2,
                    resource: wgpu::BindingResource::Sampler(&self.sampler),
                },
                wgpu::BindGroupEntry {
                    binding: 3,
                    resource: self.panel_buf.as_entire_binding(),
                },
            ],
        });

        let mut encoder = self
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("frame"),
            });

        // Pass 0: Canvas UI GlyphRain (per-fragment continuous phase reveal) → rain RT
        let rain_bind = self.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("rain bg"),
            layout: &self.rain_bgl,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: self.rain_uniform_buf.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::TextureView(&self.rain_atlas.view),
                },
                wgpu::BindGroupEntry {
                    binding: 2,
                    resource: wgpu::BindingResource::Sampler(&self.sampler),
                },
            ],
        });
        {
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("rain"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &self.rain_view,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color::BLACK),
                        store: wgpu::StoreOp::Store,
                    },
                })],
                depth_stencil_attachment: None,
                timestamp_writes: None,
                occlusion_query_set: None,
            });
            if settings.prefs.rain {
                pass.set_pipeline(&self.rain_pipeline);
                pass.set_bind_group(0, &rain_bind, &[]);
                pass.draw(0..3, 0..1);
            }
        }

        // Pass 1: composite glass chrome → scene RT (not swapchain)
        {
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("composite"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &self.scene_view,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color::BLACK),
                        store: wgpu::StoreOp::Store,
                    },
                })],
                depth_stencil_attachment: None,
                timestamp_writes: None,
                occlusion_query_set: None,
            });
            pass.set_pipeline(&self.composite_pipeline);
            pass.set_bind_group(0, &composite_bind, &[]);
            pass.draw(0..3, 0..1);
        }

        // Pass 2: text overlay onto scene RT
        let labels = chrome_labels(
            layout,
            self.metrics,
            session,
            active_idx,
            settings,
            palette,
            help,
            confirm,
            notes,
            workspace_ui,
            transfer,
            caffeine,
            commands,
            pty_active,
            terminal_cursor_visible,
            input_caret_alpha,
            chip_ui,
            &self.tab_jelly,
            term_selection,
        );
        self.text.prepare(&self.device, &self.queue, &labels);
        self.text
            .render(&self.device, &mut encoder, &self.scene_view);

        // Pass 3: cursor glass lens samples full scene → swapchain
        let lens_bind = self.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("lens bg"),
            layout: &self.lens_bgl,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: self.lens_uniform_buf.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::TextureView(&self.scene_view),
                },
                wgpu::BindGroupEntry {
                    binding: 2,
                    resource: wgpu::BindingResource::Sampler(&self.sampler),
                },
            ],
        });
        {
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("lens"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &view,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        // Transparent clear — macOS rounded corners need alpha 0 outside.
                        load: wgpu::LoadOp::Clear(wgpu::Color {
                            r: 0.0,
                            g: 0.0,
                            b: 0.0,
                            a: 0.0,
                        }),
                        store: wgpu::StoreOp::Store,
                    },
                })],
                depth_stencil_attachment: None,
                timestamp_writes: None,
                occlusion_query_set: None,
            });
            pass.set_pipeline(&self.lens_pipeline);
            pass.set_bind_group(0, &lens_bind, &[]);
            pass.draw(0..3, 0..1);
        }

        self.queue.submit(Some(encoder.finish()));
        frame.present();
        self.text.trim_atlas();
        Ok(())
    }
}

/// Derive grid cols/rows from the **cells** rect (PTY hole inside the single glass).
/// `cells` is already inset — no extra pad.
pub fn terminal_grid_size(cells: &crate::layout::Rect, _inset: f32) -> (u16, u16) {
    let inner_w = cells.w.max(CELL_W);
    let inner_h = cells.h.max(CELL_H);
    let cols = (inner_w / CELL_W).floor().max(1.0) as u16;
    let rows = (inner_h / CELL_H).floor().max(1.0) as u16;
    (cols, rows)
}

pub fn overlay_modal_rect_pub(
    window_w: f32,
    window_h: f32,
    max_w: f32,
    max_h: f32,
) -> crate::layout::Rect {
    overlay_modal_rect(window_w, window_h, max_w, max_h)
}

fn overlay_modal_rect(window_w: f32, window_h: f32, max_w: f32, max_h: f32) -> crate::layout::Rect {
    let w = (window_w - 32.0).min(max_w).max(280.0);
    let h = (window_h - 64.0).min(max_h).max(220.0);
    crate::layout::Rect::new((window_w - w) * 0.5, (window_h - h) * 0.42, w, h)
}

/// Chrome + terminal strings for the current frame (logical px).
fn chrome_labels(
    layout: &FrameLayout,
    m: Metrics,
    session: &ChromeSession,
    active_idx: usize,
    settings: &SettingsState,
    palette: &PaletteState,
    help: &HelpState,
    confirm: &ConfirmState,
    notes: &NotesState,
    workspace_ui: &WorkspaceUi,
    transfer: &TransferUi,
    caffeine: &Caffeine,
    commands: &[Command],
    pty_active: bool,
    terminal_cursor_visible: bool,
    input_caret_alpha: f32,
    chip_ui: &ChipUi,
    tab_jelly: &TabJelly,
    term_selection: &Selection,
) -> Vec<TextLabel> {
    use crate::chrome_ui::{scale_rect, ChipId};
    // Chrome label colors track prefs theme (see SETTINGS_HOOKS.md / theme.rs).
    let pal = settings.prefs.theme_colors();
    let muted = [pal.muted[0], pal.muted[1], pal.muted[2], 0.85];
    let bright = [pal.fg[0], pal.fg[1], pal.fg[2], 0.95];
    let dim = [
        pal.muted[0] * 0.85,
        pal.muted[1] * 0.85,
        pal.muted[2] * 0.85,
        0.75,
    ];
    // Warm accent when caffeine is on (product styleCaffeineOn).
    let cafe_on = [1.0, 0.82, 0.45, 0.95];
    let cafe_off = [0.45, 0.55, 0.48, 0.70];
    let selection_rgb = pal.jade;

    let mut labels = Vec::with_capacity(128);
    let _ = tab_jelly; // labels no longer follow jelly

    // No window title — chrome bar is tabs + logo only.
    // Gohu is bitmap-rooted @14 — prefer design size so chrome matches the host.
    let tab_size = 14.0;
    for (i, chip) in layout.tab_chips.iter().enumerate() {
        let title = session
            .tabs
            .get(i)
            .map(|t| t.title.as_str())
            .unwrap_or("?");
        let color = if i == active_idx { bright } else { dim };
        // Labels always sit on the layout chip — never follow the jelly goo
        // (that made the clicked tab's title jump into the previous active tab).
        let r = scale_rect(*chip, chip_ui.scale_for(ChipId::Tab(i)));
        labels.push(TextLabel::centered(
            title,
            [r.x, r.y, r.w, r.h],
            tab_size,
            color,
        ));
    }

    // Ghost + : icon always full size; only the glass shell is press-only (layout).
    {
        let id = ChipId::NewTab;
        let on = chip_ui.hover == Some(id);
        let r = scale_rect(layout.tab_new, chip_ui.scale_for(id));
        // Keep glyph size at 16 even though the hit shell is smaller (24).
        let press = chip_ui.press_light(id);
        let color = if press > 0.05 {
            bright
        } else if on {
            [0.75, 0.95, 0.82, 0.95]
        } else {
            dim
        };
        labels.push(TextLabel::centered("+", [r.x, r.y, r.w, r.h], 14.0, color));
    }

    // Caffeine ☕ — fully centered in the chip (hint only as short overlay text if timed).
    {
        let r = scale_rect(layout.caffeine, chip_ui.scale_for(ChipId::Caffeine));
        let on = caffeine.active();
        let color = if on { cafe_on } else { cafe_off };
        let h = if on { caffeine.hint() } else { String::new() };
        // Timed: show cup+hint as one centered label; indefinite/off: cup alone.
        if on && !h.is_empty() && h != "∞" {
            labels.push(TextLabel::symbol_centered(
                format!("☕{h}"),
                [r.x, r.y, r.w, r.h],
                16.0,
                color,
            ));
        } else {
            labels.push(TextLabel::symbol_centered(
                "☕",
                [r.x, r.y, r.w, r.h],
                20.0,
                color,
            ));
        }
    }

    // Logo glass button (top-right) — opens settings.
    // 硯 is CJK — Gohu will tofu; glyphon falls through to a system CJK face when
    // the primary face has no glyph (cosmic-text fallback). Size still 14 for grid.
    {
        let r = scale_rect(layout.logo, chip_ui.scale_for(ChipId::Logo));
        labels.push(TextLabel::centered(
            "硯",
            [r.x, r.y, r.w, r.h],
            14.0,
            bright,
        ));
    }

    // Every leaf pane: cells + footer
    let focus = session.focus_pane_id();
    for pl in &layout.panes {
        let Some(pane) = session.panes.get(&pl.pane_id) else {
            continue;
        };
        let show_cursor = terminal_cursor_visible && pl.pane_id == focus;
        // Selection is one global model for the focused leaf; other panes get none.
        let pane_sel = if pl.pane_id == focus {
            Some(term_selection)
        } else {
            None
        };
        push_pane_cells(
            &mut labels,
            pl,
            pane,
            show_cursor,
            pane_sel,
            selection_rgb,
        );

        // Footer is UI chrome (not terminal grid) — no ASCII dashes, no cell overflow.
        // Hairline is drawn as a glass panel; path + input are normal UI labels.
        let path_size = 12.0;
        let input_size = 13.0;
        let char_w = 7.5_f32; // approx Gohu @12–13

        let path_str = crate::session::display_path(&pane.cwd);
        if !path_str.is_empty() {
            // Never drop a lone `~` — always reserve room for at least that glyph.
            let max_c = ((pl.path.w / char_w).floor() as usize).max(1);
            let path_draw = truncate_chars(&path_str, max_c);
            // Left-align path like product chrome (not centered — `~` was easy to miss).
            let py = pl.path.y + (pl.path.h - path_size).max(0.0) * 0.5;
            labels.push(TextLabel::new(
                path_draw,
                pl.path.x,
                py,
                path_size,
                dim,
            ));
        }

        let draft = pane.draft.as_str();
        let max_c = ((pl.warp.w / char_w).floor() as usize).max(4);
        let warp_text = truncate_chars(&format!("❯ {draft}"), max_c);
        let input_bright = if pl.focused { bright } else { dim };
        // Left-align inside warp band (not full-center — reads as an input line).
        let iy = pl.warp.y + (pl.warp.h - input_size).max(0.0) * 0.5;
        labels.push(TextLabel::new(
            warp_text,
            pl.warp.x,
            iy,
            input_size,
            input_bright,
        ));

        if pl.focused {
            let a = input_caret_alpha.clamp(0.0, 1.0);
            if a > 0.02 {
                let caret_cols = (2 + draft.chars().count()).min(max_c.saturating_sub(1));
                let caret_x = pl.warp.x + caret_cols as f32 * char_w;
                labels.push(TextLabel::new(
                    CARET_BLOCK,
                    caret_x,
                    iy,
                    input_size,
                    [CARET_RGB[0], CARET_RGB[1], CARET_RGB[2], a],
                ));
            }
        }
    }

    let win_w = layout.title.w;
    let win_h = layout.workspace.y + layout.workspace.h + m.edge();

    // --- Glass modals: real UI labels (not ASCII terminal dumps) ---
    push_modal_labels(
        &mut labels,
        m,
        win_w,
        win_h,
        session,
        settings,
        palette,
        help,
        confirm,
        notes,
        workspace_ui,
        transfer,
        commands,
        pty_active,
        input_caret_alpha,
        bright,
        muted,
        dim,
    );

    labels
}

/// Glass chrome for overlays: outer shell, nested frost fields, option buttons.
fn push_modal_glass(
    panels: &mut Vec<crate::layout::PanelInstance>,
    m: Metrics,
    win_w: f32,
    win_h: f32,
    settings: &SettingsState,
    palette: &PaletteState,
    help: &HelpState,
    confirm: &ConfirmState,
    notes: &NotesState,
    workspace_ui: &WorkspaceUi,
    transfer: &TransferUi,
    commands: &[Command],
) {
    use crate::layout::{PanelInstance, PanelKind, Rect};

    if settings.visible() {
        let ease = settings.content_ease().clamp(0.0, 1.0);
        let modal = settings.animated_modal_rect(win_w, win_h);
        panels.push(PanelInstance::glass(modal, m.radius, PanelKind::Modal).with_opacity(ease));
        let pad = 16.0;
        let row_h = 40.0;
        let gap = 8.0;
        let mut y = modal.y + 48.0;
        for _ in 0..3 {
            let r = Rect::new(modal.x + pad, y, modal.w - pad * 2.0, row_h);
            panels.push(
                PanelInstance::glass(r, m.chip_radius, PanelKind::ModalButton).with_opacity(ease),
            );
            y += row_h + gap;
        }
    }

    if palette.visible() {
        let ease = palette.content_ease().clamp(0.0, 1.0);
        let modal = palette.modal_rect(win_w, win_h);
        panels.push(PanelInstance::glass(modal, m.radius, PanelKind::Modal).with_opacity(ease));
        let pad = 14.0;
        let input_h = 48.0;
        let input = Rect::new(
            modal.x + pad,
            modal.y + pad + 4.0,
            modal.w - pad * 2.0,
            input_h,
        );
        panels.push(
            PanelInstance::glass(input, m.chip_radius + 2.0, PanelKind::ModalFrost)
                .with_opacity(ease),
        );
        let filtered = filter_commands(commands, &palette.query);
        let btn_h = 36.0;
        let gap = 6.0;
        let mut y = input.y + input.h + 12.0;
        let max_y = modal.y + modal.h - pad;
        for (i, _) in filtered.iter().enumerate().take(6) {
            if y + btn_h > max_y {
                break;
            }
            let r = Rect::new(modal.x + pad, y, modal.w - pad * 2.0, btn_h);
            let kind = if i == palette.selected {
                PanelKind::ModalButtonActive
            } else {
                PanelKind::ModalButton
            };
            panels.push(PanelInstance::glass(r, m.chip_radius, kind).with_opacity(ease));
            y += btn_h + gap;
        }
    }

    if help.visible() {
        let ease = help.content_ease().clamp(0.0, 1.0);
        let modal = overlay_modal_rect(win_w, win_h, 640.0, 360.0);
        panels.push(PanelInstance::glass(modal, m.radius, PanelKind::Modal).with_opacity(ease));
        let pad = 14.0;
        let btn_h = 32.0;
        let gap = 5.0;
        let mut y = modal.y + 44.0;
        let max_y = modal.y + modal.h - pad;
        for _ in commands.iter().take(8) {
            if y + btn_h > max_y {
                break;
            }
            let r = Rect::new(modal.x + pad, y, modal.w - pad * 2.0, btn_h);
            panels.push(
                PanelInstance::glass(r, m.chip_radius, PanelKind::ModalButton).with_opacity(ease),
            );
            y += btn_h + gap;
        }
    }

    if confirm.visible() {
        let ease = confirm.content_ease().clamp(0.0, 1.0);
        let modal = confirm.animated_modal_rect(win_w, win_h);
        panels.push(PanelInstance::glass(modal, m.radius, PanelKind::Modal).with_opacity(ease));
        let yes = confirm.yes_rect(win_w, win_h);
        let no = confirm.no_rect(win_w, win_h);
        panels.push(
            PanelInstance::glass(yes, m.chip_radius, PanelKind::ModalButtonActive)
                .with_opacity(ease),
        );
        panels.push(
            PanelInstance::glass(no, m.chip_radius, PanelKind::ModalButton).with_opacity(ease),
        );
    }

    if notes.visible() {
        let ease = notes.content_ease().clamp(0.0, 1.0);
        let lay = notes.layout(win_w, win_h);
        panels.push(
            PanelInstance::glass(lay.modal, m.radius, PanelKind::Modal).with_opacity(ease),
        );
        panels.push(
            PanelInstance::glass(lay.list, m.chip_radius, PanelKind::ModalFrost).with_opacity(ease),
        );
        panels.push(
            PanelInstance::glass(lay.title, m.chip_radius, PanelKind::ModalFrost)
                .with_opacity(ease),
        );
        panels.push(
            PanelInstance::glass(lay.body, m.chip_radius + 2.0, PanelKind::ModalFrost)
                .with_opacity(ease),
        );
    }

    if workspace_ui.visible() {
        let ease = workspace_ui.content_ease().clamp(0.0, 1.0);
        let modal = workspace_ui.animated_modal_rect(win_w, win_h);
        panels.push(PanelInstance::glass(modal, m.radius, PanelKind::Modal).with_opacity(ease));
        let pad = 14.0;
        let list_w = 140.0;
        let ch = Rect::new(modal.x + pad, modal.y + pad, list_w, modal.h - pad * 2.0 - 52.0);
        panels.push(
            PanelInstance::glass(ch, m.chip_radius, PanelKind::ModalFrost).with_opacity(ease),
        );
        let msg = Rect::new(
            ch.x + ch.w + 10.0,
            modal.y + pad,
            modal.x + modal.w - pad - (ch.x + ch.w + 10.0),
            ch.h,
        );
        panels.push(
            PanelInstance::glass(msg, m.chip_radius, PanelKind::ModalFrost).with_opacity(ease),
        );
        let input = Rect::new(
            modal.x + pad,
            modal.y + modal.h - pad - 44.0,
            modal.w - pad * 2.0,
            44.0,
        );
        panels.push(
            PanelInstance::glass(input, m.chip_radius + 2.0, PanelKind::ModalFrost)
                .with_opacity(ease),
        );
    }

    if transfer.visible() {
        let ease = transfer.content_ease().clamp(0.0, 1.0);
        let modal = transfer.animated_modal_rect(win_w, win_h);
        panels.push(PanelInstance::glass(modal, m.radius, PanelKind::Modal).with_opacity(ease));
        let pad = 16.0;
        // Drop zone (send only, idle) sits above the path field.
        let mut y = modal.y + 56.0;
        if transfer.mode == crate::transfer_ui::TransferMode::Send && !transfer.is_running() {
            let drop_h = 32.0;
            let drop_zone = Rect::new(modal.x + pad, y, modal.w - pad * 2.0, drop_h);
            let kind = if transfer.drop_hover {
                PanelKind::ModalButton
            } else {
                PanelKind::ModalFrost
            };
            panels.push(PanelInstance::glass(drop_zone, m.chip_radius, kind).with_opacity(ease));
            y += drop_h + 10.0;
        }
        let input = Rect::new(modal.x + pad, y, modal.w - pad * 2.0, 48.0);
        panels.push(
            PanelInstance::glass(input, m.chip_radius + 2.0, PanelKind::ModalFrost)
                .with_opacity(ease),
        );
        // Copy-ticket chip when a ticket is shareable.
        if !transfer.ticket.is_empty() {
            let chip_w = 120.0_f32.min(modal.w - pad * 2.0);
            let chip = Rect::new(
                modal.x + (modal.w - chip_w) * 0.5,
                modal.y + modal.h - 52.0,
                chip_w,
                28.0,
            );
            panels.push(
                PanelInstance::glass(chip, m.chip_radius, PanelKind::ModalButton)
                    .with_opacity(ease),
            );
        }
    }
}

fn push_modal_labels(
    labels: &mut Vec<TextLabel>,
    m: Metrics,
    win_w: f32,
    win_h: f32,
    session: &ChromeSession,
    settings: &SettingsState,
    palette: &PaletteState,
    help: &HelpState,
    confirm: &ConfirmState,
    notes: &NotesState,
    workspace_ui: &WorkspaceUi,
    transfer: &TransferUi,
    commands: &[Command],
    pty_active: bool,
    caret_alpha: f32,
    bright: [f32; 4],
    muted: [f32; 4],
    dim: [f32; 4],
) {
    let pad = 16.0;
    let caret_a = caret_alpha.clamp(0.0, 1.0);

    if settings.visible() {
        let ease = settings.content_ease().clamp(0.0, 1.0);
        let modal = settings.animated_modal_rect(win_w, win_h);
        let mut title_c = bright;
        title_c[3] *= ease;
        labels.push(TextLabel::new(
            "Settings",
            modal.x + pad,
            modal.y + 14.0,
            15.0,
            title_c,
        ));
        let grid = session.active_grid();
        let theme_val = crate::theme::label(&settings.prefs.theme).to_string();
        let darken_val = format!("{:.0}%", settings.prefs.glass_darken * 100.0);
        let rows = [
            (
                "Glyph rain",
                if settings.prefs.rain { "On" } else { "Off" },
                "1",
            ),
            (
                "Magnifier",
                if settings.prefs.lens { "On" } else { "Off" },
                "2",
            ),
            ("Theme", theme_val.as_str(), "3"),
            ("Glass darken", darken_val.as_str(), "[ ]"),
        ];
        let row_h = 36.0;
        let gap = 6.0;
        let mut y = modal.y + 48.0;
        for (title, val, key) in rows {
            let mut tc = bright;
            tc[3] *= ease;
            let mut vc = muted;
            vc[3] *= ease;
            labels.push(TextLabel::new(
                format!("{title}   ·  {key}"),
                modal.x + pad + 12.0,
                y + 10.0,
                13.0,
                tc,
            ));
            labels.push(TextLabel::new(
                val.to_string(),
                modal.x + modal.w - pad - 72.0,
                y + 10.0,
                13.0,
                vc,
            ));
            y += row_h + gap;
        }
        let mut foot = dim;
        foot[3] *= ease * 0.9;
        let shell = if pty_active { "PTY live" } else { "mock shell" };
        labels.push(TextLabel::new(
            format!(
                "{shell}  ·  {}×{}  ·  {} tabs",
                grid.cols(),
                grid.rows(),
                session.tabs.len()
            ),
            modal.x + pad,
            modal.y + modal.h - 28.0,
            11.0,
            foot,
        ));
    }

    if palette.visible() {
        let ease = palette.content_ease().clamp(0.0, 1.0);
        let modal = palette.modal_rect(win_w, win_h);
        let pad = 14.0;
        let input_h = 48.0;
        let input = crate::layout::Rect::new(
            modal.x + pad,
            modal.y + pad + 4.0,
            modal.w - pad * 2.0,
            input_h,
        );
        let ty = input.y + (input.h - 15.0) * 0.5;
        let mut qc = if palette.query.is_empty() { dim } else { bright };
        qc[3] *= ease;
        let display = if palette.query.is_empty() {
            "Type a command…"
        } else {
            palette.query.as_str()
        };
        labels.push(TextLabel::new(
            display.to_string(),
            input.x + 14.0,
            ty,
            15.0,
            qc,
        ));
        // Caret after query (not on placeholder)
        if !palette.query.is_empty() || caret_a > 0.02 {
            let approx = 8.2_f32;
            let caret_x = input.x + 14.0 + palette.query.chars().count() as f32 * approx;
            labels.push(TextLabel::new(
                CARET_BLOCK,
                caret_x.min(input.x + input.w - 20.0),
                ty,
                15.0,
                [CARET_RGB[0], CARET_RGB[1], CARET_RGB[2], caret_a * ease],
            ));
        }

        let filtered = filter_commands(commands, &palette.query);
        let btn_h = 36.0;
        let gap = 6.0;
        let mut y = input.y + input.h + 12.0;
        let max_y = modal.y + modal.h - pad;
        for (i, &idx) in filtered.iter().enumerate().take(6) {
            if y + btn_h > max_y {
                break;
            }
            let c = &commands[idx];
            let mut tc = if i == palette.selected { bright } else { muted };
            tc[3] *= ease;
            let mut dc = dim;
            dc[3] *= ease;
            labels.push(TextLabel::new(
                c.title.to_string(),
                modal.x + pad + 14.0,
                y + 8.0,
                13.0,
                tc,
            ));
            let desc = truncate_chars(&c.desc, 42);
            labels.push(TextLabel::new(
                desc,
                modal.x + pad + 14.0,
                y + 22.0,
                10.0,
                dc,
            ));
            y += btn_h + gap;
        }
        if filtered.is_empty() {
            let mut nc = dim;
            nc[3] *= ease;
            labels.push(TextLabel::new(
                "No matches",
                modal.x + pad + 14.0,
                y + 8.0,
                13.0,
                nc,
            ));
        }
    }

    if help.visible() {
        let ease = help.content_ease().clamp(0.0, 1.0);
        let modal = overlay_modal_rect(win_w, win_h, 640.0, 360.0);
        let mut title_c = bright;
        title_c[3] *= ease;
        labels.push(TextLabel::new(
            "Keyboard shortcuts",
            modal.x + 16.0,
            modal.y + 14.0,
            15.0,
            title_c,
        ));
        let pad = 14.0;
        let btn_h = 32.0;
        let gap = 5.0;
        let mut y = modal.y + 44.0;
        let max_y = modal.y + modal.h - pad;
        for c in commands.iter().take(8) {
            if y + btn_h > max_y {
                break;
            }
            let mut tc = bright;
            tc[3] *= ease;
            let mut dc = dim;
            dc[3] *= ease;
            labels.push(TextLabel::new(
                c.title.to_string(),
                modal.x + pad + 12.0,
                y + 8.0,
                12.0,
                tc,
            ));
            labels.push(TextLabel::new(
                c.desc.clone(),
                modal.x + modal.w * 0.42,
                y + 8.0,
                11.0,
                dc,
            ));
            y += btn_h + gap;
        }
    }

    if confirm.visible() {
        let ease = confirm.content_ease().clamp(0.0, 1.0);
        let modal = confirm.animated_modal_rect(win_w, win_h);
        let pad = 16.0;
        let mut title_c = bright;
        title_c[3] *= ease;
        labels.push(TextLabel::new(
            confirm.title.clone(),
            modal.x + pad,
            modal.y + 16.0,
            15.0,
            title_c,
        ));
        let mut body_c = muted;
        body_c[3] *= ease;
        labels.push(TextLabel::new(
            confirm.body.clone(),
            modal.x + pad,
            modal.y + 48.0,
            13.0,
            body_c,
        ));
        // Product-style hints under the body (enter / esc).
        let mut hint_c = dim;
        hint_c[3] *= ease * 0.95;
        labels.push(TextLabel::new(
            format!("{}  enter    {}  esc", confirm.yes_label, confirm.no_label),
            modal.x + pad,
            modal.y + 78.0,
            11.0,
            hint_c,
        ));
        let yes = confirm.yes_rect(win_w, win_h);
        let no = confirm.no_rect(win_w, win_h);
        let mut yc = bright;
        yc[3] *= ease;
        let mut nc = muted;
        nc[3] *= ease;
        labels.push(TextLabel::centered(
            confirm.yes_label.clone(),
            [yes.x, yes.y, yes.w, yes.h],
            13.0,
            yc,
        ));
        labels.push(TextLabel::centered(
            confirm.no_label.clone(),
            [no.x, no.y, no.w, no.h],
            13.0,
            nc,
        ));
    }

    if notes.visible() {
        let ease = notes.content_ease().clamp(0.0, 1.0);
        // Shared geometry with NotesState::layout / try_click (see NOTES_HOOKS.md).
        let lay = notes.layout(win_w, win_h);
        let list = lay.list;
        let row_h = crate::notes::NOTES_ROW_H;
        for (i, r) in lay.list_rows.iter().enumerate() {
            let mut tc = if i == notes.active_index() {
                bright
            } else {
                muted
            };
            tc[3] *= ease;
            let title = notes.display_title_for(i);
            labels.push(TextLabel::new(
                truncate_chars(&title, 18),
                list.x + 10.0,
                r.y + 8.0,
                12.0,
                tc,
            ));
        }
        let mut nc = dim;
        nc[3] *= ease;
        labels.push(TextLabel::new(
            "+ New note",
            list.x + 10.0,
            lay.new_row.y + 8.0,
            12.0,
            nc,
        ));
        let mut dc = dim;
        dc[3] *= ease * 0.9;
        let del_label = if notes.bank().len() <= 1 {
            "Clear note"
        } else {
            "Delete note"
        };
        labels.push(TextLabel::new(
            del_label.to_string(),
            list.x + 10.0,
            lay.delete_row.y + 8.0,
            11.0,
            dc,
        ));

        let title_r = lay.title;
        let mut tc = bright;
        tc[3] *= ease;
        let title_text = if notes.title.is_empty() {
            if notes.focus == crate::notes::NotesFocus::Title {
                String::new()
            } else {
                "Title".into()
            }
        } else {
            notes.title.clone()
        };
        let title_color = if notes.title.is_empty() && notes.focus != crate::notes::NotesFocus::Title
        {
            let mut c = dim;
            c[3] *= ease;
            c
        } else {
            tc
        };
        labels.push(TextLabel::new(
            title_text,
            title_r.x + 12.0,
            title_r.y + 10.0,
            14.0,
            title_color,
        ));
        if notes.focus == crate::notes::NotesFocus::Title {
            let caret_x = title_r.x + 12.0 + notes.cursor as f32 * 8.0;
            labels.push(TextLabel::new(
                CARET_BLOCK,
                caret_x.min(title_r.x + title_r.w - 16.0),
                title_r.y + 10.0,
                14.0,
                [CARET_RGB[0], CARET_RGB[1], CARET_RGB[2], caret_a * ease],
            ));
        }
        let body = lay.body;
        let mut bc = muted;
        bc[3] *= ease;
        let mut by = body.y + 12.0;
        if notes.body.is_empty() {
            labels.push(TextLabel::new(
                "Start writing…",
                body.x + 14.0,
                by,
                13.0,
                bc,
            ));
            if notes.focus == crate::notes::NotesFocus::Body {
                labels.push(TextLabel::new(
                    CARET_BLOCK,
                    body.x + 14.0,
                    by,
                    13.0,
                    [CARET_RGB[0], CARET_RGB[1], CARET_RGB[2], caret_a * ease],
                ));
            }
        } else {
            for line in notes.body.lines() {
                if by > body.y + body.h - 16.0 {
                    break;
                }
                labels.push(TextLabel::new(
                    line.to_string(),
                    body.x + 14.0,
                    by,
                    13.0,
                    bc,
                ));
                by += 18.0;
            }
            if notes.focus == crate::notes::NotesFocus::Body {
                let last = notes.body.lines().last().unwrap_or("");
                let caret_x = body.x + 14.0 + last.chars().count() as f32 * 7.5;
                labels.push(TextLabel::new(
                    CARET_BLOCK,
                    caret_x.min(body.x + body.w - 16.0),
                    (by - 18.0).max(body.y + 12.0),
                    13.0,
                    [CARET_RGB[0], CARET_RGB[1], CARET_RGB[2], caret_a * ease],
                ));
            }
        }
        let mut foot = dim;
        foot[3] *= ease;
        let status = if notes.is_dirty() { "unsaved" } else { "saved" };
        labels.push(TextLabel::new(
            format!(
                "{} notes · {} chars · {status} · Esc saves",
                notes.bank().len(),
                notes.body.chars().count()
            ),
            lay.title.x,
            lay.modal.y + lay.modal.h - 22.0,
            11.0,
            foot,
        ));
        let _ = row_h; // layout constant available for future row chrome
    }

    if workspace_ui.visible() {
        use crate::workspace_ui::{
            ComposeMode, CHANNEL_LIST_TOP, CHANNEL_LIST_W, CHANNEL_ROW_H, COMPOSE_H, MODAL_PAD,
            PRESENCE_STRIP_H,
        };
        let ease = workspace_ui.content_ease().clamp(0.0, 1.0);
        let modal = workspace_ui.animated_modal_rect(win_w, win_h);
        let pad = MODAL_PAD;
        let list_w = CHANNEL_LIST_W;
        let mut title_c = bright;
        title_c[3] *= ease;
        labels.push(TextLabel::new(
            "Workspace",
            modal.x + pad,
            modal.y + 8.0,
            13.0,
            title_c,
        ));
        // Presence strip (members) — right of title / above messages.
        {
            let strip = workspace_ui.members_strip_text();
            let mut sc = dim;
            sc[3] *= ease;
            labels.push(TextLabel::new(
                truncate_chars(&strip, 72),
                modal.x + pad + list_w + 10.0,
                modal.y + 8.0,
                11.0,
                sc,
            ));
        }
        let mut y = modal.y + CHANNEL_LIST_TOP;
        for ch in &workspace_ui.channels {
            let mut tc = if *ch == workspace_ui.channel {
                bright
            } else {
                muted
            };
            tc[3] *= ease;
            labels.push(TextLabel::new(
                format!("#{ch}"),
                modal.x + pad + 10.0,
                y,
                12.0,
                tc,
            ));
            y += CHANNEL_ROW_H;
        }
        // "+ New" matches `WorkspaceUi::new_channel_row_rect` hit target.
        {
            let mut tc = dim;
            tc[3] *= ease;
            labels.push(TextLabel::new(
                "+ New",
                modal.x + pad + 10.0,
                y,
                12.0,
                tc,
            ));
        }
        let msg_x = modal.x + pad + list_w + 10.0;
        let mut my = modal.y + pad + 8.0 + PRESENCE_STRIP_H;
        let msg_bottom = modal.y + modal.h - pad - COMPOSE_H - 8.0;
        for msg in workspace_ui.visible_messages(12) {
            if my > msg_bottom - 20.0 {
                break;
            }
            let mut fc = dim;
            fc[3] *= ease;
            let mut bc = muted;
            bc[3] *= ease;
            let from_label = if msg.kind == "file" {
                format!("{} [file]:", msg.from)
            } else {
                format!("{}:", msg.from)
            };
            labels.push(TextLabel::new(from_label, msg_x, my, 11.0, fc));
            labels.push(TextLabel::new(
                truncate_chars(&msg.body, 56),
                msg_x + 8.0,
                my + 14.0,
                12.0,
                bc,
            ));
            my += 36.0;
        }
        let input_y = modal.y + modal.h - pad - COMPOSE_H;
        let placeholder = match workspace_ui.mode {
            ComposeMode::NewChannel => "Channel name · Enter to create",
            ComposeMode::AttachPath => "Path to attach · Enter to upload",
            ComposeMode::Message => "Message #… · Enter to send · drop file to attach",
        };
        let draft = if workspace_ui.draft.is_empty() {
            placeholder
        } else {
            workspace_ui.draft.as_str()
        };
        let mut dc = if workspace_ui.draft.is_empty() {
            dim
        } else {
            bright
        };
        dc[3] *= ease;
        labels.push(TextLabel::new(
            draft.to_string(),
            modal.x + pad + 14.0,
            input_y + 14.0,
            13.0,
            dc,
        ));
        if !workspace_ui.draft.is_empty() || caret_a > 0.02 {
            let cx = modal.x
                + pad
                + 14.0
                + workspace_ui.draft.chars().count() as f32 * 7.5;
            labels.push(TextLabel::new(
                CARET_BLOCK,
                cx,
                input_y + 14.0,
                13.0,
                [CARET_RGB[0], CARET_RGB[1], CARET_RGB[2], caret_a * ease],
            ));
        }
        if !workspace_ui.status.is_empty() {
            let mut sc = dim;
            sc[3] *= ease;
            labels.push(TextLabel::new(
                truncate_chars(&workspace_ui.status, 48),
                modal.x + pad + list_w + 10.0,
                modal.y + modal.h - 20.0,
                10.0,
                sc,
            ));
        }
    }

    if transfer.visible() {
        let ease = transfer.content_ease().clamp(0.0, 1.0);
        let modal = transfer.animated_modal_rect(win_w, win_h);
        let pad = 16.0;
        let mut title_c = bright;
        title_c[3] *= ease;
        let title = match transfer.mode {
            crate::transfer_ui::TransferMode::Send => "Send file (ticket)",
            crate::transfer_ui::TransferMode::Receive => "Receive ticket",
        };
        labels.push(TextLabel::new(
            title,
            modal.x + pad,
            modal.y + 16.0,
            15.0,
            title_c,
        ));
        let mut sc = muted;
        sc[3] *= ease;
        labels.push(TextLabel::new(
            transfer.status.clone(),
            modal.x + pad,
            modal.y + 36.0,
            11.0,
            sc,
        ));
        // Layout matches push_modal_glass: optional drop zone then path field.
        let mut y = modal.y + 56.0;
        if transfer.mode == crate::transfer_ui::TransferMode::Send && !transfer.is_running() {
            let drop_label = if transfer.drop_hover {
                "▼  release to send this file  ▼"
            } else {
                "┌── drop a file or folder here ──┐"
            };
            let mut dc = if transfer.drop_hover { bright } else { dim };
            dc[3] *= ease;
            labels.push(TextLabel::new(
                drop_label,
                modal.x + pad + 10.0,
                y + 8.0,
                12.0,
                dc,
            ));
            y += 32.0 + 10.0;
            if !transfer.drop_hint.is_empty() {
                // Status line already shows drop_hint; keep zone clean.
            }
        }
        let input_y = y;
        let placeholder = match transfer.mode {
            crate::transfer_ui::TransferMode::Send => "/path/to/file · or drop",
            crate::transfer_ui::TransferMode::Receive => "ticket words…",
        };
        let shown = if transfer.buf.is_empty() {
            placeholder
        } else {
            transfer.buf.as_str()
        };
        let mut ic = if transfer.buf.is_empty() { dim } else { bright };
        ic[3] *= ease;
        labels.push(TextLabel::new(
            shown.to_string(),
            modal.x + pad + 14.0,
            input_y + 16.0,
            14.0,
            ic,
        ));
        if !transfer.is_running() {
            let cx = modal.x + pad + 14.0 + transfer.buf.chars().count() as f32 * 8.0;
            labels.push(TextLabel::new(
                CARET_BLOCK,
                cx.min(modal.x + modal.w - pad - 20.0),
                input_y + 16.0,
                14.0,
                [CARET_RGB[0], CARET_RGB[1], CARET_RGB[2], caret_a * ease],
            ));
        }
        if !transfer.ticket.is_empty() {
            let mut tc = bright;
            tc[3] *= ease;
            labels.push(TextLabel::new(
                truncate_chars(&transfer.ticket, 64),
                modal.x + pad,
                modal.y + modal.h - 72.0,
                11.0,
                tc,
            ));
            // Copy ticket chip label (or "Copied!" flash).
            let chip_label = if !transfer.copy_flash.is_empty() {
                transfer.copy_flash.as_str()
            } else {
                "Copy ticket"
            };
            let mut cc = if !transfer.copy_flash.is_empty() {
                bright
            } else {
                muted
            };
            cc[3] *= ease;
            let chip_w = 120.0_f32.min(modal.w - pad * 2.0);
            let label_w = chip_label.chars().count() as f32 * 7.0;
            labels.push(TextLabel::new(
                chip_label.to_string(),
                modal.x + (modal.w - label_w.min(chip_w)) * 0.5,
                modal.y + modal.h - 44.0,
                12.0,
                cc,
            ));
        }
    }

    let _ = m;
}

/// Truncate to at most `max_chars` (append … if cut). Keeps footer single-line.
fn truncate_chars(s: &str, max_chars: usize) -> String {
    if max_chars == 0 {
        return String::new();
    }
    let n = s.chars().count();
    if n <= max_chars {
        return s.to_string();
    }
    if max_chars == 1 {
        return "…".into();
    }
    let keep = max_chars - 1;
    let mut out: String = s.chars().take(keep).collect();
    out.push('…');
    out
}

/// Paint terminal cells; optional selection underlay for the focused pane.
///
/// # Selection highlight
/// Uses full-block mono glyphs (`█`) with theme jade at [`SELECTION_ALPHA`]
/// behind cell text — same pipeline as ANSI bg, no glass/shader fill pass.
///
/// Remaining hooks (app / host): multi-click word/line select, right-click
/// copy-or-paste, clear on focus change / resize / alt-screen, optional
/// extend-while-scrolling under the cursor. See `TERMINAL_HOOKS.md`.
fn push_pane_cells(
    labels: &mut Vec<TextLabel>,
    pl: &PaneLayout,
    pane: &crate::session::Pane,
    cursor_visible: bool,
    selection: Option<&Selection>,
    selection_rgb: [f32; 3],
) {
    let mono_size = 14.0; // Gohu design size (product FontSizePx)
    let origin_x = pl.cells.x;
    let origin_y = pl.cells.y;
    let grid = &pane.grid;
    let cursor = grid.cursor();
    let live_view = grid.view_offset() == 0;
    let sel = selection.filter(|s| !s.is_empty());

    for row in 0..grid.rows() {
        let cells = grid.visible_row_cells(row);
        if cells.is_empty() {
            continue;
        }
        let abs_row = grid.viewport_to_abs(row);
        let has_content = cells.iter().any(|c| c.ch != ' ' || c.bg.is_some());
        let has_cursor = cursor_visible && live_view && cursor.row == row;
        let has_selection =
            sel.is_some_and(|s| (0..grid.cols()).any(|c| s.contains(c, abs_row)));
        if !has_content && !has_cursor && !has_selection {
            continue;
        }

        let y = origin_y + row as f32 * CELL_H;
        // Never paint terminal cells into the footer / past the cells rect.
        if y + CELL_H > pl.cells.y + pl.cells.h + 0.5 {
            break;
        }

        // Selection wash under glyphs (and under ANSI cell bg when both apply).
        if let Some(s) = sel {
            push_selection_row(
                labels,
                s,
                origin_x,
                y,
                mono_size,
                grid.cols(),
                abs_row,
                selection_rgb,
            );
        }

        let mut col = 0usize;
        while col < cells.len() {
            let start = col;
            let fg = cells[col].fg;
            let bg = cells[col].bg;
            let mut text = String::new();
            while col < cells.len() && cells[col].fg == fg && cells[col].bg == bg {
                text.push(cells[col].ch);
                col += 1;
            }
            if !text.chars().any(|c| c != ' ') {
                continue;
            }
            let end_col = start + text.chars().count();
            if end_col == cells.len() {
                while text.ends_with(' ') {
                    text.pop();
                }
            }
            if text.is_empty() {
                continue;
            }

            let x = origin_x + start as f32 * CELL_W;
            if let Some(bg) = bg {
                let blocks: String = "█".repeat(text.chars().count().max(1));
                labels.push(TextLabel::mono(
                    blocks,
                    x,
                    y,
                    mono_size,
                    [bg[0], bg[1], bg[2], 0.85],
                ));
            }
            labels.push(TextLabel::mono(
                text,
                x,
                y,
                mono_size,
                [fg[0], fg[1], fg[2], 0.95],
            ));
        }

        if has_cursor {
            let cx = origin_x + cursor.col as f32 * CELL_W;
            let cy = origin_y + row as f32 * CELL_H;
            labels.push(TextLabel::mono(
                CARET_BLOCK,
                cx,
                cy,
                mono_size,
                [CARET_RGB[0], CARET_RGB[1], CARET_RGB[2], 0.55],
            ));
        }
    }
}

/// Contiguous accent full-block runs for one viewport row of the selection.
fn push_selection_row(
    labels: &mut Vec<TextLabel>,
    selection: &Selection,
    origin_x: f32,
    y: f32,
    mono_size: f32,
    cols: u16,
    abs_row: usize,
    accent: [f32; 3],
) {
    let mut col = 0u16;
    while col < cols {
        if !selection.contains(col, abs_row) {
            col += 1;
            continue;
        }
        let start = col;
        col += 1;
        while col < cols && selection.contains(col, abs_row) {
            col += 1;
        }
        let n = (col - start) as usize;
        let x = origin_x + start as f32 * CELL_W;
        let blocks: String = "█".repeat(n.max(1));
        labels.push(TextLabel::mono(
            blocks,
            x,
            y,
            mono_size,
            [accent[0], accent[1], accent[2], SELECTION_ALPHA],
        ));
    }
}

fn create_rain_target(
    device: &wgpu::Device,
    width: u32,
    height: u32,
) -> (wgpu::Texture, wgpu::TextureView) {
    let tex = device.create_texture(&wgpu::TextureDescriptor {
        label: Some("rain target"),
        size: wgpu::Extent3d {
            width: width.max(1),
            height: height.max(1),
            depth_or_array_layers: 1,
        },
        mip_level_count: 1,
        sample_count: 1,
        dimension: wgpu::TextureDimension::D2,
        format: wgpu::TextureFormat::Rgba8UnormSrgb,
        usage: wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::TEXTURE_BINDING,
        view_formats: &[],
    });
    let view = tex.create_view(&wgpu::TextureViewDescriptor::default());
    (tex, view)
}

fn create_scene_target(
    device: &wgpu::Device,
    format: wgpu::TextureFormat,
    width: u32,
    height: u32,
) -> (wgpu::Texture, wgpu::TextureView) {
    let tex = device.create_texture(&wgpu::TextureDescriptor {
        label: Some("scene target"),
        size: wgpu::Extent3d {
            width: width.max(1),
            height: height.max(1),
            depth_or_array_layers: 1,
        },
        mip_level_count: 1,
        sample_count: 1,
        dimension: wgpu::TextureDimension::D2,
        format,
        usage: wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::TEXTURE_BINDING,
        view_formats: &[],
    });
    let view = tex.create_view(&wgpu::TextureViewDescriptor::default());
    (tex, view)
}
