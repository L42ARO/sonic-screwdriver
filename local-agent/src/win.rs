//! Thin Win32 helpers: DPI awareness and top-level window enumeration / focus.

use windows_sys::Win32::Foundation::{HWND, LPARAM, RECT};
use windows_sys::Win32::System::Threading::{AttachThreadInput, GetCurrentThreadId};
use windows_sys::Win32::UI::HiDpi::{
    DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2, SetProcessDpiAwarenessContext,
};
use windows_sys::Win32::UI::WindowsAndMessaging::{
    BringWindowToTop, EnumWindows, GetForegroundWindow, GetWindowRect, GetWindowTextLengthW,
    GetWindowTextW, GetWindowThreadProcessId, IsIconic, IsWindowVisible, SW_RESTORE,
    SetForegroundWindow, ShowWindow,
};

/// Make this process see physical pixels, so screenshot and mouse coordinates agree
/// regardless of the display scaling setting. Must run before any window/monitor call.
pub fn enable_dpi_awareness() {
    unsafe {
        SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
    }
}

#[derive(Debug, Clone)]
pub struct TopWindow {
    pub hwnd: isize,
    pub title: String,
    pub pid: u32,
    pub minimized: bool,
    pub foreground: bool,
    pub x: i32,
    pub y: i32,
    pub w: i32,
    pub h: i32,
}

unsafe extern "system" fn enum_cb(hwnd: HWND, lparam: LPARAM) -> i32 {
    unsafe {
        let out = &mut *(lparam as *mut Vec<TopWindow>);
        if IsWindowVisible(hwnd) == 0 {
            return 1;
        }
        let len = GetWindowTextLengthW(hwnd);
        if len <= 0 {
            return 1;
        }
        let mut buf = vec![0u16; len as usize + 1];
        let n = GetWindowTextW(hwnd, buf.as_mut_ptr(), buf.len() as i32);
        let title = String::from_utf16_lossy(&buf[..n.max(0) as usize]);
        let mut r = RECT {
            left: 0,
            top: 0,
            right: 0,
            bottom: 0,
        };
        GetWindowRect(hwnd, &mut r);
        let mut pid = 0;
        GetWindowThreadProcessId(hwnd, &mut pid);
        out.push(TopWindow {
            hwnd: hwnd as isize,
            title,
            pid,
            minimized: IsIconic(hwnd) != 0,
            foreground: GetForegroundWindow() == hwnd,
            x: r.left,
            y: r.top,
            w: r.right - r.left,
            h: r.bottom - r.top,
        });
        1
    }
}

/// Visible top-level windows with a title, in z-order (front first).
pub fn list_windows() -> Vec<TopWindow> {
    let mut out: Vec<TopWindow> = Vec::new();
    unsafe {
        EnumWindows(Some(enum_cb), &mut out as *mut _ as LPARAM);
    }
    out
}

/// Try to make a window the foreground window. Windows restricts this for background
/// processes, so after the plain call fails we briefly attach to the foreground window's
/// input thread, which lifts the restriction. Returns whether it ended up in front.
pub fn focus_window(hwnd: isize) -> bool {
    unsafe {
        let h = hwnd as HWND;
        if IsIconic(h) != 0 {
            ShowWindow(h, SW_RESTORE);
        }
        if SetForegroundWindow(h) != 0 && is_foreground(hwnd) {
            return true;
        }
        let fg = GetForegroundWindow();
        let fg_thread = GetWindowThreadProcessId(fg, std::ptr::null_mut());
        let me = GetCurrentThreadId();
        if fg_thread != 0 && fg_thread != me {
            AttachThreadInput(me, fg_thread, 1);
            BringWindowToTop(h);
            SetForegroundWindow(h);
            AttachThreadInput(me, fg_thread, 0);
        }
        is_foreground(hwnd)
    }
}

pub fn is_foreground(hwnd: isize) -> bool {
    unsafe { GetForegroundWindow() as isize == hwnd }
}
