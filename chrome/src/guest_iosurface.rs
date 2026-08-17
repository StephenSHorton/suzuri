//! Import a guest compositor IOSurface as a wgpu texture (macOS).
//!
//! `IOSurfaceLookup(id)` is null across processes. Chrome registers a
//! Mach receive port; the guest sends `IOSurfaceCreateMachPort` rights.

#![cfg(target_os = "macos")]

use std::ffi::{c_char, c_void, CString};
use std::ptr::NonNull;

use foreign_types::ForeignType;
use objc::{msg_send, sel, sel_impl};

type MachPort = u32;
type KernReturn = i32;

const KERN_SUCCESS: KernReturn = 0;
const MACH_PORT_NULL: MachPort = 0;
const MACH_PORT_RIGHT_RECEIVE: u32 = 1;
const MACH_MSG_TYPE_MAKE_SEND: u32 = 20;
const MACH_MSGH_BITS_COMPLEX: u32 = 0x8000_0000;
const MACH_RCV_MSG: u32 = 0x0000_0002;
const MACH_RCV_TIMEOUT: u32 = 0x0000_0100;
const MACH_PORT_LIMITS_INFO: i32 = 1;
const MACH_MSG_PORT_DESCRIPTOR: u8 = 0;

#[repr(C)]
struct MachMsgHeader {
    bits: u32,
    size: u32,
    remote_port: MachPort,
    local_port: MachPort,
    voucher_port: MachPort,
    id: i32,
}

#[repr(C)]
struct MachMsgBody {
    descriptor_count: u32,
}

#[repr(C)]
struct MachMsgPortDescriptor {
    name: MachPort,
    pad1: u32,
    pad2: u16,
    disposition: u8,
    type_: u8,
}

#[repr(C)]
struct MachMsgTrailer {
    trailer_type: u32,
    trailer_size: u32,
}

#[repr(C)]
struct SurfaceMsg {
    header: MachMsgHeader,
    body: MachMsgBody,
    port: MachMsgPortDescriptor,
    trailer: MachMsgTrailer,
    extra: [u8; 64],
}

#[repr(C)]
struct MachPortLimits {
    mpl_qlimit: u32,
}

#[link(name = "System", kind = "dylib")]
extern "C" {
    static bootstrap_port: MachPort;
    fn bootstrap_register(
        bp: MachPort,
        service_name: *const c_char,
        sp: MachPort,
    ) -> KernReturn;
    static mach_task_self_: MachPort;
    fn mach_port_allocate(task: MachPort, right: u32, name: *mut MachPort) -> KernReturn;
    fn mach_port_insert_right(
        task: MachPort,
        name: MachPort,
        right: MachPort,
        disposition: u32,
    ) -> KernReturn;
    fn mach_port_destroy(task: MachPort, name: MachPort) -> KernReturn;
    fn mach_port_deallocate(task: MachPort, name: MachPort) -> KernReturn;
    fn mach_port_set_attributes(
        task: MachPort,
        name: MachPort,
        flavor: i32,
        info: *mut MachPortLimits,
        count: u32,
    ) -> KernReturn;
    fn mach_msg(
        msg: *mut MachMsgHeader,
        option: u32,
        send_size: u32,
        rcv_size: u32,
        rcv_name: MachPort,
        timeout: u32,
        notify: MachPort,
    ) -> KernReturn;
}

#[link(name = "IOSurface", kind = "framework")]
extern "C" {
    fn IOSurfaceLookup(csid: u32) -> *mut c_void;
    fn IOSurfaceLookupFromMachPort(port: MachPort) -> *mut c_void;
    fn IOSurfaceGetWidth(s: *mut c_void) -> usize;
    fn IOSurfaceGetHeight(s: *mut c_void) -> usize;
}

#[link(name = "CoreFoundation", kind = "framework")]
extern "C" {
    fn CFRelease(cf: *const c_void);
}

fn clog(msg: &str) {
    if let Ok(mut f) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open("/tmp/suzuri-chrome-guest.log")
    {
        use std::io::Write;
        let _ = writeln!(
            f,
            "{} {msg}",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_secs())
                .unwrap_or(0)
        );
    }
}

/// Owned IOSurface from a Mach send-right.
pub struct SurfaceRef(*mut c_void);

impl SurfaceRef {
    pub fn width(&self) -> u32 {
        unsafe { IOSurfaceGetWidth(self.0) as u32 }
    }
    pub fn height(&self) -> u32 {
        unsafe { IOSurfaceGetHeight(self.0) as u32 }
    }
}

impl Drop for SurfaceRef {
    fn drop(&mut self) {
        if !self.0.is_null() {
            unsafe { CFRelease(self.0) };
            self.0 = std::ptr::null_mut();
        }
    }
}

/// Chrome-side receive port. Guest looks this name up and sends surfaces.
pub struct MachInbox {
    recv: MachPort,
    pub name: String,
}

impl MachInbox {
    pub fn register(name: &str) -> Option<Self> {
        unsafe {
            let mut recv = MACH_PORT_NULL;
            if mach_port_allocate(mach_task_self_, MACH_PORT_RIGHT_RECEIVE, &mut recv)
                != KERN_SUCCESS
            {
                clog("mach allocate fail");
                return None;
            }
            if mach_port_insert_right(
                mach_task_self_,
                recv,
                recv,
                MACH_MSG_TYPE_MAKE_SEND,
            ) != KERN_SUCCESS
            {
                let _ = mach_port_destroy(mach_task_self_, recv);
                clog("mach insert_right fail");
                return None;
            }
            let mut limits = MachPortLimits { mpl_qlimit: 16 };
            let _ = mach_port_set_attributes(
                mach_task_self_,
                recv,
                MACH_PORT_LIMITS_INFO,
                &mut limits,
                1,
            );
            let cname = CString::new(name).ok()?;
            let kr = bootstrap_register(bootstrap_port, cname.as_ptr(), recv);
            if kr != KERN_SUCCESS {
                let _ = mach_port_destroy(mach_task_self_, recv);
                clog(&format!("bootstrap_register {name} fail {kr}"));
                return None;
            }
            clog(&format!("bootstrap_register {name} ok"));
            Some(Self {
                recv,
                name: name.to_string(),
            })
        }
    }

    /// Drain pending surface rights; keep the newest.
    pub fn recv_latest(&self) -> Option<SurfaceRef> {
        let mut last: *mut c_void = std::ptr::null_mut();
        loop {
            let mut msg = unsafe { std::mem::zeroed::<SurfaceMsg>() };
            let kr = unsafe {
                mach_msg(
                    &mut msg.header,
                    MACH_RCV_MSG | MACH_RCV_TIMEOUT,
                    0,
                    std::mem::size_of::<SurfaceMsg>() as u32,
                    self.recv,
                    0,
                    MACH_PORT_NULL,
                )
            };
            if kr != KERN_SUCCESS {
                break;
            }
            if msg.header.bits & MACH_MSGH_BITS_COMPLEX == 0
                || msg.body.descriptor_count == 0
                || msg.port.type_ != MACH_MSG_PORT_DESCRIPTOR
            {
                continue;
            }
            let surf = unsafe { IOSurfaceLookupFromMachPort(msg.port.name) };
            unsafe {
                let _ = mach_port_deallocate(mach_task_self_, msg.port.name);
            }
            if surf.is_null() {
                continue;
            }
            if !last.is_null() {
                unsafe { CFRelease(last) };
            }
            last = surf;
        }
        if last.is_null() {
            None
        } else {
            Some(SurfaceRef(last))
        }
    }
}

impl Drop for MachInbox {
    fn drop(&mut self) {
        if self.recv != MACH_PORT_NULL {
            unsafe {
                let _ = mach_port_destroy(mach_task_self_, self.recv);
            }
            self.recv = MACH_PORT_NULL;
        }
    }
}

/// Wrap an IOSurface (Mach or lookup) as a wgpu texture chrome can sample.
pub fn import(
    device: &wgpu::Device,
    id: u32,
    w: u32,
    h: u32,
    held: Option<SurfaceRef>,
) -> Option<wgpu::Texture> {
    if w == 0 || h == 0 {
        return None;
    }
    unsafe { import_hal(device, id, w, h, held) }
}

unsafe fn import_hal(
    device: &wgpu::Device,
    id: u32,
    w: u32,
    h: u32,
    held: Option<SurfaceRef>,
) -> Option<wgpu::Texture> {
    let (surf, owned) = if let Some(held) = held {
        let p = held.0;
        std::mem::forget(held);
        (p, true)
    } else {
        if id == 0 {
            return None;
        }
        let p = IOSurfaceLookup(id);
        (p, true)
    };
    if surf.is_null() {
        clog(&format!("import null id={id} {w}x{h}"));
        return None;
    }
    let sw = IOSurfaceGetWidth(surf) as u32;
    let sh = IOSurfaceGetHeight(surf) as u32;
    // Guest sends the painted viewport. The IOSurface is often larger
    // (resize padding). Wrapping the painted size samples the top-left.
    let tw = if w > 0 && (sw == 0 || w <= sw) {
        w
    } else if sw > 0 {
        sw
    } else {
        w
    };
    let th = if h > 0 && (sh == 0 || h <= sh) {
        h
    } else if sh > 0 {
        sh
    } else {
        h
    };

    let wrap = |srgb: bool| {
        device.as_hal::<wgpu::hal::api::Metal, _, _>(|hal| {
            let Some(hal) = hal else {
                return None;
            };
            let mtl = hal.raw_device().lock();
            let desc = metal::TextureDescriptor::new();
            desc.set_texture_type(metal::MTLTextureType::D2);
            desc.set_pixel_format(if srgb {
                metal::MTLPixelFormat::BGRA8Unorm_sRGB
            } else {
                metal::MTLPixelFormat::BGRA8Unorm
            });
            desc.set_width(tw as u64);
            desc.set_height(th as u64);
            desc.set_storage_mode(metal::MTLStorageMode::Shared);
            desc.set_usage(metal::MTLTextureUsage::ShaderRead);

            let ns_tex: *mut objc::runtime::Object = msg_send![
                mtl.as_ptr(),
                newTextureWithDescriptor: desc.as_ref()
                iosurface: surf
                plane: 0u64
            ];
            let ns_tex = NonNull::new(ns_tex)?;
            let raw = metal::Texture::from_ptr(ns_tex.as_ptr() as *mut metal::MTLTexture);
            Some(wgpu::hal::metal::Device::texture_from_raw(
                raw,
                if srgb {
                    wgpu::TextureFormat::Bgra8UnormSrgb
                } else {
                    wgpu::TextureFormat::Bgra8Unorm
                },
                metal::MTLTextureType::D2,
                1,
                1,
                wgpu::hal::CopyExtent {
                    width: tw,
                    height: th,
                    depth: 1,
                },
            ))
        })
    };
    // Web pixels are sRGB bytes. Import as sRGB so the scene RT (sRGB)
    // does not encode them a second time — that wash looks like a white veil.
    let (hal_tex, fmt) = match wrap(true) {
        Some(t) => (Some(t), wgpu::TextureFormat::Bgra8UnormSrgb),
        None => (wrap(false), wgpu::TextureFormat::Bgra8Unorm),
    };

    if owned {
        CFRelease(surf as *const c_void);
    }

    let Some(hal_tex) = hal_tex else {
        clog(&format!("import metal fail id={id} {tw}x{th}"));
        return None;
    };
    clog(&format!("import ok id={id} {tw}x{th} srgb={}", fmt == wgpu::TextureFormat::Bgra8UnormSrgb));
    Some(device.create_texture_from_hal::<wgpu::hal::api::Metal>(
        hal_tex,
        &wgpu::TextureDescriptor {
            label: Some("guest iosurface"),
            size: wgpu::Extent3d {
                width: tw,
                height: th,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: fmt,
            usage: wgpu::TextureUsages::TEXTURE_BINDING,
            view_formats: &[],
        },
    ))
}
