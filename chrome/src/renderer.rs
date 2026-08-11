//! wgpu renderer: rain pass → glass composite → surface.

use std::sync::Arc;

use bytemuck::{Pod, Zeroable};
use wgpu::util::DeviceExt;
use winit::window::Window;

use crate::commands::{filter_commands, Command, HelpState, PaletteState};
use crate::input::{is_mac, traffic_light_rects};
use crate::layout::{FrameLayout, Metrics, PaneLayout, PanelInstance};
use crate::rain_atlas::RainAtlas;
use crate::rain_sim;
use crate::session::ChromeSession;
use crate::settings::SettingsState;
use crate::text::{TextLabel, TextLayer};

/// Approximate mono cell metrics used for grid resize + terminal paint.
pub const CELL_W: f32 = 7.8;
pub const CELL_H: f32 = 15.0;
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
/// Canvas UI default lens size (radius, CSS px).
const LENS_RADIUS: f32 = 120.0;
/// Canvas UI follow feel (~follow 0.2).
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

    /// Cursor glass lens (Canvas UI style).
    lens_pos: [f32; 2],
    lens_target: [f32; 2],
    lens_presence: f32,
    lens_presence_target: f32,
    pointer_inside: bool,
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
                lens: [0.0, 0.0, LENS_RADIUS, 0.0],
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
            lens_presence: 0.0,
            lens_presence_target: 0.0,
            pointer_inside: false,
            surface_format: format,
        }
    }

    /// Update cursor lens target (logical px, top-left origin).
    pub fn set_pointer(&mut self, x: f32, y: f32, inside: bool) {
        if inside && x.is_finite() && y.is_finite() {
            self.lens_target = [x, y];
            self.lens_presence_target = 1.0;
            // Snap onto first enter so it doesn't fade in from off-screen
            if !self.pointer_inside {
                self.lens_pos = [x, y];
            }
        } else {
            self.lens_presence_target = 0.0;
        }
        self.pointer_inside = inside;
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
        commands: &[Command],
        layout: &FrameLayout,
        pty_active: bool,
        // PTY block cursor in history cells; smooth caret alpha on the ❯ line.
        terminal_cursor_visible: bool,
        input_caret_alpha: f32,
        pointer: Option<(f32, f32)>,
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

        // Pointer from app every frame; lens can be disabled in prefs
        let lens_on = settings.prefs.lens;
        match pointer {
            Some((px, py)) if lens_on => self.set_pointer(px, py, true),
            _ => self.set_pointer(self.lens_target[0], self.lens_target[1], false),
        }
        if !lens_on {
            self.lens_presence_target = 0.0;
        }

        // Smooth follow (Canvas UI follow ≈ 0.2)
        let k_pos = 1.0 - (-dt * (4.0 + LENS_FOLLOW * 26.0)).exp();
        let k_pres = 1.0 - (-dt * 12.0).exp();
        self.lens_pos[0] += (self.lens_target[0] - self.lens_pos[0]) * k_pos;
        self.lens_pos[1] += (self.lens_target[1] - self.lens_pos[1]) * k_pos;
        self.lens_presence += (self.lens_presence_target - self.lens_presence) * k_pres;

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
        let mut panels = layout.glass_panels(self.metrics, active_idx, lights);
        // Modal overlays (settings / palette / help) — agility dialog presentation
        {
            use crate::layout::{PanelInstance, PanelKind, Rect};
            let any_overlay = settings.visible() || palette.visible() || help.visible();
            if any_overlay {
                let scrim_a = settings
                    .scrim_alpha()
                    .max(palette.scrim_alpha())
                    .max(help.scrim_alpha());
                panels.push(
                    PanelInstance::glass(
                        Rect::new(0.0, 0.0, logical_w, logical_h),
                        0.0,
                        PanelKind::Scrim,
                    )
                    .with_opacity(scrim_a),
                );
            }
            if settings.visible() {
                let modal_r = settings.animated_modal_rect(logical_w, logical_h);
                panels.push(
                    PanelInstance::glass(modal_r, self.metrics.radius, PanelKind::Modal)
                        .with_opacity(settings.content_ease().clamp(0.0, 1.0)),
                );
            }
            if palette.visible() {
                let modal_r = overlay_modal_rect(logical_w, logical_h, 480.0, 420.0);
                panels.push(
                    PanelInstance::glass(modal_r, self.metrics.radius, PanelKind::Modal)
                        .with_opacity(palette.content_ease().clamp(0.0, 1.0)),
                );
            }
            if help.visible() {
                let modal_r = overlay_modal_rect(logical_w, logical_h, 520.0, 480.0);
                panels.push(
                    PanelInstance::glass(modal_r, self.metrics.radius, PanelKind::Modal)
                        .with_opacity(help.content_ease().clamp(0.0, 1.0)),
                );
            }
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
                    LENS_RADIUS,
                    self.lens_presence,
                ],
                glass: [GLASS_IOR, GLASS_EDGE, GLASS_BEVEL, GLASS_DEPTH],
                glass2: [
                    GLASS_ABERRATION,
                    GLASS_BLUR,
                    GLASS_REFLECTION,
                    // Same shine floor as panes / lens.wgsl
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
            commands,
            pty_active,
            terminal_cursor_visible,
            input_caret_alpha,
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
    commands: &[Command],
    pty_active: bool,
    terminal_cursor_visible: bool,
    input_caret_alpha: f32,
) -> Vec<TextLabel> {
    let muted = [0.75, 0.90, 0.80, 0.85];
    let bright = [0.90, 1.0, 0.92, 0.95];
    let dim = [0.55, 0.70, 0.60, 0.75];

    let mut labels = Vec::with_capacity(128);

    // Window title
    let title_size = 12.0;
    let title_y = (m.title_h - title_size * 1.25) * 0.5;
    let title_x = if is_mac() {
        68.0 + m.stack()
    } else {
        m.edge()
    };
    labels.push(TextLabel::new(
        "suzuri · chrome",
        title_x,
        title_y,
        title_size,
        dim,
    ));

    labels.push(TextLabel::centered(
        "硯",
        [layout.logo.x, layout.logo.y, layout.logo.w, layout.logo.h],
        16.0,
        bright,
    ));

    let tab_size = 12.0;
    for (i, chip) in layout.tab_chips.iter().enumerate() {
        let title = session
            .tabs
            .get(i)
            .map(|t| t.title.as_str())
            .unwrap_or("?");
        let color = if i == active_idx { bright } else { dim };
        labels.push(TextLabel::centered(
            title,
            [chip.x, chip.y, chip.w, chip.h],
            tab_size,
            color,
        ));
    }

    labels.push(TextLabel::centered(
        "+",
        [
            layout.tab_new.x,
            layout.tab_new.y,
            layout.tab_new.w,
            layout.tab_new.h,
        ],
        16.0,
        dim,
    ));

    labels.push(TextLabel::centered(
        "settings",
        [
            layout.settings.x,
            layout.settings.y,
            layout.settings.w,
            layout.settings.h,
        ],
        tab_size,
        dim,
    ));

    // Every leaf pane: cells + footer
    let focus = session.focus_pane_id();
    for pl in &layout.panes {
        let Some(pane) = session.panes.get(&pl.pane_id) else {
            continue;
        };
        let show_cursor = terminal_cursor_visible && pl.pane_id == focus;
        push_pane_cells(&mut labels, pl, pane, show_cursor);

        let sep_color = [0.35, 0.55, 0.42, 0.75];
        let mono_size = 13.0;
        let path_size = 12.0;
        let cols = ((pl.divider.w / CELL_W).floor() as usize).max(4);
        let sep: String = "─".repeat(cols);
        let sep_y = pl.divider.y + (pl.divider.h - mono_size * 1.25).max(0.0) * 0.5;
        labels.push(TextLabel::mono(sep, pl.divider.x, sep_y, mono_size, sep_color));

        let path_str = crate::session::display_path(&pane.cwd);
        if !path_str.is_empty() {
            let path_y = pl.path.y + (pl.path.h - path_size * 1.25).max(0.0) * 0.5;
            labels.push(TextLabel::mono(
                path_str,
                pl.path.x,
                path_y,
                path_size,
                dim,
            ));
        }

        let draft = pane.draft.as_str();
        let warp_text = format!("❯ {draft}");
        let input_y = pl.warp.y + (pl.warp.h - mono_size * 1.25).max(0.0) * 0.5;
        let input_bright = if pl.focused { bright } else { dim };
        labels.push(TextLabel::mono(
            warp_text,
            pl.warp.x,
            input_y,
            mono_size,
            input_bright,
        ));

        if pl.focused {
            let a = input_caret_alpha.clamp(0.0, 1.0);
            if a > 0.02 {
                let caret_cols = 2 + draft.chars().count();
                let caret_x = pl.warp.x + caret_cols as f32 * CELL_W;
                labels.push(TextLabel::mono(
                    "▌",
                    caret_x,
                    input_y,
                    mono_size,
                    [0.0, 0.90, 0.46, a],
                ));
            }
        }
    }

    let win_w = layout.title.w;
    let win_h = layout.workspace.y + layout.workspace.h + m.edge();

    // Settings modal
    if settings.visible() {
        let ease = settings.content_ease().clamp(0.0, 1.0);
        let modal = settings.animated_modal_rect(win_w, win_h);
        let grid = session.active_grid();
        let lines = settings.display_lines(
            pty_active,
            grid.cols(),
            grid.rows(),
            session.tabs.len(),
        );
        push_modal_lines(&mut labels, &modal, "Settings", &lines, ease, bright, muted, dim, m);
    }

    // Command palette
    if palette.visible() {
        let ease = palette.content_ease().clamp(0.0, 1.0);
        let modal = overlay_modal_rect(win_w, win_h, 480.0, 420.0);
        let filtered = filter_commands(commands, &palette.query);
        let mut lines = Vec::new();
        lines.push(format!("❯ {}", palette.query));
        lines.push(String::new());
        for (i, &idx) in filtered.iter().enumerate().take(12) {
            let c = &commands[idx];
            let mark = if i == palette.selected { "▸ " } else { "  " };
            lines.push(format!("{mark}{}", c.title));
            lines.push(format!("    {}", c.desc));
        }
        if filtered.is_empty() {
            lines.push("  no matches".into());
        }
        push_modal_lines(
            &mut labels,
            &modal,
            "Command palette",
            &lines,
            ease,
            bright,
            muted,
            dim,
            m,
        );
    }

    // Keyboard shortcuts help
    if help.visible() {
        let ease = help.content_ease().clamp(0.0, 1.0);
        let modal = overlay_modal_rect(win_w, win_h, 520.0, 480.0);
        let mut lines = Vec::new();
        let mut last_cat = "";
        for c in commands {
            if c.category != last_cat {
                if !last_cat.is_empty() {
                    lines.push(String::new());
                }
                lines.push(c.category.to_string());
                last_cat = c.category;
            }
            lines.push(format!("  {}  —  {}", c.title, c.desc));
        }
        push_modal_lines(
            &mut labels,
            &modal,
            "Keyboard shortcuts",
            &lines,
            ease,
            bright,
            muted,
            dim,
            m,
        );
    }

    labels
}

fn push_modal_lines(
    labels: &mut Vec<TextLabel>,
    modal: &crate::layout::Rect,
    title: &str,
    lines: &[String],
    ease: f32,
    bright: [f32; 4],
    muted: [f32; 4],
    dim: [f32; 4],
    m: Metrics,
) {
    let pad = m.inset() * 2.0;
    let mut y = modal.y + pad;
    let x = modal.x + pad;
    let mut title_c = bright;
    title_c[3] *= ease;
    labels.push(TextLabel::new(title, x, y, 16.0, title_c));
    y += 28.0;
    for line in lines {
        if line.is_empty() {
            y += 8.0;
            continue;
        }
        let mut color = if line.starts_with("  ") || line.starts_with("    ") {
            dim
        } else if line.starts_with("▸") {
            bright
        } else {
            muted
        };
        color[3] *= ease;
        if color[3] < 0.02 {
            continue;
        }
        labels.push(TextLabel::new(line.clone(), x, y, 12.0, color));
        y += 16.0;
        if y > modal.y + modal.h - pad {
            break;
        }
    }
}

fn push_pane_cells(
    labels: &mut Vec<TextLabel>,
    pl: &PaneLayout,
    pane: &crate::session::Pane,
    cursor_visible: bool,
) {
    let mono_size = 13.0;
    let origin_x = pl.cells.x;
    let origin_y = pl.cells.y;
    let grid = &pane.grid;
    let cursor = grid.cursor();
    let live_view = grid.view_offset() == 0;

    for row in 0..grid.rows() {
        let cells = grid.visible_row_cells(row);
        if cells.is_empty() {
            continue;
        }
        let has_content = cells.iter().any(|c| c.ch != ' ' || c.bg.is_some());
        let has_cursor = cursor_visible && live_view && cursor.row == row;
        if !has_content && !has_cursor {
            continue;
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
            let y = origin_y + row as f32 * CELL_H;
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
                "█",
                cx,
                cy,
                mono_size,
                [0.0, 0.90, 0.46, 0.55],
            ));
        }
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
