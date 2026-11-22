//! The action vocabulary the compute-box sends to the local-agent, and what comes back.
//!
//! All coordinates are in *screenshot space*: the pixel grid of the (possibly downscaled)
//! screenshots this agent returns. The agent maps them to physical screen pixels itself.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "snake_case")]
pub enum MouseButton {
    #[default]
    Left,
    Right,
    Middle,
}

fn one() -> u8 {
    1
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "action", rename_all = "snake_case")]
pub enum Action {
    ListRunningApplications,
    /// Only the compute-box resolves project program IDs into this executor action.
    LaunchProgram {
        #[serde(flatten)]
        installation: crate::programs::Installation,
    },
    /// Capture the screen as-is, without waiting for anything.
    Screenshot,
    /// Capture a region (screenshot-space rect) at full physical resolution.
    Zoom { x: u32, y: u32, w: u32, h: u32 },
    Click {
        x: i32,
        y: i32,
        #[serde(default)]
        button: MouseButton,
        #[serde(default = "one")]
        count: u8,
    },
    DoubleClick { x: i32, y: i32 },
    RightClick { x: i32, y: i32 },
    Move { x: i32, y: i32 },
    Drag { from: [i32; 2], to: [i32; 2] },
    /// Scroll at a position. dy > 0 scrolls down, dx > 0 scrolls right, in wheel notches.
    Scroll {
        x: i32,
        y: i32,
        #[serde(default)]
        dx: i32,
        #[serde(default)]
        dy: i32,
    },
    /// Type literal text (unicode), as a person would at the keyboard.
    Type { text: String },
    /// Press a key or chord, e.g. "enter", "tab", "ctrl+a", "alt+f4", "f3". `count` repeats it.
    Key {
        keys: String,
        #[serde(default = "one")]
        count: u8,
    },
    /// Sleep, then screenshot.
    Wait { ms: u64 },
    /// Bring the first visible window whose title contains the text to the front.
    FocusWindow { title_contains: String },
}

impl Action {
    /// One-line human description, shown on the on-screen banner.
    pub fn describe(&self) -> String {
        match self {
            Action::ListRunningApplications => "inspecting running applications".into(),
            Action::LaunchProgram { .. } => "opening configured program".into(),
            Action::Screenshot => "taking a screenshot".into(),
            Action::Zoom { x, y, w, h } => format!("zooming into {w}x{h} at ({x}, {y})"),
            Action::Click { x, y, button, count } => {
                let b = match button { MouseButton::Left => "", MouseButton::Right => "right-", MouseButton::Middle => "middle-" };
                if *count > 1 { format!("{b}clicking ({x}, {y}) x{count}") } else { format!("{b}clicking ({x}, {y})") }
            }
            Action::DoubleClick { x, y } => format!("double-clicking ({x}, {y})"),
            Action::RightClick { x, y } => format!("right-clicking ({x}, {y})"),
            Action::Move { x, y } => format!("moving the mouse to ({x}, {y})"),
            Action::Drag { from, to } => format!("dragging ({}, {}) to ({}, {})", from[0], from[1], to[0], to[1]),
            Action::Scroll { dx, dy, .. } => format!("scrolling {dx},{dy}"),
            Action::Type { text } => format!("typing \"{text}\""),
            Action::Key { keys, count } => if *count > 1 { format!("pressing {keys} x{count}") } else { format!("pressing {keys}") },
            Action::Wait { ms } => format!("waiting {ms} ms"),
            Action::FocusWindow { title_contains } => format!("focusing window \"{title_contains}\""),
        }
    }

    /// Short name used for log lines and screenshot file names.
    pub fn name(&self) -> &'static str {
        match self {
            Action::ListRunningApplications => "list_running_applications",
            Action::LaunchProgram { .. } => "launch_program",
            Action::Screenshot => "screenshot",
            Action::Zoom { .. } => "zoom",
            Action::Click { .. } => "click",
            Action::DoubleClick { .. } => "double_click",
            Action::RightClick { .. } => "right_click",
            Action::Move { .. } => "move",
            Action::Drag { .. } => "drag",
            Action::Scroll { .. } => "scroll",
            Action::Type { .. } => "type",
            Action::Key { .. } => "key",
            Action::Wait { .. } => "wait",
            Action::FocusWindow { .. } => "focus_window",
        }
    }
}

/// A PNG screenshot plus the geometry needed to interpret coordinates on it.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Shot {
    pub width: u32,
    pub height: u32,
    /// Physical pixels per screenshot pixel (1.0 when no downscaling happened).
    pub scale: f32,
    /// For zooms: the screenshot-space rect this image shows. None for full screenshots.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub region: Option<[u32; 4]>,
    #[serde(skip)]
    pub png: Vec<u8>,
    /// Where the PNG was written, when running in scripted mode.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub path: Option<String>,
    /// The PNG itself, base64-encoded, when sending results to the compute-box.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub png_base64: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WindowInfo {
    pub title: String,
    /// [x, y, w, h] in screenshot space.
    pub rect: [i32; 4],
    /// [x, y, w, h] in physical pixels.
    pub rect_physical: [i32; 4],
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ActionResult {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<serde_json::Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_code: Option<String>,
    pub ok: bool,
    pub action: String,
    pub elapsed_ms: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub screenshot: Option<Shot>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub window: Option<WindowInfo>,
    /// True when the screen was still changing when the settle timeout expired.
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub unsettled: bool,
}

impl ActionResult {
    pub fn ok(action: &Action) -> Self {
        Self {
            data: None,
            error_code: None,
            ok: true,
            action: action.name().to_string(),
            elapsed_ms: 0,
            error: None,
            screenshot: None,
            window: None,
            unsettled: false,
        }
    }

    pub fn err(action: &Action, e: impl std::fmt::Display) -> Self {
        Self {
            data: None,
            error_code: None,
            ok: false,
            action: action.name().to_string(),
            elapsed_ms: 0,
            error: Some(e.to_string()),
            screenshot: None,
            window: None,
            unsettled: false,
        }
    }
}
