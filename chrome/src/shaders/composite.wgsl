// Composite chrome over rain.
//
// Glass panels use the Canvas UI Glass *lens model* (GlassVanilla.ts defaults):
//   ior=1.5  edge=0.7  bevel=4  depth=250  aberration=1  blur=0  reflection=1  shine=0.01
// Adapted from a cursor lens to fixed rounded-rect UI panels sampling the rain RT.
// Source: https://github.com/DavidHDev/canvas-ui (MIT + Commons Clause) — ideas/params, not a paste of their stack.

struct FrameUniforms {
    // xy = logical size (css px), zw = framebuffer size
    size: vec4f,
    // x=time, y=dpr, z=panel_count, w=glass face darken (0..1) — all panes/chips/modal
    misc: vec4f,
    // Canvas UI glass defaults: x=ior, y=edge, z=bevel, w=depth (logical px)
    glass: vec4f,
    // x=aberration, y=blur, z=reflection, w=shine
    glass2: vec4f,
    // xy = pointer logical (top-left), z = spotlight radius (logical), w = 1 when pointer inside
    hover: vec4f,
    // Theme primary RGB (user settings) + pad — active buttons, hairlines, press wash
    primary: vec4f,
    // x=1 → transparent outside panels (cursor-follow drag chip)
    flags: vec4f,
}

struct Panel {
    rect: vec4f,   // x y w h logical
    radius: f32,
    kind: f32,
    _pad: vec2f,
    // rgb + strength (0 = no wash). Workspace chat bubbles / accent panes.
    tint: vec4f,
}

@group(0) @binding(0) var<uniform> u: FrameUniforms;
@group(0) @binding(1) var rain_tex: texture_2d<f32>;
@group(0) @binding(2) var rain_samp: sampler;
@group(0) @binding(3) var<storage, read> panels: array<Panel>;

const PI: f32 = 3.14159265358979;
const AIR_IOR: f32 = 1.0003;
const INCIDENT: vec3f = vec3f(0.0, 0.0, 1.0);
// Hard cap on panel iteration (storage array is unbounded; this is GPU loop limit).
// Help sheet alone can be ~30 frost rows + chrome + modal → need well above 32.
const MAX_PANELS: u32 = 128u;

struct VsOut {
    @builtin(position) pos: vec4f,
    @location(0) uv: vec2f,
}

@vertex
fn vs(@builtin(vertex_index) vi: u32) -> VsOut {
    var p = array<vec2f, 3>(
        vec2f(-1.0, -1.0),
        vec2f(3.0, -1.0),
        vec2f(-1.0, 3.0),
    );
    var out: VsOut;
    out.pos = vec4f(p[vi], 0.0, 1.0);
    out.uv = p[vi] * 0.5 + 0.5;
    out.uv.y = 1.0 - out.uv.y;
    return out;
}

fn pow2(x: f32) -> f32 { return x * x; }
fn pow5(x: f32) -> f32 { let x2 = x * x; return x2 * x2 * x; }

fn linear_step(e0: f32, e1: f32, x: f32) -> f32 {
    return clamp((x - e0) / (e1 - e0), 0.0, 1.0);
}

fn sd_round_box(p: vec2f, b: vec2f, r: f32) -> f32 {
    let q = abs(p) - b + vec2f(r);
    return length(max(q, vec2f(0.0))) + min(max(q.x, q.y), 0.0) - r;
}

// Polynomial smooth min — surface tension / metaball blend (Inigo Quilez).
// Larger k = stretchier bridge. Keep k small so outer edges stay mostly straight
// and corners stay true circular quarters from `sd_round_box` (not long goo arcs).
fn smin(a: f32, b: f32, k: f32) -> f32 {
    let k2 = max(k, 0.001);
    let h = clamp(0.5 + 0.5 * (b - a) / k2, 0.0, 1.0);
    return mix(b, a, h) - k2 * h * (1.0 - h);
}

fn panel_sd(px: vec2f, p: Panel) -> f32 {
    let center = p.rect.xy + p.rect.zw * 0.5;
    let half = p.rect.zw * 0.5;
    // Clamp r so corners are exact quarter-circles of radius R (never > half-side).
    let r = min(p.radius, min(half.x, half.y));
    return sd_round_box(px - center, half, r);
}

// Terminal panes + active tab (and legacy TabConnect) form one liquid glass.
fn is_blob_kind(kind: f32) -> bool {
    // Terminal=0, ChipActive=2, TabConnect=11
    return (kind < 0.5)
        || (kind > 1.5 && kind < 2.5)
        || (kind > 10.5 && kind < 11.5);
}

fn blob_blend_k(kind: f32) -> f32 {
    // Tab grows into the pane geometrically; k only smooths the join.
    // Keep this moderate so the neck stays continuous without long outer goo.
    if (kind > 1.5 && kind < 2.5) {
        return 16.0; // active tab ↔ pane (overlap + this = one surface)
    }
    if (kind > 10.5) {
        return 14.0;
    }
    return 12.0; // pane ↔ pane — straighter sides
}

/// Combined SDF for all blob glass panels (smooth-union).
fn blob_sd(px: vec2f, n: u32) -> f32 {
    var d = 1e5;
    var any = false;
    for (var i = 0u; i < MAX_PANELS; i++) {
        if (i >= n) { break; }
        let p = panels[i];
        if (!is_blob_kind(p.kind)) { continue; }
        let sd = panel_sd(px, p);
        if (!any) {
            d = sd;
            any = true;
        } else {
            d = smin(d, sd, blob_blend_k(p.kind));
        }
    }
    return d;
}

/// Hover spotlight for the **active tab lobe**.
/// Full strength through the tab body, then a long soft falloff into the pane
/// (never an abrupt mid-tab cut).
fn active_tab_spot_mask(px: vec2f, n: u32) -> f32 {
    // Continuous strength 0..1 (fades when leaving the tab, not a hard gate).
    let strength = clamp(u.hover.w, 0.0, 1.0);
    if (strength < 0.001) {
        return 0.0;
    }
    var mask = 0.0;
    for (var i = 0u; i < MAX_PANELS; i++) {
        if (i >= n) { break; }
        let p = panels[i];
        // ChipActive = 2
        if (p.kind < 1.5 || p.kind > 2.5) { continue; }
        let r = p.rect; // xy = top-left, zw = size
        let local = px - r.xy;
        // Soft X containment (slightly wider than the chip so edges still light)
        let half_w = r.z * 0.5;
        let x_ok = 1.0 - smoothstep(half_w * 1.0, half_w * 1.35, abs(local.x - half_w));
        // ChipActive rect = tab body (~32px) grown down into the well.
        // Treat the upper ~32px as the tab; fade starts near the tab bottom
        // and continues well into the pane so light melts into the glass.
        let tab_body = min(32.0, r.w * 0.85);
        // 1.0 for most of the tab → soft 0 across neck + upper pane
        let v_fade = 1.0 - smoothstep(tab_body * 0.82, tab_body + 48.0, local.y);
        let d_mouse = length(px - u.hover.xy);
        let spot = exp(-pow2(d_mouse / max(u.hover.z, 8.0)) * 2.0);
        mask = max(mask, spot * x_ok * max(v_fade, 0.0) * strength);
    }
    return mask;
}

/// Optical glass from an arbitrary 2D SDF (not just a single rounded rect).
fn eval_glass_sdf(px: vec2f, sd: f32, n: u32) -> vec4f {
    let ior = max(u.glass.x, 1.01);
    let edge = u.glass.y;
    let bevel = max(u.glass.z, 0.5);
    let aberration = u.glass2.x;
    let blur = u.glass2.y;
    let reflection = u.glass2.z;
    let shine = max(u.glass2.w, 0.08);
    let optical = 120.0;
    let depth = min(u.glass.w, max(optical * 2.2, 40.0));

    let aa = 1.5;
    let mask = 1.0 - smoothstep(-aa, 0.0, sd);
    if (mask <= 0.001) {
        return vec4f(0.0);
    }

    let edge_w = max(optical * (1.0 - clamp(edge, 0.0, 0.98)), 1.0);
    let rim = pow(linear_step(-edge_w, 0.0, sd), bevel);

    let scatter = min(blur, 1.0) * 0.02;
    let rand_angle = ign(px) * PI * 2.0;
    let flat_n = normalize(vec3f(
        sin(rand_angle) * scatter,
        cos(rand_angle) * scatter,
        1.0,
    ));
    // Finite-difference gradient of the *combined* blob SDF → continuous normals
    // across the stretchy neck (no seam between tab and pane).
    let e = 1.25;
    let grad = vec2f(
        blob_sd(px + vec2f(e, 0.0), n) - blob_sd(px - vec2f(e, 0.0), n),
        blob_sd(px + vec2f(0.0, e), n) - blob_sd(px - vec2f(0.0, e), n),
    );
    let g2 = normalize(grad + vec2f(1e-5));
    let rim_n = vec3f(g2, 0.0);
    let normal = normalize(mix(flat_n, rim_n, rim));

    let eta = AIR_IOR / ior;
    let incident = vec3f(0.0, 0.0, -1.0);
    var rv = refract(incident, normal, eta);
    if (dot(rv, rv) < 1e-8) {
        rv = vec3f(g2 * rim * 8.0, -1.0);
    }
    let z = max(abs(rv.z), 1e-4);
    let disp = rv.xy * (depth / z);

    var refracted: vec3f;
    if (aberration > 0.001) {
        let d0 = disp * (ior_for_wavelength(ior, aberration, 611.4) / ior);
        let d1 = disp * (ior_for_wavelength(ior, aberration, 549.1) / ior);
        let d2 = disp * (ior_for_wavelength(ior, aberration, 464.2) / ior);
        let r = sample_rain_raw(px + d0, blur).r;
        let g = sample_rain_raw(px + d1, blur).g;
        let b = sample_rain_raw(px + d2, blur).b;
        refracted = vec3f(r, g, b);
    } else {
        refracted = sample_rain_raw(px + disp, blur);
    }

    var glass = refracted;
    let darken = clamp(u.misc.w, 0.0, 1.0);
    glass = glass * (1.0 - darken);

    if (reflection > 0.001) {
        let n_dot_v = clamp(normal.z, 0.0, 1.0);
        let f0 = pow2((ior - AIR_IOR) / (ior + AIR_IOR));
        let fres = fresnel_schlick(n_dot_v, f0) * reflection;
        glass = mix(glass, glass * 0.94 + vec3f(0.4, 0.55, 0.5) * 0.06, clamp(fres * rim, 0.0, 0.1));
    }

    let ldot = dot(g2, normalize(vec2f(-0.6, 0.8)));
    let band = pow(rim, 1.8);
    let arcs = pow(abs(ldot), 3.0) * select(0.28, 0.5, ldot > 0.0);
    glass += band * (0.012 + arcs * 0.45) * shine;

    let outline = 1.0 - smoothstep(0.0, 1.2, abs(sd));
    glass += vec3f(0.28, 0.45, 0.38) * outline * 0.04;

    return vec4f(glass, mask);
}

// Sample rain RT. logical top-left origin → texture UV (no Y flip:
// rain is written with WebGPU top-left origin matching the scene).
fn sample_rain_raw(logical_px: vec2f, lod: f32) -> vec3f {
    let logical = max(u.size.xy, vec2f(1.0));
    var uv = logical_px / logical;
    uv = clamp(uv, vec2f(0.001), vec2f(0.999));
    return textureSampleLevel(rain_tex, rain_samp, uv, lod).rgb;
}

fn ign(v: vec2f) -> f32 {
    return fract(52.9829189 * fract(0.06711056 * v.x + 0.00583715 * v.y));
}

fn ior_for_wavelength(base_ior: f32, aberration: f32, wavelength: f32) -> f32 {
    let ab = aberration * 0.1;
    return mix(
        base_ior + ab,
        base_ior - ab,
        1.0 - pow(1.0 - linear_step(450.0, 650.0, wavelength), 4.0),
    );
}

fn fresnel_schlick(cos_theta: f32, f0: f32) -> f32 {
    return f0 + (1.0 - f0) * pow5(1.0 - cos_theta);
}

fn smith_schlick_denom(cos_theta: f32, k: f32) -> f32 {
    return cos_theta * (1.0 - k) + k;
}

fn ggx(roughness: f32, n_dot_l: f32, n_dot_v: f32, n_dot_h: f32) -> f32 {
    if (n_dot_l <= 0.0) { return 0.0; }
    let a2 = pow2(roughness);
    let d = a2 / (PI * pow2(pow2(n_dot_h) * (a2 - 1.0) + 1.0));
    let k = roughness * 0.5;
    let v = 1.0 / (smith_schlick_denom(n_dot_l, k)
        * smith_schlick_denom(clamp(n_dot_v, 0.0, 1.0), k));
    return n_dot_l * d * v;
}

/// Evaluate optical glass for one panel — same model as `lens.wgsl` (the good look).
/// Shape is rounded-rect SDF; optics match the cursor lens 1:1.
/// Returns (rgb, coverage) — coverage 0 outside.
fn eval_glass_panel(
    px: vec2f,
    center: vec2f,
    half: vec2f,
    radius: f32,
    kind: f32,
) -> vec4f {
    let ior = max(u.glass.x, 1.01);
    let edge = u.glass.y;
    let bevel = max(u.glass.z, 0.5);
    let min_half = min(half.x, half.y);
    let aberration = u.glass2.x;
    let blur = u.glass2.y;
    let reflection = u.glass2.z;
    // Match lens: gentle shine floor so rim arcs read
    let shine = max(u.glass2.w, 0.08);

    // --- Optical scale ---
    // All glass chrome (panes + nav chips) shares the lens optical scale so
    // tabs/settings/+ match terminal/warp refraction, bevel, and rim.
    // `kind` is kept for callers / future style hooks; solids never reach here.
    let _kind = kind;
    let _min_half = min_half;
    let optical = 120.0; // same as LENS_RADIUS / primary panes
    let depth = min(u.glass.w, max(optical * 2.2, 40.0));

    let local = px - center;
    let sd = sd_round_box(local, half, radius);

    let aa = 1.5;
    let mask = 1.0 - smoothstep(-aa, 0.0, sd);
    if (mask <= 0.001) {
        return vec4f(0.0);
    }

    let edge_w = max(optical * (1.0 - clamp(edge, 0.0, 0.98)), 1.0);
    let rim = pow(linear_step(-edge_w, 0.0, sd), bevel);

    // Lens-style normals: flat face z=+1, rim from SDF gradient (circle used radial g2)
    let scatter = min(blur, 1.0) * 0.02;
    let rand_angle = ign(px) * PI * 2.0;
    let flat_n = normalize(vec3f(
        sin(rand_angle) * scatter,
        cos(rand_angle) * scatter,
        1.0,
    ));
    let e = 1.0;
    let grad = vec2f(
        sd_round_box(local + vec2f(e, 0.0), half, radius)
            - sd_round_box(local - vec2f(e, 0.0), half, radius),
        sd_round_box(local + vec2f(0.0, e), half, radius)
            - sd_round_box(local - vec2f(0.0, e), half, radius),
    );
    let g2 = normalize(grad + vec2f(1e-5));
    let rim_n = vec3f(g2, 0.0);
    let normal = normalize(mix(flat_n, rim_n, rim));

    // Lens refraction: incident -Z, disp = rv.xy * (depth / |rv.z|)
    let eta = AIR_IOR / ior;
    let incident = vec3f(0.0, 0.0, -1.0);
    var rv = refract(incident, normal, eta);
    if (dot(rv, rv) < 1e-8) {
        rv = vec3f(g2 * rim * 8.0, -1.0);
    }
    let z = max(abs(rv.z), 1e-4);
    let disp = rv.xy * (depth / z);

    var refracted: vec3f;
    if (aberration > 0.001) {
        // Same 3-channel CA as lens (not the old 6-lobe panel path)
        let d0 = disp * (ior_for_wavelength(ior, aberration, 611.4) / ior);
        let d1 = disp * (ior_for_wavelength(ior, aberration, 549.1) / ior);
        let d2 = disp * (ior_for_wavelength(ior, aberration, 464.2) / ior);
        let r = sample_rain_raw(px + d0, blur).r;
        let g = sample_rain_raw(px + d1, blur).g;
        let b = sample_rain_raw(px + d2, blur).b;
        refracted = vec3f(r, g, b);
    } else {
        refracted = sample_rain_raw(px + disp, blur);
    }

    var glass = refracted;

    // Shared face darken — all optical glass (panes + nav chips + modal).
    // Nested frost fields / active buttons darken harder so they read as UI.
    // Driven by `u.misc.w` ← Rust `GLASS_DARKEN`.
    var darken = clamp(u.misc.w, 0.0, 1.0);
    // ModalFrost = 13
    if (kind > 12.5 && kind < 13.5) {
        darken = clamp(darken + 0.18, 0.0, 0.95);
    }
    // ModalButtonActive = 15 — slightly lifted primary wash under text
    if (kind > 14.5 && kind < 15.5) {
        darken = clamp(darken - 0.08, 0.0, 0.95);
    }
    glass = glass * (1.0 - darken);

    // Fresnel rim — very quiet (second pass down from chalky white)
    if (reflection > 0.001) {
        let n_dot_v = clamp(normal.z, 0.0, 1.0);
        let f0 = pow2((ior - AIR_IOR) / (ior + AIR_IOR));
        let fres = fresnel_schlick(n_dot_v, f0) * reflection;
        glass = mix(glass, glass * 0.94 + vec3f(0.4, 0.55, 0.5) * 0.06, clamp(fres * rim, 0.0, 0.1));
    }

    // Rim shine / arcs — whisper only
    let ldot = dot(g2, normalize(vec2f(-0.6, 0.8)));
    let band = pow(rim, 1.8);
    let arcs = pow(abs(ldot), 3.0) * select(0.28, 0.5, ldot > 0.0);
    glass += band * (0.012 + arcs * 0.45) * shine;

    // Crystal outline — barely-there edge
    let outline = 1.0 - smoothstep(0.0, 1.2, abs(sd));
    glass += vec3f(0.28, 0.45, 0.38) * outline * 0.04;

    return vec4f(glass, mask);
}

@fragment
fn fs(in: VsOut) -> @location(0) vec4f {
    let logical = u.size.xy;
    let px = in.uv * logical; // top-left origin

    // No title-bar fill — rain shows through; traffic lights + title text only.
    let overlay = u.flags.x > 0.5;
    var col = select(sample_rain_raw(px, 0.0), vec3f(0.0), overlay);
    var cov = select(1.0, 0.0, overlay);
    // Brand primary from settings (not hardcoded inkstone jade).
    let jade = u.primary.xyz;
    let n = u32(u.misc.z);

    // --- 1) Liquid glass blob: all panes + active tab, smooth-unioned ---
    // One SDF → continuous normals → stretchy surface tension (not stacked cubes).
    {
        let sd = blob_sd(px, n);
        let g = eval_glass_sdf(px, sd, n);
        if (g.a > 0.001) {
            var rgb = g.rgb;
            // Spotlight only on the active-tab lobe (fades out toward the pane).
            // Terminal glass never gets the button hover light.
            let tab_spot = active_tab_spot_mask(px, n);
            if (tab_spot > 0.001) {
                rgb = rgb + jade * tab_spot * 0.50 * g.a;
            }
            col = mix(col, rgb, g.a * 0.96);
            if (overlay) {
                cov = max(cov, g.a * 0.96);
            }
        }
    }

    // --- 2) Discrete overlays: traffic lights, idle chips, scrim, modal ---
    for (var i = 0u; i < MAX_PANELS; i++) {
        if (i >= n) { break; }
        let p = panels[i];
        // Skip blob members — already drawn
        if (is_blob_kind(p.kind)) { continue; }

        let r = p.rect;
        let center = r.xy + r.zw * 0.5;
        let half = r.zw * 0.5;

        // Solid traffic lights (6 close / 7 min / 8 zoom)
        if (p.kind > 5.5 && p.kind < 8.5) {
            let d = sd_round_box(px - center, half, p.radius);
            let inside = 1.0 - smoothstep(-1.0, 1.0, d);
            if (inside <= 0.001) { continue; }
            var solid = vec3f(1.0, 0.373, 0.341);
            if (p.kind > 6.5 && p.kind < 7.5) {
                solid = vec3f(0.996, 0.737, 0.180);
            } else if (p.kind > 7.5) {
                solid = vec3f(0.157, 0.784, 0.251);
            }
            let shade = 0.92 + 0.08 * smoothstep(p.radius, 0.0, length(px - center));
            col = mix(col, solid * shade, inside);
            continue;
        }

        // Scrim (9) — frosted backdrop: blurred rain + dark, not a flat black sheet.
        if (p.kind > 8.5 && p.kind < 9.5) {
            let d = sd_round_box(px - center, half, max(p.radius, 0.0));
            let inside = 1.0 - smoothstep(-1.0, 1.0, d);
            let a = clamp(p._pad.x, 0.0, 1.0) * inside;
            let frost = sample_rain_raw(px, 2.4);
            let tinted = mix(frost * 0.30, vec3f(0.02, 0.025, 0.03), 0.58);
            col = mix(col, tinted, a);
            continue;
        }

        // Hairline rule (16) — solid jade, not refractive glass
        if (p.kind > 15.5 && p.kind < 16.5) {
            let d = sd_round_box(px - center, half, max(p.radius, 0.0));
            let inside = 1.0 - smoothstep(-0.6, 0.6, d);
            if (inside > 0.001) {
                let a = clamp(p._pad.x, 0.0, 1.0) * inside * 0.55;
                col = mix(col, jade * 0.55, a);
            }
            continue;
        }

        // Idle chips / logo / + / modal / modal buttons — discrete glass
        let g = eval_glass_panel(px, center, half, p.radius, p.kind);
        if (g.a <= 0.001) { continue; }
        // Modal shell + nested frost/buttons use pad.x as opacity
        let fade = select(
            1.0,
            clamp(p._pad.x, 0.0, 1.0),
            (p.kind > 9.5 && p.kind < 11.0)
                || (p.kind > 12.5 && p.kind < 16.0),
        );
        var rgb = g.rgb;
        // idle / settings / new / caffeine / modal buttons
        let is_chip = (p.kind > 2.5 && p.kind < 5.5)
            || (p.kind > 11.5 && p.kind < 12.5)
            || (p.kind > 13.5 && p.kind < 15.5);
        if (is_chip) {
            let strength = clamp(u.hover.w, 0.0, 1.0);
            if (strength > 0.001) {
                let d_mouse = length(px - u.hover.xy);
                let spot = exp(-pow2(d_mouse / max(u.hover.z, 8.0)) * 2.4);
                rgb = rgb + jade * spot * 0.50 * g.a * strength;
            }
            let press = clamp(p._pad.y, 0.0, 1.0);
            if (press > 0.01) {
                rgb = mix(rgb, jade * 0.55 + rgb * 0.45, press * 0.65 * g.a);
            }
        }
        // Selected option button — soft primary tint
        if (p.kind > 14.5 && p.kind < 15.5) {
            rgb = mix(rgb, jade * 0.35 + rgb * 0.65, 0.35 * g.a);
        }
        // Per-panel tint (member colors / theme accent on message bubbles).
        if (p.tint.w > 0.001) {
            let t = clamp(p.tint.w, 0.0, 1.0) * g.a;
            rgb = mix(rgb, p.tint.xyz * 0.55 + rgb * 0.45, t * 0.72);
        }
        col = mix(col, rgb, g.a * 0.96 * fade);
        if (overlay) {
            cov = max(cov, g.a * 0.96 * fade);
        }
    }

    if (overlay) {
        return vec4f(col * cov, cov);
    }
    return vec4f(col, 1.0);
}
