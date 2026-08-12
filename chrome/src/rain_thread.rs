//! Off-thread rain encode so glyph rain never blocks typing.
//!
//! The UI thread only samples the last completed rain RT. This worker
//! owns a pair of rain targets, encodes the full-screen rain pass, waits
//! on the GPU, then publishes the finished view.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use crate::rain_sim::RainUniforms;

/// Latest job from the UI thread (size + uniforms + enabled).
#[derive(Clone, Copy, Debug)]
struct RainJob {
    enabled: bool,
    width: u32,
    height: u32,
    uniforms: RainUniforms,
}

/// Published rain view the composite pass samples.
struct RainFront {
    view: wgpu::TextureView,
}

pub struct RainThread {
    job: Arc<Mutex<RainJob>>,
    front: Arc<Mutex<RainFront>>,
    stop: Arc<AtomicBool>,
    join: Option<JoinHandle<()>>,
}

impl RainThread {
    pub fn spawn(
        device: wgpu::Device,
        queue: wgpu::Queue,
        pipeline: wgpu::RenderPipeline,
        bgl: wgpu::BindGroupLayout,
        atlas_view: wgpu::TextureView,
        sampler: wgpu::Sampler,
        width: u32,
        height: u32,
        seed: RainUniforms,
    ) -> Self {
        let (tex0, view0) = create_rain_target(&device, width, height);
        let (tex1, view1) = create_rain_target(&device, width, height);
        // Start with a cleared front so the first composite isn't garbage.
        clear_target(&device, &queue, &view0);

        let job = Arc::new(Mutex::new(RainJob {
            enabled: true,
            width,
            height,
            uniforms: seed,
        }));
        let front = Arc::new(Mutex::new(RainFront { view: view0.clone() }));
        let stop = Arc::new(AtomicBool::new(false));

        let job_w = Arc::clone(&job);
        let front_w = Arc::clone(&front);
        let stop_w = Arc::clone(&stop);

        let join = thread::Builder::new()
            .name("suzuri-rain".into())
            .spawn(move || {
                worker(
                    device,
                    queue,
                    pipeline,
                    bgl,
                    atlas_view,
                    sampler,
                    [tex0, tex1],
                    [view0, view1],
                    job_w,
                    front_w,
                    stop_w,
                );
            })
            .expect("spawn rain thread");

        Self {
            job,
            front,
            stop,
            join: Some(join),
        }
    }

    pub fn publish(&self, enabled: bool, width: u32, height: u32, uniforms: RainUniforms) {
        if let Ok(mut j) = self.job.lock() {
            j.enabled = enabled;
            j.width = width.max(1);
            j.height = height.max(1);
            j.uniforms = uniforms;
        }
    }

    /// Last completed rain RT (clone is cheap — wgpu view is refcounted).
    pub fn front_view(&self) -> wgpu::TextureView {
        self.front
            .lock()
            .map(|f| f.view.clone())
            .expect("rain front poisoned")
    }
}

impl Drop for RainThread {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Some(h) = self.join.take() {
            let _ = h.join();
        }
    }
}

fn worker(
    device: wgpu::Device,
    queue: wgpu::Queue,
    pipeline: wgpu::RenderPipeline,
    bgl: wgpu::BindGroupLayout,
    atlas_view: wgpu::TextureView,
    sampler: wgpu::Sampler,
    mut _tex: [wgpu::Texture; 2],
    mut view: [wgpu::TextureView; 2],
    job: Arc<Mutex<RainJob>>,
    front: Arc<Mutex<RainFront>>,
    stop: Arc<AtomicBool>,
) {
    let uniform_buf = device.create_buffer(&wgpu::BufferDescriptor {
        label: Some("rain uniforms (worker)"),
        size: std::mem::size_of::<RainUniforms>() as u64,
        usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
        mapped_at_creation: false,
    });
    let mut cur_w = 0u32;
    let mut cur_h = 0u32;
    let mut back = 1usize;
    let mut last_enabled = true;
    let start = Instant::now();

    while !stop.load(Ordering::Acquire) {
        let snap = job.lock().map(|j| *j).unwrap_or(RainJob {
            enabled: false,
            width: 1,
            height: 1,
            uniforms: RainUniforms {
                res_time: [1.0, 1.0, 7.3, 0.0],
                params: [0.0; 4],
                params2: [0.0; 4],
                params3: [0.0; 4],
                color: [0.0; 4],
                head_color: [0.0; 4],
            },
        });

        if snap.width != cur_w || snap.height != cur_h {
            let a = create_rain_target(&device, snap.width, snap.height);
            let b = create_rain_target(&device, snap.width, snap.height);
            _tex = [a.0, b.0];
            view = [a.1, b.1];
            cur_w = snap.width;
            cur_h = snap.height;
            back = 1;
            clear_target(&device, &queue, &view[0]);
            if let Ok(mut f) = front.lock() {
                f.view = view[0].clone();
            }
        }

        if !snap.enabled {
            if last_enabled {
                clear_target(&device, &queue, &view[0]);
                clear_target(&device, &queue, &view[1]);
                if let Ok(mut f) = front.lock() {
                    f.view = view[0].clone();
                }
                last_enabled = false;
            }
            thread::sleep(Duration::from_millis(40));
            continue;
        }
        last_enabled = true;

        let mut u = snap.uniforms;
        // Worker owns phase so rain keeps moving even if the UI thread is
        // busy with keys — don't wait on the caller's dt.
        u.res_time[2] = 7.3 + start.elapsed().as_secs_f32();
        queue.write_buffer(&uniform_buf, 0, bytemuck::bytes_of(&u));

        let bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("rain bg (worker)"),
            layout: &bgl,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: uniform_buf.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::TextureView(&atlas_view),
                },
                wgpu::BindGroupEntry {
                    binding: 2,
                    resource: wgpu::BindingResource::Sampler(&sampler),
                },
            ],
        });

        let mut encoder = device.create_command_encoder(&wgpu::CommandEncoderDescriptor {
            label: Some("rain worker"),
        });
        {
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("rain"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &view[back],
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
            pass.set_pipeline(&pipeline);
            pass.set_bind_group(0, &bind, &[]);
            pass.draw(0..3, 0..1);
        }
        let idx = queue.submit([encoder.finish()]);
        let _ = device.poll(wgpu::Maintain::WaitForSubmissionIndex(idx));

        if let Ok(mut f) = front.lock() {
            f.view = view[back].clone();
        }
        back = 1 - back;

        // ~45 Hz is plenty for rain; leaves the GPU for the UI thread.
        thread::sleep(Duration::from_millis(22));
    }
}

fn create_rain_target(device: &wgpu::Device, width: u32, height: u32) -> (wgpu::Texture, wgpu::TextureView) {
    let tex = device.create_texture(&wgpu::TextureDescriptor {
        label: Some("rain target (worker)"),
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

fn clear_target(device: &wgpu::Device, queue: &wgpu::Queue, view: &wgpu::TextureView) {
    let mut encoder = device.create_command_encoder(&wgpu::CommandEncoderDescriptor {
        label: Some("rain clear"),
    });
    {
        let _pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
            label: Some("rain-clear"),
            color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                view,
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
    }
    let idx = queue.submit([encoder.finish()]);
    let _ = device.poll(wgpu::Maintain::WaitForSubmissionIndex(idx));
}
