//! Stay-awake (☕) — product suzuri caffeine strip.
//!
//! macOS: IOPM assertion `PreventUserIdleDisplaySleep` (same as classic Caffeine).
//! Other platforms: UI state only until a host assertion is wired.

use std::time::{Duration, Instant};

/// Process-local caffeine manager.
pub struct Caffeine {
    active: bool,
    /// `None` = indefinite while active.
    until: Option<Instant>,
    #[cfg(target_os = "macos")]
    hold: Option<MacHold>,
}

impl Default for Caffeine {
    fn default() -> Self {
        Self::new()
    }
}

impl Caffeine {
    pub fn new() -> Self {
        // Product defaults caffeine **on** so long sessions don't sleep.
        let mut c = Self {
            active: false,
            until: None,
            #[cfg(target_os = "macos")]
            hold: None,
        };
        let _ = c.activate(None);
        c
    }

    pub fn active(&self) -> bool {
        self.active
    }

    /// Short strip label: `""` off, `"∞"` indefinite, or `"15m"` style remaining.
    pub fn hint(&self) -> String {
        if !self.active {
            return String::new();
        }
        match self.until {
            None => "∞".into(),
            Some(t) => {
                let left = t.saturating_duration_since(Instant::now());
                let secs = left.as_secs();
                if secs >= 3600 {
                    format!("{}h", (secs + 1800) / 3600)
                } else if secs >= 60 {
                    format!("{}m", (secs + 30) / 60)
                } else {
                    format!("{secs}s")
                }
            }
        }
    }

    /// Toggle indefinite on/off. Returns new active state.
    pub fn toggle(&mut self) -> bool {
        self.expire();
        if self.active {
            self.deactivate();
            false
        } else {
            let _ = self.activate(None);
            self.active
        }
    }

    /// Activate for `duration` (`None` = until toggled off).
    pub fn activate(&mut self, duration: Option<Duration>) -> Result<(), String> {
        self.expire();
        #[cfg(target_os = "macos")]
        {
            if self.hold.is_none() {
                self.hold = Some(MacHold::acquire().map_err(|e| e)?);
            }
        }
        self.active = true;
        self.until = duration.map(|d| Instant::now() + d);
        Ok(())
    }

    pub fn deactivate(&mut self) {
        #[cfg(target_os = "macos")]
        {
            self.hold = None; // Drop releases assertion
        }
        self.active = false;
        self.until = None;
    }

    /// Expire timed activations. Returns true if just turned off.
    pub fn tick(&mut self) -> bool {
        if !self.active {
            return false;
        }
        let Some(t) = self.until else {
            return false;
        };
        if Instant::now() < t {
            return false;
        }
        self.deactivate();
        true
    }

    fn expire(&mut self) {
        let _ = self.tick();
    }
}

impl Drop for Caffeine {
    fn drop(&mut self) {
        self.deactivate();
    }
}

#[cfg(target_os = "macos")]
struct MacHold {
    id: u32,
}

#[cfg(target_os = "macos")]
impl MacHold {
    fn acquire() -> Result<Self, String> {
        // IOPMAssertionCreateWithName("PreventUserIdleDisplaySleep", …)
        unsafe {
            let typ = cfstr("PreventUserIdleDisplaySleep");
            let reason = cfstr("suzuri caffeine");
            let mut id: u32 = 0;
            let r = IOPMAssertionCreateWithName(typ, 255, reason, &mut id);
            CFRelease(typ);
            CFRelease(reason);
            if r != 0 {
                return Err(format!("IOPMAssertionCreateWithName: 0x{r:x}"));
            }
            Ok(Self { id })
        }
    }
}

#[cfg(target_os = "macos")]
impl Drop for MacHold {
    fn drop(&mut self) {
        unsafe {
            let _ = IOPMAssertionRelease(self.id);
        }
    }
}

#[cfg(target_os = "macos")]
#[link(name = "IOKit", kind = "framework")]
#[link(name = "CoreFoundation", kind = "framework")]
extern "C" {
    fn CFStringCreateWithCString(
        alloc: *const std::ffi::c_void,
        c_str: *const i8,
        encoding: u32,
    ) -> *const std::ffi::c_void;
    fn CFRelease(cf: *const std::ffi::c_void);
    fn IOPMAssertionCreateWithName(
        assertion_type: *const std::ffi::c_void,
        level: u32,
        reason: *const std::ffi::c_void,
        id_out: *mut u32,
    ) -> i32;
    fn IOPMAssertionRelease(id: u32) -> i32;
}

#[cfg(target_os = "macos")]
unsafe fn cfstr(s: &str) -> *const std::ffi::c_void {
    // kCFStringEncodingUTF8 = 0x08000100
    let c = std::ffi::CString::new(s).unwrap_or_default();
    CFStringCreateWithCString(std::ptr::null(), c.as_ptr(), 0x0800_0100)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn toggle_roundtrip() {
        let mut c = Caffeine {
            active: false,
            until: None,
            #[cfg(target_os = "macos")]
            hold: None,
        };
        // Don't call New() (auto-on) in unit test on CI without GUI — manual state.
        assert!(!c.active());
        // activate may fail off-mac or in sandbox; still sets active flag only with hold
        let _ = c.activate(None);
        // On mac, if assertion works, active is true
        #[cfg(not(target_os = "macos"))]
        {
            assert!(c.active());
            c.deactivate();
            assert!(!c.active());
        }
    }
}
