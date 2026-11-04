//! Builds libghostty-vt, the VT engine (card #290), and links it statically.
//!
//! The ghostty commit is pinned below; bindings in src/ghostty/sys.rs are
//! generated from the same commit. The source is fetched once (a shallow git
//! fetch of that commit) and built once per target into a cache shared by
//! every checkout: RSZ_GHOSTTY_CACHE, or ~/.cache/rsz-ghostty
//! (%LOCALAPPDATA%\rsz-ghostty on Windows). Needs Zig 0.17 (ZIG, or `zig` on
//! PATH) and git the first time.
//!
//! GHOSTTY_VT_LIB=<dir holding libghostty-vt.a / ghostty-vt.lib> skips all of
//! that and links a library you built yourself.

use std::path::{Path, PathBuf};
use std::process::Command;

const GHOSTTY_COMMIT: &str = "c7f68a7f48c6a5b357689eb8c2328bc2dab5d8ed";
const ZIG_VERSION: &str = "0.17.0";

fn main() {
    println!("cargo:rerun-if-changed=build.rs");
    println!("cargo:rerun-if-env-changed=GHOSTTY_VT_LIB");
    println!("cargo:rerun-if-env-changed=RSZ_GHOSTTY_CACHE");
    println!("cargo:rerun-if-env-changed=ZIG");

    let lib_dir = match std::env::var_os("GHOSTTY_VT_LIB") {
        Some(d) => PathBuf::from(d),
        None => build_cached(),
    };
    println!("cargo:rustc-link-search=native={}", lib_dir.display());
    println!("cargo:rustc-link-lib=static=ghostty-vt");
    let target = std::env::var("TARGET").unwrap();
    if target.contains("apple") {
        println!("cargo:rustc-link-lib=c++");
    } else if target.contains("linux") {
        println!("cargo:rustc-link-lib=stdc++");
    }
}

fn cache_root() -> PathBuf {
    if let Some(d) = std::env::var_os("RSZ_GHOSTTY_CACHE") {
        return PathBuf::from(d);
    }
    if cfg!(windows) {
        if let Some(d) = std::env::var_os("LOCALAPPDATA") {
            return PathBuf::from(d).join("rsz-ghostty");
        }
    }
    let home = std::env::var_os("HOME").or_else(|| std::env::var_os("USERPROFILE")).expect("no HOME");
    PathBuf::from(home).join(".cache").join("rsz-ghostty")
}

fn run(cmd: &mut Command) {
    let st = cmd.status().unwrap_or_else(|e| panic!("{cmd:?}: {e}"));
    assert!(st.success(), "{cmd:?} failed: {st}");
}

fn zig_target(target: &str) -> Option<&'static str> {
    Some(match target {
        "aarch64-apple-darwin" => "aarch64-macos",
        "x86_64-apple-darwin" => "x86_64-macos",
        "x86_64-pc-windows-msvc" => "x86_64-windows-msvc",
        "aarch64-pc-windows-msvc" => "aarch64-windows-msvc",
        "x86_64-unknown-linux-gnu" => "x86_64-linux-gnu",
        "aarch64-unknown-linux-gnu" => "aarch64-linux-gnu",
        _ => return None,
    })
}

fn build_cached() -> PathBuf {
    let target = std::env::var("TARGET").unwrap();
    let root = cache_root();
    let out = root.join(format!("{}-{}", &GHOSTTY_COMMIT[..12], target));
    let lib = out.join("lib");
    let built = |d: &Path| d.join("libghostty-vt.a").exists() || d.join("ghostty-vt.lib").exists();
    if built(&lib) {
        return lib;
    }

    let zig = std::env::var("ZIG").unwrap_or_else(|_| "zig".into());
    let ver = Command::new(&zig).arg("version").output().unwrap_or_else(|e| panic!("libghostty-vt needs Zig {ZIG_VERSION} (set ZIG or put zig on PATH): {e}"));
    let ver = String::from_utf8_lossy(&ver.stdout).trim().to_string();
    assert!(ver == ZIG_VERSION, "libghostty-vt needs Zig {ZIG_VERSION}, found {ver} ({zig}); set ZIG to a {ZIG_VERSION} binary");

    // The source: one shallow fetch of the pinned commit.
    let src = root.join(format!("src-{}", &GHOSTTY_COMMIT[..12]));
    if !src.join("build.zig").exists() {
        let tmp = root.join(format!("src-{}.tmp-{}", &GHOSTTY_COMMIT[..12], std::process::id()));
        let _ = std::fs::remove_dir_all(&tmp);
        std::fs::create_dir_all(&tmp).unwrap();
        let git = |args: &[&str]| run(Command::new("git").current_dir(&tmp).args(args));
        git(&["init", "-q"]);
        git(&["remote", "add", "origin", "https://github.com/ghostty-org/ghostty.git"]);
        git(&["fetch", "-q", "--depth", "1", "origin", GHOSTTY_COMMIT]);
        git(&["-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"]);
        if std::fs::rename(&tmp, &src).is_err() {
            // Another build got there first.
            let _ = std::fs::remove_dir_all(&tmp);
        }
    }

    // Build into a temporary prefix, then move it into place, so a build
    // that dies halfway never looks finished.
    let tmp_out = root.join(format!("{}-{}.tmp-{}", &GHOSTTY_COMMIT[..12], target, std::process::id()));
    let _ = std::fs::remove_dir_all(&tmp_out);
    let mut cmd = Command::new(&zig);
    cmd.current_dir(&src).args(["build", "-Demit-lib-vt", "-Doptimize=ReleaseFast", "--prefix"]).arg(&tmp_out);
    // Zig builds for the host by default; name the target only when cross
    // compiling.
    if std::env::var("HOST").unwrap() != target {
        let t = zig_target(&target).unwrap_or_else(|| panic!("no Zig target for {target}"));
        cmd.arg(format!("-Dtarget={t}"));
    }
    run(&mut cmd);
    // Keep only the static library: next to a dylib, the linker would take
    // the dylib, and the app would need it shipped beside it.
    if let Ok(rd) = std::fs::read_dir(tmp_out.join("lib")) {
        for e in rd.flatten() {
            let name = e.file_name().to_string_lossy().into_owned();
            if name.contains(".dylib") || name.contains(".so") || name.ends_with(".dll") || name.ends_with(".xcframework") {
                let p = e.path();
                let _ = if p.is_dir() { std::fs::remove_dir_all(&p) } else { std::fs::remove_file(&p) };
            }
        }
    }
    if std::fs::rename(&tmp_out, &out).is_err() {
        let _ = std::fs::remove_dir_all(&tmp_out);
    }
    assert!(built(&lib), "zig build finished but {} has no libghostty-vt", lib.display());
    lib
}
