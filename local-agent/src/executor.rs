//! Runs actions against the real screen and returns results with fresh screenshots.

use std::thread;
use std::time::{Duration, Instant};

use anyhow::{Result, anyhow};

use crate::input::Input;
use crate::overlay::Overlay;
use crate::protocol::{Action, ActionResult, MouseButton, WindowInfo};
use crate::screen::Screen;
use crate::win;

pub struct Executor {
    launcher: crate::programs::Launcher,
    pub screen: Screen,
    input: Input,
    /// On-screen "agent at work" indicator, when enabled.
    pub overlay: Option<Overlay>,
    /// How long to wait for the screen to stop changing after an action.
    pub settle_timeout: Duration,
}

impl Executor {
    pub fn new(max_width: u32) -> Result<Self> {
        Ok(Self {
            launcher: crate::programs::Launcher::default(),
            screen: Screen::new(max_width)?,
            input: Input::new()?,
            overlay: None,
            settle_timeout: Duration::from_millis(2500),
        })
    }

    /// Show the overlay (no-op if it is already up). Failures are reported, not fatal.
    pub fn show_overlay(&mut self, status: &str) {
        if self.overlay.is_none() {
            match Overlay::show(status) {
                Ok(o) => self.overlay = Some(o),
                Err(e) => eprintln!("overlay unavailable: {e}"),
            }
        }
    }

    /// Take the overlay down (no-op if it isn't showing).
    pub fn hide_overlay(&mut self) {
        self.overlay = None;
    }

    pub fn set_status(&self, status: &str) {
        if let Some(o) = &self.overlay {
            o.set_status(status);
        }
    }

    pub fn run(&mut self, action: &Action) -> ActionResult {
        let t = Instant::now();
        let mut r = match self.run_inner(action) {
            Ok(r) => r,
            Err(e) => {
                let mut r = ActionResult::err(action, &e);
                r.error_code = e
                    .downcast_ref::<crate::programs::AppError>()
                    .map(|e| e.code.to_string());
                r
            }
        };
        r.elapsed_ms = t.elapsed().as_millis() as u64;
        r
    }

    fn run_inner(&mut self, action: &Action) -> Result<ActionResult> {
        let mut r = ActionResult::ok(action);
        match action {
            Action::ListRunningApplications => {
                r.data = Some(crate::programs::inventory()?);
                r.screenshot = Some(self.screen.screenshot()?);
            }
            Action::LaunchProgram { installation } => {
                let window = self.launcher.open(&installation.expanded()?)?;
                self.focus(&window, &mut r)?;
            }
            Action::Screenshot => {
                r.screenshot = Some(self.screen.screenshot()?);
            }
            Action::Zoom { x, y, w, h } => {
                r.screenshot = Some(self.screen.zoom(*x, *y, *w, *h)?);
            }
            Action::Click {
                x,
                y,
                button,
                count,
            } => {
                let (px, py) = self.screen.to_physical(*x, *y);
                self.input.click(px, py, *button, *count)?;
                self.after_action(&mut r)?;
            }
            Action::DoubleClick { x, y } => {
                let (px, py) = self.screen.to_physical(*x, *y);
                self.input.click(px, py, MouseButton::Left, 2)?;
                self.after_action(&mut r)?;
            }
            Action::RightClick { x, y } => {
                let (px, py) = self.screen.to_physical(*x, *y);
                self.input.click(px, py, MouseButton::Right, 1)?;
                self.after_action(&mut r)?;
            }
            Action::Move { x, y } => {
                let (px, py) = self.screen.to_physical(*x, *y);
                self.input.move_to(px, py)?;
                self.after_action(&mut r)?;
            }
            Action::Drag { from, to } => {
                let f = self.screen.to_physical(from[0], from[1]);
                let t = self.screen.to_physical(to[0], to[1]);
                self.input.drag(f, t)?;
                self.after_action(&mut r)?;
            }
            Action::Scroll { x, y, dx, dy } => {
                let (px, py) = self.screen.to_physical(*x, *y);
                self.input.scroll(px, py, *dx, *dy)?;
                self.after_action(&mut r)?;
            }
            Action::Type { text } => {
                self.input.type_text(text)?;
                self.after_action(&mut r)?;
            }
            Action::Key { keys, count } => {
                self.input.key_chord_n(keys, *count)?;
                self.after_action(&mut r)?;
            }
            Action::Wait { ms } => {
                thread::sleep(Duration::from_millis(*ms));
                r.screenshot = Some(self.screen.screenshot()?);
            }
            Action::FocusWindow { title_contains } => {
                let needle = title_contains.to_lowercase();
                let w = win::list_windows()
                    .into_iter()
                    .find(|w| w.title.to_lowercase().contains(&needle))
                    .ok_or_else(|| {
                        anyhow!("no visible window with title containing {title_contains:?}")
                    })?;
                self.focus(&w, &mut r)?;
            }
        }
        Ok(r)
    }

    fn focus(&mut self, w: &win::TopWindow, r: &mut ActionResult) -> Result<()> {
        // Windows limits background processes from changing the foreground window.
        // An Alt press (see unlock_foreground) plus focus_window's thread trick usually
        // works; if not, do what a person would: click the window's title bar. A window
        // that is already in front (a program that just started usually is) is left alone.
        if !win::is_foreground(w.hwnd) {
            win::unlock_foreground();
        }
        if !win::focus_window(w.hwnd) {
            let (cx, cy) = (w.x + w.w / 2, w.y + 12);
            self.input.click(cx, cy, MouseButton::Left, 1)?;
            thread::sleep(Duration::from_millis(150));
            if !win::is_foreground(w.hwnd) {
                return Err(anyhow!("could not bring {:?} to the foreground", w.title));
            }
        }
        thread::sleep(Duration::from_millis(250));
        r.window = Some(WindowInfo {
            title: w.title.clone(),
            rect: self.screen.to_shot_rect(w.x, w.y, w.w, w.h),
            rect_physical: [w.x, w.y, w.w, w.h],
        });
        self.after_action(r)
    }

    /// Give the app a moment to react, wait for the screen to settle, then screenshot.
    fn after_action(&mut self, r: &mut ActionResult) -> Result<()> {
        thread::sleep(Duration::from_millis(80));
        r.unsettled = !self.screen.wait_settled(self.settle_timeout)?;
        r.screenshot = Some(self.screen.screenshot()?);
        Ok(())
    }
}
