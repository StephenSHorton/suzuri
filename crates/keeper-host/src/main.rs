//! `rsz-keeper-host [--keeper-host] <dir> <id>`: one kept shell, its config
//! as JSON on stdin (see the crate's docs). The `--keeper-host` word is
//! optional, so a program that starts `suzuri --keeper-host <dir> <id>` can
//! start this one with the same arguments.

fn main() {
    let mut args: Vec<String> = std::env::args().skip(1).collect();
    if args.first().is_some_and(|a| a == "--keeper-host") {
        args.remove(0);
    }
    if args.is_empty() || args[0] == "--help" || args[0] == "-h" {
        eprintln!(
            "usage: rsz-keeper-host [--keeper-host] <dir> <id>   (config as JSON on stdin)\n\
             rsz-keeper-host {}",
            env!("CARGO_PKG_VERSION")
        );
        std::process::exit(2);
    }
    if args[0] == "--version" {
        println!("rsz-keeper-host {}", env!("CARGO_PKG_VERSION"));
        return;
    }
    std::process::exit(rsz_keeper_host::host::run(&args));
}
