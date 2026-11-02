//! Suzuri's Inkstone theme (suzuri internal/chrome/theme.go, themeCatalog),
//! copied verbatim: the shell ANSI-16 table plus the chrome roles the UI uses.

pub type Rgb = (u8, u8, u8);

const fn hex(v: u32) -> Rgb {
    ((v >> 16) as u8, (v >> 8) as u8, v as u8)
}

pub const VOID: Rgb = hex(0x0c0c10);
pub const TEXT: Rgb = hex(0xe8e6e3);
pub const PRIMARY: Rgb = hex(0xb8a0c8);
pub const SEL: Rgb = hex(0x2a2430);

pub const ANSI16: [Rgb; 16] = [
    hex(0x0c0c10),
    hex(0xc07070),
    hex(0x70a078),
    hex(0xc0a860),
    hex(0x7090b8),
    hex(0xa080a8),
    hex(0x60a0a0),
    hex(0xb8b6b0),
    hex(0x585858),
    hex(0xd88888),
    hex(0x90c098),
    hex(0xd8c880),
    hex(0x90b0d0),
    hex(0xc0a0c8),
    hex(0x88c0c0),
    hex(0xe8e6e3),
];

/// xterm 256-colour table entry (16..=255); 0..16 come from ANSI16.
pub fn xterm256(i: u8) -> Rgb {
    match i {
        0..=15 => ANSI16[i as usize],
        16..=231 => {
            let i = i - 16;
            let step = |v: u8| if v == 0 { 0 } else { 55 + v * 40 };
            (step(i / 36), step((i / 6) % 6), step(i % 6))
        }
        _ => {
            let v = 8 + (i - 232) * 10;
            (v, v, v)
        }
    }
}
