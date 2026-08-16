//! Guests catalog modal — install / open / remove Ladybird (and later guests).

use std::sync::mpsc::{self, Receiver, TryRecvError};
use std::thread;

use crate::guest_install::{self, CatalogGuest};
use crate::guest_manifest::load_guests;
use crate::layout::Rect;

/// One row in the catalog card.
#[derive(Clone, Debug)]
pub struct GuestRow {
    pub id: String,
    pub name: String,
    pub desc: String,
    pub installed: bool,
}

/// What a click / Enter on the card should do.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum GuestClick {
    Install(String),
    Open(String),
    Remove(String),
}

/// Glass modal state (same springs as settings / help).
#[derive(Debug)]
pub struct GuestUi {
    pub open: bool,
    pub selected: usize,
    pub status: String,
    present: f32,
    present_vel: f32,
    overlay: f32,
    working: bool,
    job_rx: Option<Receiver<Result<String, String>>>,
}

impl Default for GuestUi {
    fn default() -> Self {
        Self::new()
    }
}

impl GuestUi {
    pub fn new() -> Self {
        Self {
            open: false,
            selected: 0,
            status: String::new(),
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
            working: false,
            job_rx: None,
        }
    }

    pub fn open_catalog(&mut self) {
        self.open = true;
        self.selected = 0;
        if !self.working {
            self.status.clear();
        }
    }

    pub fn close(&mut self) {
        self.open = false;
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01 || self.overlay > 0.01
    }

    pub fn present(&self) -> f32 {
        self.present.clamp(0.0, 1.0)
    }

    pub fn content_ease(&self) -> f32 {
        let t = self.present();
        t * t * (3.0 - 2.0 * t)
    }

    pub fn scrim_alpha(&self) -> f32 {
        let t = self.overlay.clamp(0.0, 1.0);
        t * t * (3.0 - 2.0 * t) * 0.50
    }

    pub fn working(&self) -> bool {
        self.working
    }

    pub fn rows(&self) -> Vec<GuestRow> {
        let installed = load_guests();
        let mut rows: Vec<GuestRow> = guest_install::catalog()
            .iter()
            .map(|c: &CatalogGuest| GuestRow {
                id: c.id.into(),
                name: c.name.into(),
                desc: c.desc.into(),
                installed: installed.iter().any(|g| g.id == c.id),
            })
            .collect();
        for g in installed {
            if rows.iter().any(|r| r.id == g.id) {
                continue;
            }
            rows.push(GuestRow {
                id: g.id,
                name: g.name,
                desc: "Installed guest".into(),
                installed: true,
            });
        }
        rows
    }

    pub fn move_selection(&mut self, delta: i32) {
        let n = self.rows().len().max(1);
        let cur = self.selected as i32 + delta;
        self.selected = cur.rem_euclid(n as i32) as usize;
    }

    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let target = if self.open { 1.0 } else { 0.0 };
        const K: f32 = 150.0;
        const C: f32 = 25.0;
        let force = -K * (self.present - target) - C * self.present_vel;
        self.present_vel += force * dt;
        self.present += self.present_vel * dt;
        if (self.present - target).abs() < 0.001 && self.present_vel.abs() < 0.01 {
            self.present = target;
            self.present_vel = 0.0;
        }
        let step = dt / 0.2;
        if self.overlay < target {
            self.overlay = (self.overlay + step).min(target);
        } else if self.overlay > target {
            self.overlay = (self.overlay - step).max(target);
        }
        self.poll_job();
    }

    pub fn start_install(&mut self, id: String) {
        if self.working {
            return;
        }
        if id != "ladybird" {
            self.status = format!("{id} has no installer yet");
            return;
        }
        self.working = true;
        self.status = "installing ladybird…".into();
        let (tx, rx) = mpsc::channel();
        self.job_rx = Some(rx);
        thread::spawn(move || {
            let r = guest_install::install_ladybird().map(|p| format!("installed · {}", p.display()));
            let _ = tx.send(r);
        });
    }

    pub fn start_remove(&mut self, id: String) {
        if self.working {
            return;
        }
        self.working = true;
        self.status = format!("removing {id}…");
        let (tx, rx) = mpsc::channel();
        self.job_rx = Some(rx);
        thread::spawn(move || {
            let r = guest_install::remove_guest(&id).map(|()| format!("removed {id}"));
            let _ = tx.send(r);
        });
    }

    fn poll_job(&mut self) {
        let Some(rx) = self.job_rx.as_ref() else {
            return;
        };
        match rx.try_recv() {
            Ok(Ok(msg)) => {
                self.status = msg;
                self.working = false;
                self.job_rx = None;
            }
            Ok(Err(e)) => {
                self.status = e;
                self.working = false;
                self.job_rx = None;
            }
            Err(TryRecvError::Empty) => {}
            Err(TryRecvError::Disconnected) => {
                self.working = false;
                self.job_rx = None;
                if self.status.ends_with('…') {
                    self.status = "stopped".into();
                }
            }
        }
    }

    pub fn layout(&self, win_w: f32, win_h: f32) -> GuestLayout {
        GuestLayout::with_ease(win_w, win_h, self.content_ease(), &self.rows(), self.selected)
    }

    pub fn try_click(&mut self, x: f32, y: f32, win_w: f32, win_h: f32) -> Option<GuestClick> {
        let lay = self.layout(win_w, win_h);
        for (i, row) in lay.rows.iter().enumerate() {
            if row.remove.contains(x, y) && row.installed {
                self.selected = i;
                return Some(GuestClick::Remove(row.id.clone()));
            }
            if row.action.contains(x, y) || row.rect.contains(x, y) {
                self.selected = i;
                return Some(if row.installed {
                    GuestClick::Open(row.id.clone())
                } else {
                    GuestClick::Install(row.id.clone())
                });
            }
        }
        None
    }
}

/// Geometry for the guests card.
#[derive(Clone, Debug)]
pub struct GuestLayout {
    pub modal: Rect,
    pub pad: f32,
    pub rows: Vec<GuestRowGeom>,
    pub footer_y: f32,
}

#[derive(Clone, Debug)]
pub struct GuestRowGeom {
    pub id: String,
    pub installed: bool,
    pub rect: Rect,
    pub action: Rect,
    pub remove: Rect,
}

impl GuestLayout {
    pub const ROW_H: f32 = 64.0;
    pub const ROW_GAP: f32 = 8.0;

    pub fn with_ease(
        win_w: f32,
        win_h: f32,
        ease: f32,
        rows: &[GuestRow],
        selected: usize,
    ) -> Self {
        let _ = selected;
        let modal = Self::animated_modal_rect(win_w, win_h, ease, rows.len());
        let pad = 16.0;
        let footer_y = modal.y + modal.h - 24.0;
        let mut y = modal.y + 48.0;
        let inner_w = (modal.w - pad * 2.0).max(40.0);
        let mut geoms = Vec::new();
        for r in rows {
            if y + Self::ROW_H > footer_y - 4.0 {
                break;
            }
            let rect = Rect::new(modal.x + pad, y, inner_w, Self::ROW_H);
            let btn_w = 72.0;
            let btn_h = 26.0;
            let by = rect.y + (rect.h - btn_h) * 0.5;
            let action = Rect::new(rect.x + rect.w - btn_w - 12.0, by, btn_w, btn_h);
            let remove = if r.installed {
                Rect::new(action.x - btn_w - 8.0, by, btn_w, btn_h)
            } else {
                Rect::new(0.0, 0.0, 0.0, 0.0)
            };
            geoms.push(GuestRowGeom {
                id: r.id.clone(),
                installed: r.installed,
                rect,
                action,
                remove,
            });
            y += Self::ROW_H + Self::ROW_GAP;
        }
        Self {
            modal,
            pad,
            rows: geoms,
            footer_y,
        }
    }

    pub fn modal_rect(win_w: f32, win_h: f32) -> Rect {
        Self::base_modal_rect(win_w, win_h, 1)
    }

    fn base_modal_rect(win_w: f32, win_h: f32, n_rows: usize) -> Rect {
        let w = (win_w - 48.0).min(520.0).max(340.0);
        let n = n_rows.max(1) as f32;
        let content = 56.0 + n * Self::ROW_H + (n - 1.0) * Self::ROW_GAP + 40.0;
        let h = content.clamp(200.0, (win_h - 80.0).min(360.0));
        Rect::new((win_w - w) * 0.5, (win_h - h) * 0.38, w, h)
    }

    pub fn animated_modal_rect(win_w: f32, win_h: f32, ease: f32, n_rows: usize) -> Rect {
        let base = Self::base_modal_rect(win_w, win_h, n_rows);
        let t = ease.clamp(0.0, 1.0);
        let sx = 0.88 + 0.12 * t;
        let sy = 0.82 + 0.18 * t;
        let y_nudge = -24.0 * (1.0 - t);
        let cx = base.x + base.w * 0.5;
        let cy = base.y + base.h * 0.5 + y_nudge;
        Rect::new(cx - base.w * sx * 0.5, cy - base.h * sy * 0.5, base.w * sx, base.h * sy)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn layout_has_a_row() {
        let ui = GuestUi::new();
        let lay = ui.layout(900.0, 700.0);
        assert!(lay.modal.w > 200.0);
        assert!(!ui.rows().is_empty());
        assert_eq!(ui.rows()[0].id, "ladybird");
    }

    #[test]
    fn spring_opens() {
        let mut ui = GuestUi::new();
        ui.open_catalog();
        for _ in 0..90 {
            ui.tick(1.0 / 60.0);
        }
        assert!(ui.present() > 0.9);
        assert!(ui.visible());
        ui.close();
        for _ in 0..90 {
            ui.tick(1.0 / 60.0);
        }
        assert!(ui.present() < 0.1);
    }
}
