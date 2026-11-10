//! UI hang watchdog (Windows). A thread asks the main window to answer a
//! no-op message (WM_NULL through SendMessageTimeoutW) every 500 ms. When
//! the main thread has not answered for 3 s it logs "ui stall", the names
//! of the process's threads, and writes a minidump of the whole process to
//! the logs folder as hang-<time>.dmp (the newest 3 are kept, and at most
//! one is written per 10 min). When the main thread answers again it logs
//! "ui stall ended after N s". docs/hangs.md says how to read a dump.
//!
//! The dump is written by a second copy of suzuri.exe started as the dump
//! helper (`__hang-dump <pid> <path>`), not from inside this process:
//! MiniDumpWriteDump on its own process suspends the other threads and can
//! deadlock on a lock one of them holds (heap, loader), which would turn a
//! stall into a permanent hang. The watch waits up to 30 s for the helper
//! and never kills it.
//!
//! The probe never blocks the main thread: it is a message the window
//! handles whenever it next pumps. A hang that still pumps messages (a
//! nested modal loop) answers it and isn't caught. Elsewhere this is a
//! no-op.

use std::sync::atomic::{AtomicBool, Ordering};

static STOP: AtomicBool = AtomicBool::new(false);

/// `suzuri __hang-dump <pid> <path>`: the helper process that writes the
/// hang dump of `pid` (the app that started it). Hidden; not for people.
pub const DUMP_ARG: &str = "__hang-dump";

/// Whether argv is the dump helper's.
pub fn wants_dump(args: &[String]) -> bool {
    args.get(1).is_some_and(|a| a == DUMP_ARG)
}

/// The dump helper: write `<pid>`'s minidump to `<path>` (the arguments
/// after DUMP_ARG). The exit code.
#[cfg(windows)]
pub fn run_dump(args: &[String]) -> i32 {
    let (Some(pid), Some(path)) = (args.first().and_then(|p| p.parse::<u32>().ok()), args.get(1)) else { return 2 };
    match win::write_dump(pid, std::path::Path::new(path)) {
        Ok(()) => 0,
        Err(e) => {
            eprintln!("hang dump: {e}");
            1
        }
    }
}

#[cfg(not(windows))]
pub fn run_dump(_args: &[String]) -> i32 {
    2
}

/// The app is quitting: stop watching, so a slow exit isn't a stall.
pub fn stop() {
    STOP.store(true, Ordering::Release);
}

/// Start watching the main window. Call from setup (the main thread).
#[cfg(windows)]
pub fn install(app: &tauri::AppHandle) {
    use std::time::Duration;
    use tauri::Manager;
    let Some(hwnd) = app.get_webview_window("main").and_then(|w| w.hwnd().ok()) else {
        log::warn!("hang watchdog: no main window");
        return;
    };
    // SAFETY: no arguments; setup runs on the main thread.
    let ui_thread = unsafe { windows_sys::Win32::System::Threading::GetCurrentThreadId() };
    let exe = match std::env::current_exe() {
        Ok(e) => e,
        Err(e) => {
            log::warn!("hang watchdog: no exe path for the dump helper: {e}");
            return;
        }
    };
    let watch = win::Watch {
        hwnd: hwnd.0 as isize,
        ui_thread,
        dir: crate::applog::dir(),
        every: Duration::from_millis(500),
        timeout: Duration::from_millis(1500),
        stall_after: Duration::from_secs(3),
        keep: 3,
        dump_gap: Duration::from_secs(600),
        dump: std::sync::Arc::new(move |path: &std::path::Path| win::dump_by_helper(&exe, path)),
    };
    let spawned = std::thread::Builder::new().name("rsz-hang-watch".into()).spawn(move || {
        let say: win::Say = std::sync::Arc::new(|line: String| log::warn!("{line}"));
        watch.run(&STOP, &say);
    });
    if let Err(e) = spawned {
        log::warn!("hang watchdog: not started: {e}");
    }
}

#[cfg(not(windows))]
pub fn install(_app: &tauri::AppHandle) {}

#[cfg(windows)]
mod win {
    use std::io;
    use std::os::windows::io::AsRawHandle;
    use std::path::{Path, PathBuf};
    use std::sync::Arc;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::time::{Duration, Instant};

    use windows_sys::Win32::Foundation::{CloseHandle, HANDLE, HWND, INVALID_HANDLE_VALUE, LocalFree};
    use windows_sys::Win32::System::Diagnostics::Debug::{
        MINIDUMP_TYPE, MiniDumpWithHandleData, MiniDumpWithIndirectlyReferencedMemory, MiniDumpWithThreadInfo,
        MiniDumpWithUnloadedModules, MiniDumpWriteDump,
    };
    use windows_sys::Win32::System::Diagnostics::ToolHelp::{
        CreateToolhelp32Snapshot, TH32CS_SNAPTHREAD, THREADENTRY32, Thread32First, Thread32Next,
    };
    use windows_sys::Win32::System::Threading::{
        CREATE_NO_WINDOW, GetCurrentProcessId, GetThreadDescription, OpenProcess, OpenThread, PROCESS_DUP_HANDLE,
        PROCESS_QUERY_INFORMATION, PROCESS_VM_READ, THREAD_QUERY_LIMITED_INFORMATION,
    };
    use windows_sys::Win32::UI::WindowsAndMessaging::{IsWindow, SMTO_ABORTIFHUNG, SendMessageTimeoutW, WM_NULL};

    /// Every thread's stack and state, the memory their stacks point at,
    /// and the handles (what a blocked thread waits on), without the heap:
    /// a few MB.
    const DUMP: MINIDUMP_TYPE =
        MiniDumpWithThreadInfo | MiniDumpWithIndirectlyReferencedMemory | MiniDumpWithUnloadedModules | MiniDumpWithHandleData;

    /// Logs a line (log::warn! in the app).
    pub type Say = Arc<dyn Fn(String) + Send + Sync>;

    /// Writes this process's dump at a path (the helper process in the app).
    pub type Dump = Arc<dyn Fn(&Path) -> io::Result<()> + Send + Sync>;

    /// How long the watch waits for the dump helper. Past that it is left
    /// to finish on its own: killing it mid-dump could leave our threads
    /// suspended.
    const DUMP_WAIT: Duration = Duration::from_secs(30);

    /// How long the watch waits for its log lines before going on: the
    /// log can be what's stuck (a paused console under a dev build's
    /// stderr mirror), and the dump must still be written.
    const SAY_WAIT: Duration = Duration::from_secs(1);

    /// Log `lines` from a helper thread, waiting up to SAY_WAIT for them.
    fn say_all(say: &Say, lines: Vec<String>) {
        let (tx, rx) = crossbeam_channel::bounded::<()>(1);
        let say = Arc::clone(say);
        let spawned = std::thread::Builder::new().name("rsz-hang-log".into()).spawn(move || {
            for l in lines {
                say(l);
            }
            let _ = tx.send(());
        });
        if spawned.is_ok() {
            let _ = rx.recv_timeout(SAY_WAIT);
        }
    }

    pub struct Watch {
        /// The main window, as an integer so the watch can move threads.
        pub hwnd: isize,
        /// The main (UI) thread's id, marked in the thread list.
        pub ui_thread: u32,
        pub dir: PathBuf,
        pub every: Duration,
        pub timeout: Duration,
        pub stall_after: Duration,
        pub keep: usize,
        /// The least time between two dumps: a run of stalls under load
        /// gets one dump, not one per stall.
        pub dump_gap: Duration,
        pub dump: Dump,
    }

    impl Watch {
        /// Probe until `stop` or the window is gone. `say` logs a line.
        pub fn run(&self, stop: &AtomicBool, say: &Say) {
            let hwnd = self.hwnd as HWND;
            let timeout_ms = self.timeout.as_millis().min(u32::MAX as u128) as u32;
            let mut last_ok = Instant::now();
            let mut misses = 0u32;
            let mut stalled = false;
            let mut last_dump = None;
            loop {
                std::thread::sleep(self.every);
                if stop.load(Ordering::Acquire) {
                    return;
                }
                let mut res = 0usize;
                // SAFETY: plain Win32 call; an invalid handle just fails.
                let answered = unsafe { SendMessageTimeoutW(hwnd, WM_NULL, 0, 0, SMTO_ABORTIFHUNG, timeout_ms, &mut res) } != 0;
                if answered {
                    if stalled {
                        say_all(say, vec![format!("ui stall ended after {:.1} s", last_ok.elapsed().as_secs_f64())]);
                        stalled = false;
                    }
                    last_ok = Instant::now();
                    misses = 0;
                    continue;
                }
                // SAFETY: as above.
                if unsafe { IsWindow(hwnd) } == 0 || stop.load(Ordering::Acquire) {
                    return;
                }
                misses += 1;
                // Two misses in a row, so one probe that straddles a sleep
                // and resume isn't a stall.
                if !stalled && misses >= 2 && last_ok.elapsed() >= self.stall_after {
                    stalled = true;
                    self.report(last_ok.elapsed(), say, &mut last_dump);
                }
            }
        }

        fn report(&self, quiet: Duration, say: &Say, last_dump: &mut Option<Instant>) {
            let mut lines = vec![format!("ui stall: main thread not answering for {:.1} s (at {})", quiet.as_secs_f64(), crate::bridge::rfc3339_now())];
            let mut unnamed = Vec::new();
            for (id, name) in thread_names() {
                let ui = if id == self.ui_thread { " (ui)" } else { "" };
                match name {
                    Some(n) => lines.push(format!("ui stall thread {id}{ui}: {n}")),
                    None if !ui.is_empty() => lines.push(format!("ui stall thread {id}{ui}")),
                    None => unnamed.push(id.to_string()),
                }
            }
            if !unnamed.is_empty() {
                lines.push(format!("ui stall unnamed threads: {}", unnamed.join(" ")));
            }
            if let Some(at) = *last_dump
                && at.elapsed() < self.dump_gap
            {
                lines.push(format!("ui stall: no dump (the last was {} s ago)", at.elapsed().as_secs()));
                say_all(say, lines);
                return;
            }
            let name = format!("hang-{}.dmp", crate::bridge::rfc3339_now().replace(':', ""));
            let path = self.dir.join(name);
            lines.push(format!("ui stall: writing {}", path.display()));
            // The lines go out first (the dump briefly suspends every
            // thread), and a stuck log doesn't stop the dump.
            say_all(say, lines);
            *last_dump = Some(Instant::now());
            if let Err(e) = (self.dump)(&path) {
                say_all(say, vec![format!("ui stall: dump failed: {e}")]);
            }
            prune(&self.dir, self.keep);
        }
    }

    /// This process's threads: id and name (SetThreadDescription, which
    /// Rust's thread names set), if any.
    pub fn thread_names() -> Vec<(u32, Option<String>)> {
        let mut out = Vec::new();
        // SAFETY: Toolhelp walk of this process's threads; every handle
        // opened here is closed here and the description freed.
        unsafe {
            let pid = GetCurrentProcessId();
            let snap = CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0);
            if snap == INVALID_HANDLE_VALUE || snap.is_null() {
                return out;
            }
            let mut te: THREADENTRY32 = std::mem::zeroed();
            te.dwSize = size_of::<THREADENTRY32>() as u32;
            let mut ok = Thread32First(snap, &mut te) != 0;
            while ok {
                if te.th32OwnerProcessID == pid {
                    out.push((te.th32ThreadID, describe(te.th32ThreadID)));
                }
                te.dwSize = size_of::<THREADENTRY32>() as u32;
                ok = Thread32Next(snap, &mut te) != 0;
            }
            CloseHandle(snap);
        }
        out
    }

    unsafe fn describe(id: u32) -> Option<String> {
        // SAFETY: the caller's contract; see thread_names.
        unsafe {
            let h = OpenThread(THREAD_QUERY_LIMITED_INFORMATION, 0, id);
            if h.is_null() {
                return None;
            }
            let mut p: *mut u16 = std::ptr::null_mut();
            let hr = GetThreadDescription(h, &mut p);
            CloseHandle(h);
            if hr < 0 || p.is_null() {
                return None;
            }
            let mut n = 0;
            while *p.add(n) != 0 {
                n += 1;
            }
            let s = String::from_utf16_lossy(std::slice::from_raw_parts(p, n));
            LocalFree(p.cast());
            (!s.is_empty()).then_some(s)
        }
    }

    /// This process's dump at `path`, written by `exe` run as the dump
    /// helper. Waits up to DUMP_WAIT; never kills it.
    pub fn dump_by_helper(exe: &Path, path: &Path) -> io::Result<()> {
        use std::os::windows::process::CommandExt;
        use std::process::{Command, Stdio};
        let mut child = Command::new(exe)
            .arg(super::DUMP_ARG)
            .arg(std::process::id().to_string())
            .arg(path)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .creation_flags(CREATE_NO_WINDOW)
            .spawn()?;
        let end = Instant::now() + DUMP_WAIT;
        loop {
            if let Some(st) = child.try_wait()? {
                return if st.success() { Ok(()) } else { Err(io::Error::other(format!("dump helper failed ({st})"))) };
            }
            if Instant::now() >= end {
                return Err(io::Error::other(format!("dump helper still running after {} s; left to finish", DUMP_WAIT.as_secs())));
            }
            std::thread::sleep(Duration::from_millis(100));
        }
    }

    /// A minidump of process `pid` at `path`. Meant to run in another
    /// process (the dump helper): see the module docs.
    pub fn write_dump(pid: u32, path: &Path) -> io::Result<()> {
        // dbghelp is single-threaded (the tests dump from several threads).
        static DUMPING: std::sync::Mutex<()> = std::sync::Mutex::new(());
        let _one = DUMPING.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(d) = path.parent() {
            std::fs::create_dir_all(d)?;
        }
        // SAFETY: plain Win32 calls; the process handle is closed below and
        // the file handle outlives the dump call.
        unsafe {
            let h = OpenProcess(PROCESS_QUERY_INFORMATION | PROCESS_VM_READ | PROCESS_DUP_HANDLE, 0, pid);
            if h.is_null() {
                return Err(io::Error::last_os_error());
            }
            let file = match std::fs::File::create(path) {
                Ok(f) => f,
                Err(e) => {
                    CloseHandle(h);
                    return Err(e);
                }
            };
            let ok = MiniDumpWriteDump(h, pid, file.as_raw_handle() as HANDLE, DUMP, std::ptr::null(), std::ptr::null(), std::ptr::null()) != 0;
            let e = io::Error::last_os_error();
            CloseHandle(h);
            if ok {
                return Ok(());
            }
            drop(file);
            let _ = std::fs::remove_file(path);
            Err(e)
        }
    }

    /// Keep the newest `keep` hang-*.dmp in `dir` (names sort by time).
    pub fn prune(dir: &Path, keep: usize) {
        let Ok(rd) = std::fs::read_dir(dir) else { return };
        let mut dumps: Vec<PathBuf> = rd
            .flatten()
            .map(|e| e.path())
            .filter(|p| p.file_name().and_then(|n| n.to_str()).is_some_and(|n| n.starts_with("hang-") && n.ends_with(".dmp")))
            .collect();
        dumps.sort();
        let extra = dumps.len().saturating_sub(keep);
        for p in &dumps[..extra] {
            let _ = std::fs::remove_file(p);
        }
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        fn temp(tag: &str) -> PathBuf {
            let d = std::env::temp_dir().join(format!("rsz-hang-{tag}-{}", std::process::id()));
            let _ = std::fs::remove_dir_all(&d);
            std::fs::create_dir_all(&d).unwrap();
            d
        }

        /// In the tests the dump is written in-process (there is no suzuri
        /// exe to run as the helper).
        fn in_process() -> Dump {
            Arc::new(|p: &Path| write_dump(std::process::id(), p))
        }

        #[test]
        fn writes_another_process_minidump() {
            use std::os::windows::process::CommandExt;
            let d = temp("dump");
            let p = d.join("hang-test.dmp");
            let mut child = std::process::Command::new("cmd")
                .args(["/c", "ping -n 30 127.0.0.1 >nul"])
                .creation_flags(CREATE_NO_WINDOW)
                .spawn()
                .unwrap();
            // Let it finish starting (a process still loading can't be read).
            std::thread::sleep(Duration::from_millis(300));
            let r = write_dump(child.id(), &p);
            let _ = child.kill();
            let _ = child.wait();
            r.unwrap();
            let head = std::fs::read(&p).unwrap();
            assert_eq!(&head[..4], b"MDMP");
            let _ = std::fs::remove_dir_all(&d);
        }

        #[test]
        fn dump_helper_args() {
            let a = |v: &[&str]| v.iter().map(|s| s.to_string()).collect::<Vec<_>>();
            assert!(crate::hang::wants_dump(&a(&["suzuri", crate::hang::DUMP_ARG, "1", "x"])));
            assert!(!crate::hang::wants_dump(&a(&["suzuri", "version"])));
            assert_eq!(crate::hang::run_dump(&a(&["nope", "x"])), 2);
            assert_eq!(crate::hang::run_dump(&a(&[])), 2);
        }

        #[test]
        fn lists_rust_thread_names() {
            let (tx, rx) = crossbeam_channel::bounded::<()>(0);
            let t = std::thread::Builder::new().name("rsz-test-named".into()).spawn(move || rx.recv()).unwrap();
            let names = thread_names();
            tx.send(()).unwrap();
            t.join().unwrap().unwrap();
            assert!(names.iter().any(|(_, n)| n.as_deref() == Some("rsz-test-named")), "{names:?}");
        }

        #[test]
        fn keeps_the_newest_three() {
            let d = temp("prune");
            for n in ["hang-1.dmp", "hang-2.dmp", "hang-3.dmp", "hang-4.dmp", "hang-5.dmp", "suzuri.log"] {
                std::fs::write(d.join(n), b"x").unwrap();
            }
            prune(&d, 3);
            let mut left: Vec<String> = std::fs::read_dir(&d).unwrap().flatten().map(|e| e.file_name().to_string_lossy().into_owned()).collect();
            left.sort();
            assert_eq!(left, ["hang-3.dmp", "hang-4.dmp", "hang-5.dmp", "suzuri.log"]);
            let _ = std::fs::remove_dir_all(&d);
        }

        #[test]
        fn a_stuck_log_does_not_stop_the_dump() {
            let d = temp("stuck-log");
            let watch = Watch {
                hwnd: 0,
                ui_thread: 0,
                dir: d.clone(),
                every: Duration::from_millis(20),
                timeout: Duration::from_millis(100),
                stall_after: Duration::from_millis(400),
                keep: 3,
                dump_gap: Duration::from_secs(600),
                dump: in_process(),
            };
            let (release, wait) = crossbeam_channel::bounded::<()>(0);
            let say: Say = Arc::new(move |_| {
                let _ = wait.recv();
            });
            let t = Instant::now();
            watch.report(Duration::from_secs(3), &say, &mut None);
            assert!(t.elapsed() < Duration::from_secs(5));
            drop(release);
            let dumps = std::fs::read_dir(&d).unwrap().flatten().filter(|e| e.file_name().to_string_lossy().ends_with(".dmp")).count();
            assert_eq!(dumps, 1);
            let _ = std::fs::remove_dir_all(&d);
        }

        #[test]
        fn one_dump_per_gap() {
            let d = temp("gap");
            let n = Arc::new(std::sync::atomic::AtomicUsize::new(0));
            let n2 = Arc::clone(&n);
            let watch = Watch {
                hwnd: 0,
                ui_thread: 0,
                dir: d.clone(),
                every: Duration::from_millis(20),
                timeout: Duration::from_millis(100),
                stall_after: Duration::from_millis(400),
                keep: 3,
                dump_gap: Duration::from_secs(600),
                dump: Arc::new(move |_: &Path| {
                    n2.fetch_add(1, Ordering::AcqRel);
                    Ok(())
                }),
            };
            let lines = Arc::new(parking_lot::Mutex::new(Vec::<String>::new()));
            let l2 = Arc::clone(&lines);
            let say: Say = Arc::new(move |s| l2.lock().push(s));
            let mut last = None;
            watch.report(Duration::from_secs(3), &say, &mut last);
            watch.report(Duration::from_secs(3), &say, &mut last);
            assert_eq!(n.load(Ordering::Acquire), 1);
            assert!(lines.lock().iter().any(|l| l.starts_with("ui stall: no dump")), "{:?}", lines.lock());
            let _ = std::fs::remove_dir_all(&d);
        }

        /// A message-only window on its own thread pumps, stops pumping for
        /// a while, pumps again, then goes away.
        #[test]
        fn catches_a_stall_and_its_end() {
            use windows_sys::Win32::UI::WindowsAndMessaging::{
                CreateWindowExW, DestroyWindow, DispatchMessageW, HWND_MESSAGE, MSG, PM_REMOVE, PeekMessageW,
            };
            let pump = |for_: Duration| {
                let end = Instant::now() + for_;
                while Instant::now() < end {
                    // SAFETY: a zeroed MSG filled by PeekMessageW.
                    unsafe {
                        let mut m: MSG = std::mem::zeroed();
                        while PeekMessageW(&mut m, std::ptr::null_mut(), 0, 0, PM_REMOVE) != 0 {
                            DispatchMessageW(&m);
                        }
                    }
                    std::thread::sleep(Duration::from_millis(5));
                }
            };
            let (htx, hrx) = crossbeam_channel::bounded::<isize>(1);
            let ui = std::thread::spawn(move || {
                let class: Vec<u16> = "STATIC\0".encode_utf16().collect();
                // SAFETY: a message-only STATIC window owned by this thread.
                let hwnd = unsafe {
                    CreateWindowExW(0, class.as_ptr(), std::ptr::null(), 0, 0, 0, 0, 0, HWND_MESSAGE, std::ptr::null_mut(), std::ptr::null_mut(), std::ptr::null())
                };
                assert!(!hwnd.is_null());
                htx.send(hwnd as isize).unwrap();
                pump(Duration::from_millis(300));
                std::thread::sleep(Duration::from_millis(1200));
                pump(Duration::from_millis(600));
                // SAFETY: our own window, on its thread.
                unsafe { DestroyWindow(hwnd) };
            });
            let hwnd = hrx.recv().unwrap();
            let d = temp("watch");
            let watch = Watch {
                hwnd,
                ui_thread: 0,
                dir: d.clone(),
                every: Duration::from_millis(20),
                timeout: Duration::from_millis(100),
                stall_after: Duration::from_millis(400),
                keep: 3,
                dump_gap: Duration::from_secs(600),
                dump: in_process(),
            };
            let lines = Arc::new(parking_lot::Mutex::new(Vec::<String>::new()));
            let l2 = Arc::clone(&lines);
            let stop = AtomicBool::new(false);
            let say: Say = Arc::new(move |s| l2.lock().push(s));
            watch.run(&stop, &say);
            ui.join().unwrap();
            let lines = lines.lock().clone();
            assert_eq!(lines.iter().filter(|l| l.starts_with("ui stall: main thread not answering")).count(), 1, "{lines:?}");
            assert!(lines.iter().any(|l| l.starts_with("ui stall ended after")), "{lines:?}");
            assert!(lines.iter().any(|l| l.contains("rsz-hang") || l.starts_with("ui stall thread") || l.starts_with("ui stall unnamed")), "{lines:?}");
            let dumps = std::fs::read_dir(&d).unwrap().flatten().filter(|e| e.file_name().to_string_lossy().ends_with(".dmp")).count();
            assert_eq!(dumps, 1);
            let _ = std::fs::remove_dir_all(&d);
        }
    }
}
