//! Import a guest compositor IOSurface as a wgpu texture (macOS).
//!
//! Ladybird already paints into an IOSurface. Chrome is the presenter:
//! look up the surface by id and wrap it for `guest_blit`.

#![cfg(target_os = "macos")]

use std::ffi::c_void;
use std::ptr::NonNull;

use foreign_types::ForeignType;
use objc::{msg_send, sel, sel_impl};

#[link(name = "IOSurface", kind = "framework")]
extern "C" {
    fn IOSurfaceLookup(csid: u32) -> *mut c_void;
}

#[link(name = "CoreFoundation", kind = "framework")]
extern "C" {
    fn CFRelease(cf: *const c_void);
}

/// Wrap `IOSurfaceLookup(id)` as a wgpu texture chrome can sample.
pub fn import(device: &wgpu::Device, id: u32, w: u32, h: u32) -> Option<wgpu::Texture> {
    if id == 0 || w == 0 || h == 0 {
        return None;
    }
    unsafe { import_hal(device, id, w, h) }
}

unsafe fn import_hal(device: &wgpu::Device, id: u32, w: u32, h: u32) -> Option<wgpu::Texture> {
    let surf = IOSurfaceLookup(id);
    if surf.is_null() {
        return None;
    }

    let hal_tex = device.as_hal::<wgpu::hal::api::Metal, _, _>(|hal| {
        let Some(hal) = hal else {
            return None;
        };
        let mtl = hal.raw_device().lock();
        let desc = metal::TextureDescriptor::new();
        desc.set_texture_type(metal::MTLTextureType::D2);
        desc.set_pixel_format(metal::MTLPixelFormat::BGRA8Unorm);
        desc.set_width(w as u64);
        desc.set_height(h as u64);
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
            wgpu::TextureFormat::Bgra8Unorm,
            metal::MTLTextureType::D2,
            1,
            1,
            wgpu::hal::CopyExtent {
                width: w,
                height: h,
                depth: 1,
            },
        ))
    });

    CFRelease(surf as *const c_void);

    let Some(hal_tex) = hal_tex else {
        return None;
    };
    Some(device.create_texture_from_hal::<wgpu::hal::api::Metal>(
        hal_tex,
        &wgpu::TextureDescriptor {
            label: Some("guest iosurface"),
            size: wgpu::Extent3d {
                width: w,
                height: h,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::Bgra8Unorm,
            usage: wgpu::TextureUsages::TEXTURE_BINDING,
            view_formats: &[],
        },
    ))
}
