fn main() {
    println!("cargo:rerun-if-env-changed=GHOSTTY_VT_LIB");
    if std::env::var_os("CARGO_FEATURE_GHOSTTY").is_some() {
        let dir = std::env::var("GHOSTTY_VT_LIB").expect("set GHOSTTY_VT_LIB to libghostty-vt's directory");
        println!("cargo:rustc-link-search=native={dir}");
        println!("cargo:rustc-link-arg=-Wl,-rpath,{dir}");
    }
}
