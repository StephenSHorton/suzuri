# rsz-caffeine

Keep the computer awake: indefinitely, or for a while and then off by itself.
macOS takes an IOPMAssertion (`PreventUserIdleDisplaySleep`); Windows calls
`SetThreadExecutionState` from one thread that owns the hold. Other systems
build, and report that they have no hold. No dependencies beyond std
(`serde` feature: `Serialize` on `Status`).

```toml
[dependencies]
rsz-caffeine = { git = "https://github.com/StephenSHorton/suzuri", tag = "caffeine-v0.1.0" }
```

## Pick how your program uses it

**On its own.** The switch lives in your process; quitting drops the hold.

```rust
let c = rsz_caffeine::Caffeine::solo("my-app caffeine"); // name shows in `pmset -g assertions`
c.activate(None);        // on until turned off
c.activate(Some(15));    // on for 15 minutes, then off
c.toggle();              // off when on, else on indefinitely
c.deactivate();
```

**One switch shared with other programs.** The switch is a one-line file;
every program that opens the same path follows it. Turning it on or off in
one does it in all of them (within half a second), and the computer stays
awake while any of them is running. If the file already says on, a new
program starts holding straight away. Quitting one program leaves the others
holding; to turn it off everywhere, call `deactivate()`.

```rust
use rsz_caffeine::{Caffeine, shared_switch_path};
let path = shared_switch_path().unwrap(); // or any path your programs agree on
let c = Caffeine::shared("my-app caffeine", path);
```

`shared_switch_path()` is `~/Library/Application Support/rsz-caffeine/switch`
on macOS and `%LOCALAPPDATA%\rsz-caffeine\switch` on Windows. The file holds
`off`, `on`, or `on <unix ms>` (on until then).

## Status and changes

Every call returns a `Status { active, remaining_s, label, error }`; `label`
is `""` off, `"∞"` indefinite, else `"1h5"`, `"12m"`, `"40s"`. `error` says
the OS refused the hold or the shared file could not be written.
`on_change(|status| ...)` is called on every change, including a timer
running out and (shared) another program's change, so a UI can follow it.

`Caffeine::custom(hold, shared)` takes your own `OsHold` (a fake in tests,
or extra work around `SystemHold`).

Try it: `cargo run -p rsz-caffeine --example stay_awake -- --shared` in two
terminals, then type `on 15` or `off` in either.

Tests: `cargo test -p rsz-caffeine` (on macOS one checks the real assertion in
`pmset`; on Windows one takes and drops the real execution state).
