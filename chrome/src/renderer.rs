//! wgpu renderer: rain pass → glass composite → surface.

use std::sync::Arc;

use bytemuck::{Pod, Zeroable};
use wgpu::util::DeviceExt;
use winit::window::Window;

use crate::chrome_ui::{ChipUi, TabJelly};
use crate::commands::{
    filter_commands, splash_hint_rows, Command, HelpLayout, HelpState, PaletteState, SplashState,
};
use crate::confirm::ConfirmState;
use crate::input::{is_mac, traffic_light_rects};
use crate::layout::{FrameLayout, Metrics, PaneLayout, PanelInstance};
use crate::rain_atlas::RainAtlas;
use crate::rain_sim::{self, RainUniforms};
use crate::rain_thread::RainThread;
use crate::rename::RenameState;
use crate::links::LinkHoverSpan;
use crate::selection::Selection;
use crate::session::ChromeSession;
use crate::caffeine::Caffeine;
use crate::notes::NotesState;
use crate::settings::SettingsState;
use crate::toast::ToastState;
use crate::transfer_ui::TransferUi;
use crate::workspace_ui::WorkspaceUi;
use crate::text::{MonoCellMetrics, TextLabel, TextLayer, CARET_BLOCK};

/// Jade selection wash alpha for full-block underlay (`█` mono labels).
const SELECTION_ALPHA: f32 = 0.32;
/// Primary wash under a hovered URL span (product link hover ≈ 12% BG blend).
const LINK_HOVER_ALPHA: f32 = 0.14;

/// Secondary UI label color: blend theme muted → FG by `lift` (0=muted, 1=fg).
fn secondary_label_rgba(pal: &crate::theme::ThemeColors, lift: f32, alpha: f32) -> [f32; 4] {
    let t = lift.clamp(0.0, 1.0);
    [
        (pal.muted[0] * (1.0 - t) + pal.fg[0] * t).clamp(0.0, 1.0),
        (pal.muted[1] * (1.0 - t) + pal.fg[1] * t).clamp(0.0, 1.0),
        (pal.muted[2] * (1.0 - t) + pal.fg[2] * t).clamp(0.0, 1.0),
        alpha.clamp(0.0, 1.0),
    ]
}

/// True when two accents match closely (preset selection ring).
fn accent_near(a: [f32; 3], b: [f32; 3]) -> bool {
    (a[0] - b[0]).abs() < 0.02 && (a[1] - b[1]).abs() < 0.02 && (a[2] - b[2]).abs() < 0.02
}

/// Fallback mono cell metrics (Gohu uni14 design). Prefer [`Renderer::cell_metrics`].
pub const CELL_W: f32 = 7.0;
pub const CELL_H: f32 = 14.0;
/// Default inner glass inset — prefer [`Metrics::inset`] at call sites.
/// Kept for callers that don't have Metrics yet; matches Spacing::inset.
pub const TERM_PAD: f32 = 8.0; // matches Spacing::inset (inside); outside is Spacing::space

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
    /// Theme primary RGB + pad (ModalButtonActive, hairlines, chip press).
    primary: [f32; 4],
    /// x=1 → transparent outside panels (cursor-follow drag chip).
    flags: [f32; 4],
}

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct LensUniforms {
    size: [f32; 4],
    lens: [f32; 4],
    glass: [f32; 4],
    glass2: [f32; 4],
    /// x = last-tab dissolve blur radius (logical px).
    exit: [f32; 4],
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

/// Click-through chip that follows the pointer during a pane/tab drag.
pub struct GhostLayer {
    pub window: Arc<Window>,
    surface: wgpu::Surface<'static>,
    config: wgpu::SurfaceConfiguration,
    text: crate::text::TextLayer,
    frame_uniform_buf: wgpu::Buffer,
    panel_buf: wgpu::Buffer,
    size: winit::dpi::PhysicalSize<u32>,
    scale: f32,
}

pub struct Renderer {
    instance: wgpu::Instance,
    surface: wgpu::Surface<'static>,
    device: wgpu::Device,
    queue: wgpu::Queue,
    config: wgpu::SurfaceConfiguration,
    size: winit::dpi::PhysicalSize<u32>,
    scale_factor: f32,

    rain_atlas: RainAtlas,
    /// Encodes rain off the UI thread; we only sample the last completed RT.
    rain_thread: RainThread,

    composite_pipeline: wgpu::RenderPipeline,
    composite_overlay_pipeline: wgpu::RenderPipeline,
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
    /// Last-tab dissolve blur (logical px). Set per frame before [`render`].
    pub window_exit_blur: f32,
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

        let rain_seed = RainUniforms {
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
        };

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

        let sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            label: Some("rain sampler"),
            mag_filter: wgpu::FilterMode::Linear,
            min_filter: wgpu::FilterMode::Linear,
            ..Default::default()
        });

        let rain_thread = RainThread::spawn(
            device.clone(),
            queue.clone(),
            rain_pipeline,
            rain_bgl,
            rain_atlas.view.clone(),
            sampler.clone(),
            config.width,
            config.height,
            rain_seed,
        );

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
                primary: [
                    crate::theme::DEFAULT_PRIMARY[0],
                    crate::theme::DEFAULT_PRIMARY[1],
                    crate::theme::DEFAULT_PRIMARY[2],
                    1.0,
                ],
                flags: [0.0, 0.0, 0.0, 0.0],
            }),
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        });

        // Help sheet alone can push 30+ frost rows; start with headroom (shader MAX_PANELS=128).
        let panel_capacity = 64u64;
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

        let composite_overlay_pipeline =
            device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
                label: Some("composite overlay pipeline"),
                layout: Some(
                    &device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
                        label: Some("composite overlay pl"),
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
                        blend: Some(wgpu::BlendState::PREMULTIPLIED_ALPHA_BLENDING),
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
                exit: [0.0, 0.0, 0.0, 0.0],
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
            instance,
            surface,
            device,
            queue,
            config,
            size,
            scale_factor,
            rain_atlas,
            rain_thread,
            composite_pipeline,
            composite_overlay_pipeline,
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
            window_exit_blur: 0.0,
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

    /// Measured mono cell (logical px) — use for PTY cols/rows and paint pitch.
    pub fn cell_metrics(&self) -> MonoCellMetrics {
        self.text.mono_cell()
    }

    /// Apply settings mono font id. Returns true if metrics changed (caller
    /// should re-grid PTY panes).
    pub fn set_mono_font_id(&mut self, id: &str) -> bool {
        self.text.set_mono_font_id(id)
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

    /// Small always-on-top surface that shares this GPU device (no second adapter).
    pub fn spawn_ghost(&self, window: Arc<Window>) -> GhostLayer {
        let size = window.inner_size();
        let scale = window.scale_factor() as f32;
        let surface = self
            .instance
            .create_surface(window.clone())
            .expect("ghost surface");
        let config = wgpu::SurfaceConfiguration {
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
            format: self.surface_format,
            width: size.width.max(1),
            height: size.height.max(1),
            present_mode: wgpu::PresentMode::AutoVsync,
            alpha_mode: self.config.alpha_mode,
            view_formats: vec![],
            desired_maximum_frame_latency: 2,
        };
        surface.configure(&self.device, &config);
        let mut text = crate::text::TextLayer::new(&self.device, &self.queue, self.surface_format);
        text.resize(size, scale.max(0.01));
        let frame_uniform_buf =
            self.device
                .create_buffer_init(&wgpu::util::BufferInitDescriptor {
                    label: Some("ghost uniforms"),
                    contents: bytemuck::bytes_of(&FrameUniforms {
                        size: [1.0, 1.0, 1.0, 1.0],
                        misc: [0.0, 1.0, 1.0, crate::settings::GLASS_DARKEN_DEFAULT],
                        glass: [GLASS_IOR, GLASS_EDGE, GLASS_BEVEL, GLASS_DEPTH],
                        glass2: [
                            GLASS_ABERRATION,
                            GLASS_BLUR,
                            GLASS_REFLECTION,
                            GLASS_SHINE.max(0.1),
                        ],
                        hover: [0.0, 0.0, 28.0, 0.0],
                        primary: [0.0, 0.0, 0.0, 1.0],
                        flags: [1.0, 0.0, 0.0, 0.0],
                    }),
                    usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
                });
        let panel_buf = self.device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("ghost panels"),
            size: 4 * std::mem::size_of::<crate::layout::PanelInstance>() as u64,
            usage: wgpu::BufferUsages::STORAGE | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        GhostLayer {
            window,
            surface,
            config,
            text,
            frame_uniform_buf,
            panel_buf,
            size,
            scale,
        }
    }

    pub fn render_ghost(
        &self,
        ghost: &mut GhostLayer,
        title: &str,
        settings: &SettingsState,
        tab: bool,
    ) -> Result<(), wgpu::SurfaceError> {
        let size = ghost.window.inner_size();
        let scale = ghost.window.scale_factor() as f32;
        if size.width != ghost.size.width
            || size.height != ghost.size.height
            || (scale - ghost.scale).abs() > 0.01
        {
            if size.width == 0 || size.height == 0 {
                return Ok(());
            }
            ghost.size = size;
            ghost.scale = scale.max(0.01);
            ghost.config.width = size.width;
            ghost.config.height = size.height;
            ghost.surface.configure(&self.device, &ghost.config);
            ghost.text.resize(size, ghost.scale);
        }
        let frame = ghost.surface.get_current_texture()?;
        let view = frame
            .texture
            .create_view(&wgpu::TextureViewDescriptor::default());
        let fw = ghost.size.width as f32;
        let fh = ghost.size.height as f32;
        let logical_w = fw / ghost.scale;
        let logical_h = fh / ghost.scale;
        let pad = 2.0;
        let rect = crate::layout::Rect::new(
            pad,
            pad,
            (logical_w - pad * 2.0).max(4.0),
            (logical_h - pad * 2.0).max(4.0),
        );
        let kind = if tab {
            crate::layout::PanelKind::ChipActive
        } else {
            crate::layout::PanelKind::Terminal
        };
        let radius = if tab {
            self.metrics.chip_radius
        } else {
            self.metrics.radius
        };
        let panel = crate::layout::PanelInstance::glass(rect, radius, kind).with_opacity(0.92);
        let mut panels = [panel; 4];
        panels[1] = panel;
        panels[2] = panel;
        panels[3] = panel;
        self.queue
            .write_buffer(&ghost.panel_buf, 0, bytemuck::cast_slice(&panels));
        let j = settings.prefs.theme_colors().jade;
        let pal = settings.prefs.theme_colors();
        self.queue.write_buffer(
            &ghost.frame_uniform_buf,
            0,
            bytemuck::bytes_of(&FrameUniforms {
                size: [logical_w, logical_h, fw, fh],
                misc: [
                    0.0,
                    ghost.scale,
                    1.0,
                    settings.prefs.glass_darken.clamp(0.0, 0.95),
                ],
                glass: [GLASS_IOR, GLASS_EDGE, GLASS_BEVEL, GLASS_DEPTH],
                glass2: [
                    GLASS_ABERRATION,
                    GLASS_BLUR,
                    GLASS_REFLECTION,
                    GLASS_SHINE.max(0.1),
                ],
                hover: [0.0, 0.0, 28.0, 0.0],
                primary: [j[0], j[1], j[2], 1.0],
                flags: [1.0, 0.0, 0.0, 0.0],
            }),
        );
        let rain_view = self.rain_thread.front_view();
        let bind = self.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("ghost composite"),
            layout: &self.composite_bgl,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: ghost.frame_uniform_buf.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::TextureView(&rain_view),
                },
                wgpu::BindGroupEntry {
                    binding: 2,
                    resource: wgpu::BindingResource::Sampler(&self.sampler),
                },
                wgpu::BindGroupEntry {
                    binding: 3,
                    resource: ghost.panel_buf.as_entire_binding(),
                },
            ],
        });
        let mut encoder = self
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("ghost frame"),
            });
        {
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("ghost composite"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &view,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color::TRANSPARENT),
                        store: wgpu::StoreOp::Store,
                    },
                })],
                depth_stencil_attachment: None,
                timestamp_writes: None,
                occlusion_query_set: None,
            });
            pass.set_pipeline(&self.composite_overlay_pipeline);
            pass.set_bind_group(0, &bind, &[]);
            pass.draw(0..3, 0..1);
        }
        let label = crate::text::TextLabel::centered(
            title,
            [rect.x, rect.y, rect.w, rect.h],
            14.0,
            [pal.fg[0], pal.fg[1], pal.fg[2], 0.95],
        );
        ghost
            .text
            .prepare(&self.device, &self.queue, std::slice::from_ref(&label));
        ghost.text.render(&self.device, &mut encoder, &view);
        self.queue.submit(Some(encoder.finish()));
        frame.present();
        Ok(())
    }

    pub fn render(
        &mut self,
        session: &ChromeSession,
        settings: &SettingsState,
        palette: &PaletteState,
        help: &HelpState,
        confirm: &ConfirmState,
        splash: &SplashState,
        notes: &NotesState,
        workspace_ui: &WorkspaceUi,
        transfer: &TransferUi,
        rename: &RenameState,
        caffeine: &Caffeine,
        toast: &ToastState,
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
        // Hovered URL span for primary tint (focused pane; None = no-op).
        hovered_link: Option<&LinkHoverSpan>,
        pane_drop: Option<crate::input::DropKind>,
        drag_ghost: Option<crate::layout::Rect>,
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

        let j = settings.prefs.theme_colors().jade;
        self.rain_thread.publish(
            settings.prefs.rain,
            self.config.width,
            self.config.height,
            RainUniforms {
                res_time: [fw, fh, 0.0, 0.0],
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
                color: [j[0], j[1], j[2], 1.0],
                head_color: [
                    (j[0] + 0.15).min(1.0),
                    (j[1] + 0.12).min(1.0),
                    (j[2] + 0.10).min(1.0),
                    1.0,
                ],
            },
        );
        let rain_view = self.rain_thread.front_view();
        let _ = t; // wall-clock still used by composite glass time

        let surface = session.active_tab().map(|t| t.surface).unwrap_or(0);
        let active_idx = session
            .tabs
            .iter()
            .filter(|tab| tab.surface == surface)
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
        if let Some(drop) = pane_drop {
            use crate::input::{drop_edge_rect, DropKind};
            use crate::layout::{PanelInstance, PanelKind};
            match drop {
                DropKind::Edge { pane_id, edge } => {
                    if let Some(pl) = layout.panes.iter().find(|p| p.pane_id == pane_id) {
                        let strip = drop_edge_rect(pl.glass, edge);
                        panels.push(
                            PanelInstance::glass(
                                strip,
                                self.metrics.chip_radius,
                                PanelKind::ModalButtonActive,
                            )
                            .with_opacity(0.85),
                        );
                    }
                }
                DropKind::Tab { tab_id } => {
                    let surface = session.active_tab().map(|t| t.surface).unwrap_or(0);
                    if let Some(i) = session
                        .tabs
                        .iter()
                        .filter(|t| t.surface == surface)
                        .position(|t| t.id == tab_id)
                    {
                        if let Some(chip) = layout.tab_chips.get(i) {
                            panels.push(
                                PanelInstance::glass(
                                    *chip,
                                    self.metrics.chip_radius,
                                    PanelKind::ModalButtonActive,
                                )
                                .with_opacity(0.9),
                            );
                        }
                    }
                }
                DropKind::TabInsert { index } => {
                    if let Some(chip) = layout.tab_chips.get(index) {
                        let gap = crate::layout::Rect::new(
                            chip.x - 3.0,
                            chip.y,
                            6.0,
                            chip.h,
                        );
                        panels.push(
                            PanelInstance::glass(gap, 2.0, PanelKind::ModalButtonActive)
                                .with_opacity(0.95),
                        );
                    }
                }
                DropKind::TearOff => {}
            }
        }
        if let Some(ghost) = drag_ghost {
            use crate::layout::{PanelInstance, PanelKind};
            panels.push(
                PanelInstance::glass(ghost, self.metrics.radius, PanelKind::ModalFrost)
                    .with_opacity(0.55),
            );
        }
        // Workspace interiors live in the pane (under overlay scrim), not as a modal.
        if workspace_ui.is_docked() {
            if let Some(host) = workspace_host_rect(workspace_ui, layout) {
                let accent = settings.prefs.theme_colors().jade;
                let logical_w = layout.title.w;
                let logical_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                push_workspace_glass(
                    &mut panels,
                    self.metrics,
                    host,
                    1.0,
                    workspace_ui,
                    accent,
                    logical_w,
                    logical_h,
                    false,
                );
            }
        }
        // Focused-pane scroll thumb (product-style; hide when nothing to scroll).
        {
            use crate::layout::{PanelInstance, PanelKind, Rect};
            let focus = session.focus_pane_id();
            for pl in &layout.panes {
                if pl.pane_id != focus {
                    continue;
                }
                if session.is_widget(pl.pane_id) {
                    continue;
                }
                let Some(pane) = session.panes.get(&pl.pane_id) else {
                    continue;
                };
                let geom = pane.grid.scrollbar(pl.cells.h);
                if !geom.visible {
                    continue;
                }
                const TRACK_W: f32 = 5.0;
                let x = pl.cells.x + pl.cells.w - TRACK_W - 2.0;
                let thumb = Rect::new(
                    x,
                    pl.cells.y + geom.thumb_y,
                    TRACK_W,
                    geom.thumb_h.max(12.0),
                );
                panels.push(
                    PanelInstance::glass(thumb, 2.5, PanelKind::ModalFrost).with_opacity(0.5),
                );
            }
        }
        // Modal overlays (settings / palette / help / splash / confirm / notes / rename)
        {
            use crate::layout::{PanelInstance, PanelKind, Rect};
            let any_overlay = settings.visible()
                || palette.visible()
                || help.visible()
                || confirm.visible()
                || splash.visible()
                || notes.visible()
                || workspace_ui.is_modal()
                || transfer.visible()
                || rename.visible();
            if any_overlay {
                let scrim_a = settings
                    .scrim_alpha()
                    .max(palette.scrim_alpha())
                    .max(help.scrim_alpha())
                    .max(confirm.scrim_alpha())
                    .max(splash.scrim_alpha())
                    .max(notes.scrim_alpha())
                    .max(workspace_ui.scrim_alpha())
                    .max(transfer.scrim_alpha())
                    .max(rename.scrim_alpha());
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
                splash,
                notes,
                workspace_ui,
                transfer,
                rename,
                commands,
            );
        }
        // Toast is non-modal: frost chip only, never a scrim or input capture.
        if toast.visible() {
            use crate::layout::{PanelInstance, PanelKind};
            let op = toast.opacity();
            let chip = toast.chip_rect(logical_w, logical_h);
            panels.push(
                PanelInstance::glass(chip, self.metrics.chip_radius, PanelKind::ModalFrost)
                    .with_opacity(op),
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
                primary: {
                    let j = settings.prefs.theme_colors().jade;
                    [j[0], j[1], j[2], 1.0]
                },
                flags: [0.0, 0.0, 0.0, 0.0],
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
                exit: [self.window_exit_blur.max(0.0), 0.0, 0.0, 0.0],
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
                    resource: wgpu::BindingResource::TextureView(&rain_view),
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

        // Rain is encoded on RainThread. This frame only composites the last
        // completed rain RT (never waits on the rain pass).

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
            splash,
            notes,
            workspace_ui,
            transfer,
            rename,
            caffeine,
            toast,
            commands,
            pty_active,
            terminal_cursor_visible,
            input_caret_alpha,
            chip_ui,
            &self.tab_jelly,
            term_selection,
            hovered_link,
            self.cell_metrics(),
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
/// `cells` is already inset — no extra pad. Uses measured mono metrics when provided.
pub fn terminal_grid_size(cells: &crate::layout::Rect, _inset: f32) -> (u16, u16) {
    terminal_grid_size_with(cells, CELL_W, CELL_H)
}

/// Like [`terminal_grid_size`] with explicit cell pitch (logical px).
pub fn terminal_grid_size_with(
    cells: &crate::layout::Rect,
    cell_w: f32,
    cell_h: f32,
) -> (u16, u16) {
    let cw = cell_w.max(1.0);
    let ch = cell_h.max(1.0);
    let inner_w = cells.w.max(cw);
    let inner_h = cells.h.max(ch);
    let cols = (inner_w / cw).floor().max(1.0) as u16;
    let rows = (inner_h / ch).floor().max(1.0) as u16;
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

fn workspace_host_rect(workspace_ui: &WorkspaceUi, layout: &FrameLayout) -> Option<crate::layout::Rect> {
    let id = workspace_ui.docked_pane?;
    layout.panes.iter().find(|p| p.pane_id == id).map(|p| p.glass)
}

/// True overlay cards (not docked workspace). Terminal glyphs under these are clipped.
fn overlay_cards(
    win_w: f32,
    win_h: f32,
    settings: &SettingsState,
    palette: &PaletteState,
    help: &HelpState,
    confirm: &ConfirmState,
    splash: &SplashState,
    notes: &NotesState,
    workspace_ui: &WorkspaceUi,
    transfer: &TransferUi,
    rename: &RenameState,
) -> Vec<crate::layout::Rect> {
    let mut cards = Vec::new();
    if settings.visible() {
        cards.push(settings.animated_modal_rect(win_w, win_h));
    }
    if palette.visible() {
        cards.push(palette.modal_rect(win_w, win_h));
    }
    if help.visible() {
        cards.push(HelpLayout::modal_rect(win_w, win_h));
    }
    if confirm.visible() {
        cards.push(confirm.animated_modal_rect(win_w, win_h));
    }
    if splash.visible() {
        cards.push(SplashState::modal_rect(win_w, win_h));
    }
    if notes.visible() {
        cards.push(notes.animated_modal_rect(win_w, win_h));
    }
    if workspace_ui.is_modal() {
        cards.push(workspace_ui.card_rect(win_w, win_h));
    }
    if transfer.visible() {
        cards.push(transfer.animated_modal_rect(win_w, win_h));
    }
    if rename.visible() {
        cards.push(rename.modal_rect(win_w, win_h));
    }
    cards
}

fn hidden_by_cards(r: crate::layout::Rect, cards: &[crate::layout::Rect]) -> bool {
    cards.iter().any(|c| c.intersects(r))
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
    splash: &SplashState,
    notes: &NotesState,
    workspace_ui: &WorkspaceUi,
    transfer: &TransferUi,
    rename: &RenameState,
    caffeine: &Caffeine,
    toast: &ToastState,
    commands: &[Command],
    pty_active: bool,
    terminal_cursor_visible: bool,
    input_caret_alpha: f32,
    chip_ui: &ChipUi,
    tab_jelly: &TabJelly,
    term_selection: &Selection,
    hovered_link: Option<&LinkHoverSpan>,
    cell: MonoCellMetrics,
) -> Vec<TextLabel> {
    use crate::chrome_ui::{scale_rect, ChipId};
    // Chrome label colors track prefs theme (see SETTINGS_HOOKS.md / theme.rs).
    let pal = settings.prefs.theme_colors();
    let bright = [pal.fg[0], pal.fg[1], pal.fg[2], 0.95];
    // Secondary text: lift theme `muted` toward FG so palette subtitles,
    // inactive tabs, settings values, etc. stay readable on dark glass.
    // (Raw muted × low alpha was nearly invisible.)
    let muted = secondary_label_rgba(&pal, 0.55, 0.92);
    let dim = secondary_label_rgba(&pal, 0.42, 0.88);
    // Warm accent when caffeine is on (product styleCaffeineOn).
    let cafe_on = [1.0, 0.82, 0.45, 0.95];
    let cafe_off = [0.45, 0.55, 0.48, 0.70];
    let selection_rgb = pal.jade;
    // Link hover uses secondary/accent so it reads apart from selection primary.
    let link_hover_rgb = pal.secondary;
    // Carets / block cursor follow theme primary (not hardcoded jade).
    let caret_rgb = pal.jade;

    let mut labels = Vec::with_capacity(128);
    let _ = tab_jelly; // labels no longer follow jelly

    // No window title — chrome bar is tabs + logo only.
    // Gohu is bitmap-rooted @14 — prefer design size so chrome matches the host.
    let tab_size = 14.0;
    for (i, chip) in layout.tab_chips.iter().enumerate() {
        let id = ChipId::Tab(i);
        let surface = session.active_tab().map(|t| t.surface).unwrap_or(0);
        let title = session
            .tabs
            .iter()
            .filter(|t| t.surface == surface)
            .nth(i)
            .map(|t| t.title.as_str())
            .unwrap_or("?");
        let mut color = chip_ui.dim_color(id, if i == active_idx { bright } else { dim });
        if let Some((xi, exit)) = session.tab_exit_on_surface(surface) {
            if xi == i {
                color[3] *= exit.t.clamp(0.0, 1.0);
            }
        }
        // Labels sit on layout chips — no hover scale.
        let r = scale_rect(*chip, chip_ui.scale_for(id));
        // Title sits in the left band; close × has its own rect on the right.
        let title_r = crate::layout::FrameLayout::tab_title_rect(r);
        labels.push(TextLabel::centered(
            title,
            [title_r.x, title_r.y, title_r.w, title_r.h],
            tab_size,
            color,
        ));
        if let Some(close) = layout.tab_closes.get(i) {
            let cr = scale_rect(*close, chip_ui.scale_for(id));
            // × only dims on hover — no red flash.
            let mut xc = chip_ui.dim_color(id, dim);
            if let Some((xi, exit)) = session.tab_exit_on_surface(surface) {
                if xi == i {
                    xc[3] *= exit.t.clamp(0.0, 1.0);
                }
            }
            labels.push(TextLabel::centered(
                "×",
                [cr.x, cr.y, cr.w, cr.h],
                13.0,
                xc,
            ));
        }
    }

    // Ghost + : no hover recolor — just dim.
    {
        let id = ChipId::NewTab;
        let r = scale_rect(layout.tab_new, chip_ui.scale_for(id));
        let color = chip_ui.dim_color(id, dim);
        labels.push(TextLabel::centered("+", [r.x, r.y, r.w, r.h], 14.0, color));
    }

    // Caffeine ☕ — fully centered in the chip (hint only as short overlay text if timed).
    {
        let id = ChipId::Caffeine;
        let r = scale_rect(layout.caffeine, chip_ui.scale_for(id));
        let on = caffeine.active();
        let color = chip_ui.dim_color(id, if on { cafe_on } else { cafe_off });
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
        let id = ChipId::Logo;
        let r = scale_rect(layout.logo, chip_ui.scale_for(id));
        labels.push(TextLabel::centered(
            "硯",
            [r.x, r.y, r.w, r.h],
            14.0,
            chip_ui.dim_color(id, bright),
        ));
    }

    // Terminal cells paint after glass, so they punch through overlay cards.
    // Clip only under those cards; dim the rest so the backdrop still reads.
    let win_w = layout.title.w;
    let win_h = layout.workspace.y + layout.workspace.h + m.edge();
    let cover_cards = overlay_cards(
        win_w,
        win_h,
        settings,
        palette,
        help,
        confirm,
        splash,
        notes,
        workspace_ui,
        transfer,
        rename,
    );
    let dim_term = !cover_cards.is_empty();

    // Every leaf pane: header + cells + footer
    let focus = session.focus_pane_id();
    for pl in &layout.panes {
        let Some(pane) = session.panes.get(&pl.pane_id) else {
            continue;
        };
        if pl.title_pill.w > 2.0 {
            let title = pane.title.as_str();
            if !title.is_empty() {
                let max_c = ((pl.title_pill.w / cell.w.max(1.0)).floor() as usize).max(1);
                let draw = truncate_chars(title, max_c);
                let ts = 11.0;
                let ty = pl.title_pill.y + (pl.title_pill.h - ts).max(0.0) * 0.5;
                labels.push(TextLabel::new(
                    draw,
                    pl.title_pill.x + 5.0,
                    ty,
                    ts,
                    if pl.focused { bright } else { dim },
                ));
            }
        }
        if pl.close.w > 2.0 {
            let cid = ChipId::PaneClose(pl.pane_id);
            let cr = scale_rect(pl.close, chip_ui.scale_for(cid));
            let xc = chip_ui.dim_color(cid, dim);
            labels.push(TextLabel::centered("×", [cr.x, cr.y, cr.w, cr.h], 12.0, xc));
        }
        if pane.kind.is_workspace() {
            if workspace_ui.is_docked() {
                push_workspace_labels(
                    &mut labels,
                    workspace_ui,
                    settings,
                    win_w,
                    win_h,
                    1.0,
                    bright,
                    muted,
                    dim,
                    caret_rgb,
                    input_caret_alpha,
                    &cover_cards,
                );
            }
            continue;
        }
        if pane.kind.widget().is_some() {
            continue;
        }
        {
            let show_cursor = terminal_cursor_visible && pl.pane_id == focus;
            // Selection + link hover are one global model for the focused leaf.
            let pane_sel = if pl.pane_id == focus {
                Some(term_selection)
            } else {
                None
            };
            let pane_link = if pl.pane_id == focus {
                hovered_link
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
                pane_link,
                link_hover_rgb,
                caret_rgb,
                cell,
                &cover_cards,
                dim_term,
            );
        }

        // Footer (path + warp) only when the command strip is present.
        // Alt-screen panes collapse the strip to zero height — skip paint.
        if pl.warp.h >= 1.0 && pl.path.h >= 1.0 {
            let path_size = 12.0;
            let input_size = 13.0;
            let char_w = cell.w.max(1.0);

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
                        [caret_rgb[0], caret_rgb[1], caret_rgb[2], a],
                    ));
                }
            }
        }
    }

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
        splash,
        notes,
        workspace_ui,
        transfer,
        rename,
        commands,
        pty_active,
        input_caret_alpha,
        bright,
        muted,
        dim,
        caret_rgb,
    );

    // Ephemeral toast chip (non-modal) — bottom-center frost label.
    if toast.visible() {
        let chip = toast.chip_rect(win_w, win_h);
        let op = toast.opacity();
        let mut c = bright;
        c[3] *= op;
        labels.push(TextLabel::centered(
            toast.message().to_string(),
            [chip.x, chip.y, chip.w, chip.h],
            13.0,
            c,
        ));
    }

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
    splash: &SplashState,
    notes: &NotesState,
    workspace_ui: &WorkspaceUi,
    transfer: &TransferUi,
    rename: &RenameState,
    commands: &[Command],
) {
    use crate::layout::{PanelInstance, PanelKind, Rect};

    if splash.visible() {
        let ease = splash.content_ease().clamp(0.0, 1.0);
        let lay = SplashState::layout(win_w, win_h);
        panels.push(
            PanelInstance::glass(lay.modal, m.radius, PanelKind::Modal).with_opacity(ease),
        );
        // Hint rows as frost chips (same rects as text path).
        for r in &lay.rows {
            panels.push(
                PanelInstance::glass(*r, m.chip_radius, PanelKind::ModalFrost).with_opacity(ease),
            );
        }
        // Continue affordance — text is TextLabel::centered on this exact rect.
        panels.push(
            PanelInstance::glass(lay.continue_btn, m.chip_radius, PanelKind::ModalButton)
                .with_opacity(ease),
        );
    }

    if settings.visible() {
        let ease = settings.content_ease().clamp(0.0, 1.0);
        let lay = settings.layout(win_w, win_h);
        panels.push(
            PanelInstance::glass(lay.modal, m.radius, PanelKind::Modal).with_opacity(ease),
        );
        use crate::settings::settings_row;
        for (i, r) in lay.rows.iter().enumerate() {
            // Focused row is lit like a selected list item.
            let kind = if i == settings.selected {
                PanelKind::ModalButtonActive
            } else {
                PanelKind::ModalButton
            };
            panels.push(PanelInstance::glass(*r, m.chip_radius, kind).with_opacity(ease));
            // Boolean rows: Apple-style glass switch (track + spring-animated thumb).
            if i == settings_row::RAIN || i == settings_row::LENS {
                let t = if i == settings_row::RAIN {
                    settings.rain_toggle_t()
                } else {
                    settings.lens_toggle_t()
                };
                let track = crate::settings::SettingsLayout::toggle_track_rect(*r);
                // Track lights up as the knob crosses the midpoint.
                let track_kind = if t > 0.5 {
                    PanelKind::ModalButtonActive
                } else {
                    PanelKind::ModalFrost
                };
                panels.push(
                    PanelInstance::glass(track, track.h * 0.5, track_kind).with_opacity(ease),
                );
                let thumb = crate::settings::SettingsLayout::toggle_thumb_rect_t(*r, t);
                panels.push(
                    PanelInstance::glass(thumb, thumb.h * 0.5, PanelKind::ModalButton)
                        .with_opacity(ease),
                );
            }
        }
        // Color swatch chips — highlight matches the focused color row target.
        let focus_rgb = if settings.selected == settings_row::ACCENT {
            settings.prefs.effective_accent()
        } else {
            settings.prefs.primary
        };
        for (i, sw) in lay.swatches.iter().enumerate() {
            let selected = crate::theme::COLOR_PRESETS
                .get(i)
                .map(|rgb| accent_near(*rgb, focus_rgb))
                .unwrap_or(false);
            let kind = if selected {
                PanelKind::ModalButtonActive
            } else {
                PanelKind::ModalFrost
            };
            panels.push(
                PanelInstance::glass(*sw, sw.h * 0.35, kind).with_opacity(ease),
            );
        }
    }

    if help.visible() {
        let ease = help.content_ease().clamp(0.0, 1.0);
        // Animated rect (scale + drop) matches palette/settings — not a static card.
        let lay = HelpLayout::with_ease(win_w, win_h, ease);
        panels.push(
            PanelInstance::glass(lay.modal, m.radius, PanelKind::Modal).with_opacity(ease),
        );
        // One frost chip per shortcut row (same rects as text path).
        for (r, _, _) in &lay.rows {
            panels.push(
                PanelInstance::glass(*r, m.chip_radius, PanelKind::ModalFrost).with_opacity(ease),
            );
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

    if workspace_ui.is_modal() {
        let ease = workspace_ui.content_ease().clamp(0.0, 1.0);
        let host = workspace_ui.card_rect(win_w, win_h);
        let accent = settings.prefs.theme_colors().jade;
        push_workspace_glass(
            panels,
            m,
            host,
            ease,
            workspace_ui,
            accent,
            win_w,
            win_h,
            true,
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

    if rename.visible() {
        let ease = rename.content_ease().clamp(0.0, 1.0);
        let modal = rename.modal_rect(win_w, win_h);
        panels.push(PanelInstance::glass(modal, m.radius, PanelKind::Modal).with_opacity(ease));
        let pad = 14.0;
        let input_h = 44.0;
        let input = Rect::new(
            modal.x + pad,
            modal.y + 44.0,
            modal.w - pad * 2.0,
            input_h,
        );
        panels.push(
            PanelInstance::glass(input, m.chip_radius + 2.0, PanelKind::ModalFrost)
                .with_opacity(ease),
        );
    }

    // Palette last so it always sits above other chrome (including a workspace pane).
    if palette.visible() {
        use crate::commands::PaletteState;
        let ease = palette.content_ease().clamp(0.0, 1.0);
        let modal = palette.modal_rect(win_w, win_h);
        panels.push(PanelInstance::glass(modal, m.radius, PanelKind::Modal).with_opacity(ease));
        let pad = 14.0;
        let input_h = PaletteState::INPUT_H;
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
        let btn_h = PaletteState::ROW_H;
        let gap = PaletteState::ROW_GAP;
        let mut y = input.y + input.h + 12.0;
        let max_y = modal.y + modal.h - pad;
        for (i, _) in filtered.iter().enumerate().take(PaletteState::MAX_ROWS) {
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
}

fn push_workspace_glass(
    panels: &mut Vec<crate::layout::PanelInstance>,
    m: Metrics,
    host: crate::layout::Rect,
    ease: f32,
    workspace_ui: &WorkspaceUi,
    accent: [f32; 3],
    win_w: f32,
    win_h: f32,
    include_shell: bool,
) {
    use crate::layout::{PanelInstance, PanelKind, Rect};
    if include_shell {
        panels.push(PanelInstance::glass(host, m.radius, PanelKind::Modal).with_opacity(ease));
    }
    let pad = 14.0;
    let list_w = workspace_ui.channel_list_w(win_w, win_h);
    let ch = Rect::new(
        host.x + pad,
        host.y + pad,
        list_w,
        host.h - pad * 2.0 - 52.0,
    );
    panels.push(PanelInstance::glass(ch, m.chip_radius, PanelKind::ModalFrost).with_opacity(ease));
    let msg = Rect::new(
        ch.x + ch.w + 10.0,
        host.y + pad,
        host.x + host.w - pad - (ch.x + ch.w + 10.0),
        ch.h,
    );
    if msg.w > 8.0 {
        panels.push(
            PanelInstance::glass(msg, m.chip_radius, PanelKind::ModalFrost).with_opacity(ease),
        );
    }
    for b in workspace_ui.layout_bubbles(win_w, win_h, accent) {
        let kind = if b.mine {
            PanelKind::ModalButtonActive
        } else {
            PanelKind::ModalFrost
        };
        panels.push(
            PanelInstance::glass(b.rect, m.chip_radius + 2.0, kind)
                .with_opacity(ease)
                .with_tint(b.tint, b.tint_strength),
        );
    }
    let input = Rect::new(
        host.x + pad,
        host.y + host.h - pad - 44.0,
        host.w - pad * 2.0,
        44.0,
    );
    panels.push(
        PanelInstance::glass(input, m.chip_radius + 2.0, PanelKind::ModalFrost).with_opacity(ease),
    );
}

fn push_workspace_labels(
    labels: &mut Vec<TextLabel>,
    workspace_ui: &WorkspaceUi,
    settings: &SettingsState,
    win_w: f32,
    win_h: f32,
    ease: f32,
    bright: [f32; 4],
    muted: [f32; 4],
    dim: [f32; 4],
    caret_rgb: [f32; 3],
    caret_a: f32,
    cover_cards: &[crate::layout::Rect],
) {
    use crate::workspace_ui::{ComposeMode, CHANNEL_LIST_TOP, CHANNEL_ROW_H, COMPOSE_H, MODAL_PAD};
    let modal = workspace_ui.card_rect(win_w, win_h);
    let pad = MODAL_PAD;
    let list_w = workspace_ui.channel_list_w(win_w, win_h);
    let skip = |x: f32, y: f32| {
        hidden_by_cards(crate::layout::Rect::new(x, y, 12.0, 14.0), cover_cards)
    };
    let mut title_c = bright;
    title_c[3] *= ease;
    if !skip(modal.x + pad, modal.y + 8.0) {
        labels.push(TextLabel::new(
            "Workspace",
            modal.x + pad,
            modal.y + 8.0,
            13.0,
            title_c,
        ));
    }
    {
        let strip = workspace_ui.members_strip_text();
        let mut sc = dim;
        sc[3] *= ease;
        let x = modal.x + pad + list_w + 10.0;
        if !skip(x, modal.y + 8.0) {
            labels.push(TextLabel::new(
                truncate_chars(&strip, 72),
                x,
                modal.y + 8.0,
                11.0,
                sc,
            ));
        }
    }
    let mut y = modal.y + CHANNEL_LIST_TOP;
    for ch in &workspace_ui.channels {
        let mut tc = if *ch == workspace_ui.channel {
            bright
        } else {
            muted
        };
        tc[3] *= ease;
        if !skip(modal.x + pad + 10.0, y) {
            labels.push(TextLabel::new(
                format!("#{ch}"),
                modal.x + pad + 10.0,
                y,
                12.0,
                tc,
            ));
        }
        y += CHANNEL_ROW_H;
    }
    {
        let mut tc = dim;
        tc[3] *= ease;
        if !skip(modal.x + pad + 10.0, y) {
            labels.push(TextLabel::new(
                "+ New",
                modal.x + pad + 10.0,
                y,
                12.0,
                tc,
            ));
        }
    }
    let accent = settings.prefs.theme_colors().jade;
    for b in workspace_ui.layout_bubbles(win_w, win_h, accent) {
        let r = b.rect;
        if hidden_by_cards(r, cover_cards) {
            continue;
        }
        if b.system {
            let mut sc = dim;
            sc[3] *= ease;
            labels.push(TextLabel::centered(
                b.header.clone(),
                [r.x, r.y, r.w, r.h],
                11.0,
                sc,
            ));
            continue;
        }
        let mut hc = [b.tint[0], b.tint[1], b.tint[2], 0.95 * ease];
        if b.mine {
            hc = [accent[0], accent[1], accent[2], 0.98 * ease];
        }
        let mut bc = bright;
        bc[3] *= ease;
        labels.push(TextLabel::new(
            b.header.clone(),
            r.x + 10.0,
            r.y + 8.0,
            11.0,
            hc,
        ));
        if !b.body.is_empty() {
            labels.push(TextLabel::new(
                b.body.clone(),
                r.x + 10.0,
                r.y + 24.0,
                12.0,
                bc,
            ));
        }
    }
    let input_y = modal.y + modal.h - pad - COMPOSE_H;
    if workspace_ui.mention_picker_open() {
        let cands = workspace_ui.mention_candidates();
        let sel = workspace_ui.mention_selected();
        let row_h = 18.0;
        let show_n = cands.len().min(6);
        let box_h = row_h * show_n as f32 + 8.0;
        let box_y = input_y - box_h - 4.0;
        let box_x = modal.x + pad + list_w + 10.0;
        for (i, m) in cands.iter().take(show_n).enumerate() {
            if skip(box_x, box_y + 4.0 + i as f32 * row_h) {
                continue;
            }
            let (rgb, glyph) = crate::workspace_ui::member_identity(&m.name, &m.kind);
            let tc = if i == sel {
                [rgb[0], rgb[1], rgb[2], 0.98 * ease]
            } else {
                let mut d = dim;
                d[3] *= ease;
                d
            };
            let kind = if m.kind.eq_ignore_ascii_case("agent") {
                " · ai"
            } else {
                ""
            };
            labels.push(TextLabel::new(
                format!("{glyph} @{}{kind}", m.name),
                box_x,
                box_y + 4.0 + i as f32 * row_h,
                12.0,
                tc,
            ));
        }
    }
    if !skip(modal.x + pad + 14.0, input_y + 14.0) {
        let placeholder = match workspace_ui.mode {
            ComposeMode::NewChannel => "Channel name · Enter to create",
            ComposeMode::AttachPath => "Path to attach · Enter to upload",
            ComposeMode::Message => "Message #… · @mention · Enter to send",
        };
        let draft_x = modal.x + pad + 14.0;
        let draft_y = input_y + 14.0;
        if workspace_ui.draft.is_empty() {
            let mut dc = dim;
            dc[3] *= ease;
            labels.push(TextLabel::new(
                placeholder.to_string(),
                draft_x,
                draft_y,
                13.0,
                dc,
            ));
        } else {
            let mut x = draft_x;
            for seg in workspace_ui.compose_highlight_segments() {
                let c = if let Some(rgb) = seg.mention_rgb {
                    [rgb[0], rgb[1], rgb[2], 0.98 * ease]
                } else {
                    let mut b = bright;
                    b[3] *= ease;
                    b
                };
                labels.push(TextLabel::new(seg.text.clone(), x, draft_y, 13.0, c));
                x += seg.text.chars().count() as f32 * 7.5;
            }
        }
        if !workspace_ui.draft.is_empty() || caret_a > 0.02 {
            let cx = draft_x + workspace_ui.draft.chars().count() as f32 * 7.5;
            labels.push(TextLabel::new(
                CARET_BLOCK,
                cx,
                draft_y,
                13.0,
                [caret_rgb[0], caret_rgb[1], caret_rgb[2], caret_a * ease],
            ));
        }
    }
    if !workspace_ui.status.is_empty() {
        let mut sc = dim;
        sc[3] *= ease;
        let x = modal.x + pad + list_w + 10.0;
        if !skip(x, modal.y + modal.h - 20.0) {
            labels.push(TextLabel::new(
                truncate_chars(&workspace_ui.status, 48),
                x,
                modal.y + modal.h - 20.0,
                10.0,
                sc,
            ));
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
    splash: &SplashState,
    notes: &NotesState,
    workspace_ui: &WorkspaceUi,
    transfer: &TransferUi,
    rename: &RenameState,
    commands: &[Command],
    pty_active: bool,
    caret_alpha: f32,
    bright: [f32; 4],
    muted: [f32; 4],
    dim: [f32; 4],
    caret_rgb: [f32; 3],
) {
    let caret_a = caret_alpha.clamp(0.0, 1.0);
    let caret_rgba = |a: f32| -> [f32; 4] {
        [caret_rgb[0], caret_rgb[1], caret_rgb[2], a]
    };

    if splash.visible() {
        let ease = splash.content_ease().clamp(0.0, 1.0);
        let lay = SplashState::layout(win_w, win_h);
        let modal = lay.modal;
        let mut title_c = bright;
        title_c[3] *= ease;
        labels.push(TextLabel::new(
            "硯  suzuri",
            modal.x + lay.pad,
            modal.y + 16.0,
            16.0,
            title_c,
        ));
        let mut sub = dim;
        sub[3] *= ease * 0.95;
        labels.push(TextLabel::new(
            "real terminal · charm chrome",
            modal.x + lay.pad,
            modal.y + 40.0,
            11.0,
            sub,
        ));
        let hints = splash_hint_rows();
        let text_size = 12.0;
        // Shared left edges so every row’s ⌘ / labels form clean vertical columns.
        let key_x = lay.rows.first().map(|r| r.x + 12.0).unwrap_or(0.0);
        let label_x = lay
            .rows
            .first()
            .map(|r| lay.label_x(*r))
            .unwrap_or(key_x + lay.key_col_w);
        for (row, (key, label)) in lay.rows.iter().zip(hints.into_iter()) {
            let mut kc = bright;
            kc[3] *= ease;
            let mut lc = muted;
            lc[3] *= ease;
            // Keys: LEFT-aligned (not centered) — centering made short chords
            // (⌘,) sit further right than long ones (⇧⌘T).
            labels.push(TextLabel::key_left_vcenter(
                key,
                key_x,
                row.y,
                row.h,
                text_size,
                kc,
            ));
            labels.push(TextLabel::left_vcenter(
                label.to_string(),
                label_x,
                row.y,
                row.h,
                text_size,
                lc,
            ));
        }
        let mut foot = dim;
        foot[3] *= ease * 0.95;
        let cont = lay.continue_btn;
        labels.push(TextLabel::centered(
            "enter  continue",
            [cont.x, cont.y, cont.w, cont.h],
            11.0,
            foot,
        ));
    }

    if settings.visible() {
        let ease = settings.content_ease().clamp(0.0, 1.0);
        let lay = settings.layout(win_w, win_h);
        let modal = lay.modal;
        let mut title_c = bright;
        title_c[3] *= ease;
        labels.push(TextLabel::new(
            "Settings",
            modal.x + lay.pad,
            modal.y + 14.0,
            15.0,
            title_c,
        ));
        let grid = session.active_grid();
        let primary = settings.prefs.primary;
        let accent = settings.prefs.effective_accent();
        let primary_hex = crate::theme::to_hex(primary);
        let accent_hex = crate::theme::to_hex(accent);
        let primary_disp = format!("‹  {primary_hex}  ›");
        let accent_disp = if settings.prefs.accent_is_custom() {
            format!("‹  {accent_hex}  ›")
        } else {
            format!("auto · {accent_hex}")
        };
        let darken_val = format!("‹  {:.0}%  ›", settings.prefs.glass_darken * 100.0);
        use crate::settings::settings_row;
        // Booleans = glass switches; primary/accent/font/darken show values.
        let titles = [
            "Glyph rain",
            "Magnifier",
            "Primary color",
            "Accent color",
            "Font",
            "Glass darken",
            "Reset defaults",
        ];
        let font_disp = format!(
            "‹  {}  ›",
            crate::theme::font_label(&settings.prefs.font)
        );
        let text_size = 13.0;
        for (i, row) in lay.rows.iter().enumerate() {
            let mut tc = bright;
            tc[3] *= ease;
            labels.push(TextLabel::left_vcenter(
                titles[i],
                lay.label_x(*row),
                row.y,
                row.h,
                text_size,
                tc,
            ));
            if i == settings_row::PRIMARY
                || i == settings_row::ACCENT
                || i == settings_row::FONT
                || i == settings_row::DARKEN
            {
                let vc = if i == settings_row::PRIMARY {
                    [primary[0], primary[1], primary[2], 0.95 * ease]
                } else if i == settings_row::ACCENT {
                    [accent[0], accent[1], accent[2], 0.95 * ease]
                } else {
                    let mut c = muted;
                    c[3] *= ease;
                    c
                };
                let val = if i == settings_row::PRIMARY {
                    primary_disp.as_str()
                } else if i == settings_row::ACCENT {
                    accent_disp.as_str()
                } else if i == settings_row::FONT {
                    font_disp.as_str()
                } else {
                    darken_val.as_str()
                };
                let vr = lay.value_rect(*row);
                labels.push(TextLabel::centered(
                    val.to_string(),
                    [vr.x, vr.y, vr.w, vr.h],
                    text_size - if i == settings_row::ACCENT
                        && !settings.prefs.accent_is_custom()
                    {
                        1.0
                    } else {
                        0.0
                    },
                    vc,
                ));
            }
        }
        // Preset color strip — solid block glyphs on glass chips.
        // Checkmark tracks the focused color role (primary or accent).
        let focus_rgb = if settings.selected == settings_row::ACCENT {
            accent
        } else {
            primary
        };
        for (i, sw) in lay.swatches.iter().enumerate() {
            let Some(rgb) = crate::theme::COLOR_PRESETS.get(i) else {
                continue;
            };
            let selected = accent_near(*rgb, focus_rgb);
            let fill = [rgb[0], rgb[1], rgb[2], 0.98 * ease];
            labels.push(TextLabel::centered(
                "██",
                [sw.x, sw.y, sw.w, sw.h],
                18.0,
                fill,
            ));
            if selected {
                let ink = crate::theme::contrasting_text(*rgb);
                labels.push(TextLabel::centered(
                    "✓",
                    [sw.x, sw.y, sw.w, sw.h],
                    12.0,
                    [ink[0], ink[1], ink[2], 0.95 * ease],
                ));
            }
        }
        let mut foot = dim;
        foot[3] *= ease * 0.9;
        let shell = if pty_active { "PTY live" } else { "mock shell" };
        labels.push(TextLabel::new(
            format!(
                "↑↓  ·  swatch→focus  ·  ←→ hue  ·  Enter accent=auto  ·  {shell}  {}×{}",
                grid.cols(),
                grid.rows(),
            ),
            modal.x + lay.pad,
            modal.y + modal.h - 28.0,
            11.0,
            foot,
        ));
    }

    if help.visible() {
        let ease = help.content_ease().clamp(0.0, 1.0);
        let lay = HelpLayout::with_ease(win_w, win_h, ease);
        let modal = lay.modal;
        let mut title_c = bright;
        title_c[3] *= ease;
        labels.push(TextLabel::new(
            "Shortcuts",
            modal.x + lay.pad,
            modal.y + 12.0,
            15.0,
            title_c,
        ));
        for (x, y, title) in &lay.headers {
            let mut sc = muted;
            sc[3] *= ease;
            labels.push(TextLabel::new(title.to_string(), *x, *y, 11.0, sc));
        }
        let text_size = 11.0;
        for (r, keys, desc) in &lay.rows {
            let mut kc = bright;
            kc[3] *= ease;
            // Keys left, description right — both v-centered in the chip.
            let key_w = r.w * 0.52;
            let max_key = ((key_w - 16.0) / 6.5).floor().max(4.0) as usize;
            let key_draw = truncate_chars(keys, max_key);
            labels.push(TextLabel::left_vcenter(
                key_draw,
                r.x + 10.0,
                r.y,
                r.h,
                text_size,
                kc,
            ));
            let max_desc = (((r.w - key_w) - 12.0) / 6.5).floor().max(4.0) as usize;
            let desc_draw = truncate_chars(desc, max_desc);
            labels.push(TextLabel::left_vcenter(
                desc_draw,
                r.x + key_w,
                r.y,
                r.h,
                text_size,
                kc,
            ));
        }
        let mut foot = bright;
        foot[3] *= ease * 0.85;
        labels.push(TextLabel::new(
            "esc  close",
            modal.x + lay.pad,
            lay.footer_y,
            11.0,
            foot,
        ));
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
                caret_rgba(caret_a * ease),
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
                    caret_rgba(caret_a * ease),
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
                    caret_rgba(caret_a * ease),
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

    if workspace_ui.is_modal() {
        let ease = workspace_ui.content_ease().clamp(0.0, 1.0);
        push_workspace_labels(
            labels,
            workspace_ui,
            settings,
            win_w,
            win_h,
            ease,
            bright,
            muted,
            dim,
            caret_rgb,
            caret_a,
            &[],
        );
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
                caret_rgba(caret_a * ease),
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

    if rename.visible() {
        let ease = rename.content_ease().clamp(0.0, 1.0);
        let modal = rename.modal_rect(win_w, win_h);
        let pad = 14.0;
        let mut title_c = bright;
        title_c[3] *= ease;
        labels.push(TextLabel::new(
            rename.title(),
            modal.x + pad,
            modal.y + 14.0,
            15.0,
            title_c,
        ));
        let input_h = 44.0;
        let input_y = modal.y + 44.0;
        let ty = input_y + (input_h - 15.0) * 0.5;
        let empty = rename.buffer.is_empty();
        let display = if empty {
            "name…"
        } else {
            rename.buffer.as_str()
        };
        let mut qc = if empty { dim } else { bright };
        qc[3] *= ease;
        labels.push(TextLabel::new(
            format!("> {display}"),
            modal.x + pad + 12.0,
            ty,
            15.0,
            qc,
        ));
        // Caret after buffer (always show while open so empty seed is editable)
        {
            let approx = 8.2_f32;
            // "> " + buffer
            let cols = 2 + rename.buffer.chars().count();
            let caret_x = modal.x + pad + 12.0 + cols as f32 * approx;
            labels.push(TextLabel::new(
                CARET_BLOCK,
                caret_x.min(modal.x + modal.w - pad - 20.0),
                ty,
                15.0,
                caret_rgba(caret_a * ease),
            ));
        }
        let mut foot = dim;
        foot[3] *= ease * 0.9;
        labels.push(TextLabel::new(
            "enter save  ·  esc cancel  ·  empty clears custom name",
            modal.x + pad,
            modal.y + modal.h - 26.0,
            11.0,
            foot,
        ));
    }

    // Palette last — same z-order as glass (above workspace pane + other overlays).
    if palette.visible() {
        use crate::commands::PaletteState;
        let ease = palette.content_ease().clamp(0.0, 1.0);
        let modal = palette.modal_rect(win_w, win_h);
        let pad = 14.0;
        let input_h = PaletteState::INPUT_H;
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
        if !palette.query.is_empty() || caret_a > 0.02 {
            let approx = 8.2_f32;
            let caret_x = input.x + 14.0 + palette.query.chars().count() as f32 * approx;
            labels.push(TextLabel::new(
                CARET_BLOCK,
                caret_x.min(input.x + input.w - 20.0),
                ty,
                15.0,
                caret_rgba(caret_a * ease),
            ));
        }

        let filtered = filter_commands(commands, &palette.query);
        let btn_h = PaletteState::ROW_H;
        let gap = PaletteState::ROW_GAP;
        let mut y = input.y + input.h + 12.0;
        let max_y = modal.y + modal.h - pad;
        for (i, &idx) in filtered.iter().enumerate().take(PaletteState::MAX_ROWS) {
            if y + btn_h > max_y {
                break;
            }
            let c = &commands[idx];
            let mut tc = if i == palette.selected { bright } else { muted };
            tc[3] *= ease;
            let mut dc = muted;
            dc[3] *= ease;
            labels.push(TextLabel::new(
                c.title.to_string(),
                modal.x + pad + 14.0,
                y + 10.0,
                14.0,
                tc,
            ));
            let desc = truncate_chars(&c.desc, 48);
            labels.push(TextLabel::new(
                desc,
                modal.x + pad + 14.0,
                y + 28.0,
                12.0,
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
                y + 14.0,
                13.0,
                nc,
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

/// Paint terminal cells; optional selection underlay + link-hover primary tint.
///
/// # Selection highlight
/// Uses full-block mono glyphs (`█`) with theme jade at [`SELECTION_ALPHA`]
/// behind cell text — same pipeline as ANSI bg, no glass/shader fill pass.
///
/// # Link hover
/// Product `applyLinkHoverTint`: primary FG + light primary BG blend on the
/// hovered URL span. We paint a light jade underlay ([`LINK_HOVER_ALPHA`]) and
/// recolor glyphs to theme primary. Selection underlay (if any) still paints
/// first so drag-select remains visible over a hovered link.
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
    link_hover: Option<&LinkHoverSpan>,
    link_hover_rgb: [f32; 3],
    caret_rgb: [f32; 3],
    cell: MonoCellMetrics,
    cover_cards: &[crate::layout::Rect],
    dim_term: bool,
) {
    let mono_size = cell.h.max(1.0); // design size = cell height (Gohu 14)
    let cell_w = cell.w.max(1.0);
    let cell_h = cell.h.max(1.0);
    let origin_x = pl.cells.x;
    let origin_y = pl.cells.y;
    let clip = [pl.cells.x, pl.cells.y, pl.cells.w, pl.cells.h];
    let grid = &pane.grid;
    let cursor = grid.cursor();
    let alt = grid.suppress_scrollback;
    let cursor_abs = if alt {
        // Live-only: abs = live row index.
        cursor.row as usize
    } else {
        grid.cursor_abs_row()
    };
    let sel = selection.filter(|s| !s.is_empty());

    for row in 0..grid.rows() {
        let cells = if alt {
            grid.live_row_cells(row)
        } else {
            grid.visible_row_cells(row)
        };
        if cells.is_empty() {
            continue;
        }
        let abs_row = if alt {
            row as usize
        } else {
            grid.viewport_to_abs(row)
        };
        let has_content = cells.iter().any(|c| c.ch != ' ' || c.bg.is_some());
        let has_cursor = if alt {
            cursor_visible && cursor.row == row
        } else {
            cursor_visible && grid.abs_to_viewport(cursor_abs) == Some(row)
        };
        let has_selection =
            sel.is_some_and(|s| (0..grid.cols()).any(|c| s.contains(c, abs_row)));
        let has_link_hover =
            link_hover.is_some_and(|h| h.abs_row == abs_row && h.col0 < h.col1);
        if !has_content && !has_cursor && !has_selection && !has_link_hover {
            continue;
        }

        let y = origin_y + row as f32 * cell_h;
        // Never paint terminal cells into the footer / past the cells rect.
        if y + cell_h > pl.cells.y + pl.cells.h + 0.5 {
            break;
        }
        let row_rect = crate::layout::Rect::new(origin_x, y, pl.cells.w, cell_h);
        if hidden_by_cards(row_rect, cover_cards) {
            // Skip the whole row only if it sits fully inside a card; otherwise
            // per-cell clip below still paints the visible tail.
            let fully = cover_cards.iter().any(|c| {
                row_rect.x >= c.x
                    && row_rect.y >= c.y
                    && row_rect.x + row_rect.w <= c.x + c.w
                    && row_rect.y + row_rect.h <= c.y + c.h
            });
            if fully {
                continue;
            }
        }
        let dim_a = if dim_term { 0.38 } else { 1.0 };

        // Selection wash under glyphs (and under ANSI cell bg when both apply).
        if let Some(s) = sel {
            push_accent_row(
                labels,
                origin_x,
                y,
                mono_size,
                cell_w,
                grid.cols(),
                abs_row,
                selection_rgb,
                SELECTION_ALPHA,
                clip,
                |col, ar| s.contains(col, ar),
            );
        }

        // Link hover wash (lighter than selection; product primary blend).
        if let Some(h) = link_hover {
            if h.abs_row == abs_row {
                push_accent_row(
                    labels,
                    origin_x,
                    y,
                    mono_size,
                    cell_w,
                    grid.cols(),
                    abs_row,
                    link_hover_rgb,
                    LINK_HOVER_ALPHA,
                    clip,
                    |col, ar| h.contains(col, ar),
                );
            }
        }

        // Per-cell paint at fixed pitch — never run-shape a whole row (advance drift
        // against CELL_W is what made wide TUIs overflow the glass).
        for col in 0..cells.len() {
            let c = &cells[col];
            let x = origin_x + col as f32 * cell_w;
            // Skip cells that start past the clip (right overflow).
            if x >= pl.cells.x + pl.cells.w {
                break;
            }
            let cell_r = crate::layout::Rect::new(x, y, cell_w, cell_h);
            if hidden_by_cards(cell_r, cover_cards) {
                continue;
            }
            let hover_cell = link_hover.is_some_and(|h| h.contains(col as u16, abs_row));
            let fg = if hover_cell {
                link_hover_rgb
            } else {
                c.fg
            };
            if let Some(bg) = c.bg {
                labels.push(
                    TextLabel::mono(
                        "█",
                        x,
                        y,
                        mono_size,
                        [bg[0], bg[1], bg[2], 0.85 * dim_a],
                    )
                    .with_clip(clip),
                );
            }
            if c.ch != ' ' {
                labels.push(
                    TextLabel::mono(
                        c.ch.to_string(),
                        x,
                        y,
                        mono_size,
                        [fg[0], fg[1], fg[2], 0.95 * dim_a],
                    )
                    .with_clip(clip),
                );
            }
        }

        if has_cursor {
            let cx = origin_x + cursor.col as f32 * cell_w;
            let cy = origin_y + row as f32 * cell_h;
            let cr = crate::layout::Rect::new(cx, cy, cell_w, cell_h);
            if !hidden_by_cards(cr, cover_cards) {
                labels.push(
                    TextLabel::mono(
                        CARET_BLOCK,
                        cx,
                        cy,
                        mono_size,
                        [caret_rgb[0], caret_rgb[1], caret_rgb[2], 0.55 * dim_a],
                    )
                    .with_clip(clip),
                );
            }
        }
    }
}

/// Contiguous accent full-block runs for one viewport row (selection or link hover).
fn push_accent_row(
    labels: &mut Vec<TextLabel>,
    origin_x: f32,
    y: f32,
    mono_size: f32,
    cell_w: f32,
    cols: u16,
    abs_row: usize,
    accent: [f32; 3],
    alpha: f32,
    clip: [f32; 4],
    mut contains: impl FnMut(u16, usize) -> bool,
) {
    let mut col = 0u16;
    while col < cols {
        if !contains(col, abs_row) {
            col += 1;
            continue;
        }
        let x = origin_x + col as f32 * cell_w;
        labels.push(
            TextLabel::mono(
                "█",
                x,
                y,
                mono_size,
                [accent[0], accent[1], accent[2], alpha],
            )
            .with_clip(clip),
        );
        col += 1;
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
