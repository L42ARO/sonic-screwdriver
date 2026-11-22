//! "Agent at work" indicator: a click-through, always-on-top layered window covering the
//! primary monitor. It draws a soft, slowly pulsing glow around the screen edge and a small
//! banner near the bottom with the current status. The window is excluded from screen
//! capture, so the agent's own screenshots never contain it and settle detection is not
//! disturbed by the animation.

use std::ffi::c_void;
use std::ptr::{null, null_mut};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{mpsc, Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use windows_sys::Win32::Foundation::{HWND, LPARAM, LRESULT, POINT, RECT, SIZE, WPARAM};
use windows_sys::Win32::Graphics::Gdi::{
    CreateCompatibleDC, CreateDIBSection, CreateFontW, DeleteDC, DeleteObject, DrawTextW, GetDC,
    ReleaseDC, SelectObject, SetBkMode, SetTextColor, AC_SRC_ALPHA, AC_SRC_OVER, BITMAPINFO,
    BITMAPINFOHEADER, BLENDFUNCTION, DIB_RGB_COLORS, DT_CALCRECT, DT_END_ELLIPSIS, DT_NOPREFIX, DT_SINGLELINE,
    HBITMAP, HDC, HGDIOBJ, TRANSPARENT,
};
use windows_sys::Win32::System::LibraryLoader::GetModuleHandleW;
use windows_sys::Win32::UI::WindowsAndMessaging::{
    CreateWindowExW, DefWindowProcW, DestroyWindow, DispatchMessageW, GetSystemMetrics,
    PeekMessageW, PostQuitMessage, RegisterClassW, SetWindowDisplayAffinity, ShowWindow,
    TranslateMessage, UpdateLayeredWindow, MSG, PM_REMOVE, SM_CXSCREEN, SM_CYSCREEN,
    SW_SHOWNOACTIVATE, ULW_ALPHA, WDA_EXCLUDEFROMCAPTURE, WM_DESTROY, WM_QUIT, WNDCLASSW,
    WS_EX_LAYERED, WS_EX_NOACTIVATE, WS_EX_TOOLWINDOW, WS_EX_TOPMOST, WS_EX_TRANSPARENT,
    WS_POPUP,
};

/// Width of the glow band, physical pixels.
const BORDER: i32 = 30;
const FRAME: Duration = Duration::from_millis(40);

struct Shared {
    status: String,
    changed: bool,
}

pub struct Overlay {
    shared: Arc<Mutex<Shared>>,
    quit: Arc<AtomicBool>,
    thread: Option<JoinHandle<()>>,
}

impl Overlay {
    /// Show the overlay. Returns an error string if the window could not be created.
    pub fn show(status: &str) -> Result<Self, String> {
        let shared = Arc::new(Mutex::new(Shared { status: status.to_string(), changed: true }));
        let quit = Arc::new(AtomicBool::new(false));
        let (tx, rx) = mpsc::channel::<Result<(), String>>();
        let (s2, q2) = (shared.clone(), quit.clone());
        let thread = thread::spawn(move || run(s2, q2, tx));
        match rx.recv() {
            Ok(Ok(())) => Ok(Self { shared, quit, thread: Some(thread) }),
            Ok(Err(e)) => Err(e),
            Err(_) => Err("overlay thread died".into()),
        }
    }

    pub fn set_status(&self, status: &str) {
        let mut s = self.shared.lock().unwrap();
        if s.status != status {
            s.status = status.to_string();
            s.changed = true;
        }
    }
}

impl Drop for Overlay {
    fn drop(&mut self) {
        self.quit.store(true, Ordering::SeqCst);
        if let Some(t) = self.thread.take() {
            let _ = t.join();
        }
    }
}

fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(Some(0)).collect()
}

unsafe extern "system" fn wndproc(hwnd: HWND, msg: u32, wp: WPARAM, lp: LPARAM) -> LRESULT {
    unsafe {
        if msg == WM_DESTROY {
            PostQuitMessage(0);
            return 0;
        }
        DefWindowProcW(hwnd, msg, wp, lp)
    }
}

/// A 32-bit top-down DIB selected into a memory DC, with direct access to its pixels.
struct Dib {
    hdc: HDC,
    hbm: HBITMAP,
    old: HGDIOBJ,
    bits: *mut u32,
    w: i32,
    h: i32,
}

impl Dib {
    unsafe fn new(w: i32, h: i32) -> Option<Self> {
        unsafe {
            let screen = GetDC(null_mut());
            let hdc = CreateCompatibleDC(screen);
            ReleaseDC(null_mut(), screen);
            let mut bmi: BITMAPINFO = std::mem::zeroed();
            bmi.bmiHeader = BITMAPINFOHEADER {
                biSize: std::mem::size_of::<BITMAPINFOHEADER>() as u32,
                biWidth: w,
                biHeight: -h,
                biPlanes: 1,
                biBitCount: 32,
                biCompression: 0,
                biSizeImage: 0,
                biXPelsPerMeter: 0,
                biYPelsPerMeter: 0,
                biClrUsed: 0,
                biClrImportant: 0,
            };
            let mut bits: *mut c_void = null_mut();
            let hbm = CreateDIBSection(hdc, &bmi, DIB_RGB_COLORS, &mut bits, null_mut(), 0);
            if hbm.is_null() || bits.is_null() {
                DeleteDC(hdc);
                return None;
            }
            let old = SelectObject(hdc, hbm as HGDIOBJ);
            Some(Self { hdc, hbm, old, bits: bits as *mut u32, w, h })
        }
    }

    fn pixels(&mut self) -> &mut [u32] {
        unsafe { std::slice::from_raw_parts_mut(self.bits, (self.w * self.h) as usize) }
    }
}

impl Drop for Dib {
    fn drop(&mut self) {
        unsafe {
            SelectObject(self.hdc, self.old);
            DeleteObject(self.hbm as HGDIOBJ);
            DeleteDC(self.hdc);
        }
    }
}

/// Antialiased text rendered by GDI into a coverage mask (0..255 per pixel).
struct TextMask {
    w: i32,
    h: i32,
    a: Vec<u8>,
}

/// The tab is a fixed size; the status text inside it is cut off with "…" when too long.
const TAB_PAD: i32 = 28;
const TAB_BODY_W: i32 = 560;
const TEXT_W: i32 = TAB_BODY_W - 2 * TAB_PAD - 8 - 12;

/// The tab shape never changes, so it is computed once.
fn tab_shape() -> &'static Tab {
    static TAB: std::sync::OnceLock<Tab> = std::sync::OnceLock::new();
    TAB.get_or_init(|| build_tab(TAB_BODY_W))
}

/// Height of the tab hanging from the top edge, radius of its bottom corners, and radius of
/// the concave flares where it meets the top edge.
const TAB_H: i32 = 44;
const TAB_CORNER: f32 = 18.0;
const TAB_FLARE: i32 = 16;
const TAB_RING: f32 = 2.0;

/// Precomputed antialiased coverage of the tab: `fill` for the black body, `ring` for the
/// white outline. Covers the body plus a flare on each side.
struct Tab {
    w: i32,
    h: i32,
    body_w: i32,
    fill: Vec<f32>,
    ring: Vec<f32>,
}

/// Inside test in tab-local coordinates, where the body spans x in [0, bw] and y in [0, TAB_H].
/// Everything above the screen edge counts as inside, so the outline never runs along the
/// top edge itself and instead flows off into it.
fn tab_inside(x: f32, y: f32, bw: f32) -> bool {
    let (h, r, f) = (TAB_H as f32, TAB_CORNER, TAB_FLARE as f32);
    if y < 0.0 {
        return true;
    }
    if (0.0..=bw).contains(&x) {
        if y > h {
            return false;
        }
        if y > h - r {
            if x < r {
                return (x - r).powi(2) + (y - (h - r)).powi(2) <= r * r;
            }
            if x > bw - r {
                return (x - (bw - r)).powi(2) + (y - (h - r)).powi(2) <= r * r;
            }
        }
        return true;
    }
    if x < 0.0 && x >= -f {
        return y <= f && (x + f).powi(2) + (y - f).powi(2) >= f * f;
    }
    if x > bw && x <= bw + f {
        return y <= f && (x - bw - f).powi(2) + (y - f).powi(2) >= f * f;
    }
    false
}

fn build_tab(body_w: i32) -> Tab {
    let (w, h) = (body_w + 2 * TAB_FLARE, TAB_H + 2);
    let bw = body_w as f32;
    let off = TAB_FLARE as f32;
    let subs = [1.0 / 6.0, 0.5, 5.0 / 6.0];
    let dirs: Vec<(f32, f32)> = (0..12)
        .map(|k| {
            let a = k as f32 * std::f32::consts::TAU / 12.0;
            (a.cos() * TAB_RING, a.sin() * TAB_RING)
        })
        .collect();
    let mut fill = vec![0.0; (w * h) as usize];
    let mut ring = vec![0.0; (w * h) as usize];
    for y in 0..h {
        for x in 0..w {
            let (mut fi, mut ri) = (0, 0);
            for sy in subs {
                for sx in subs {
                    let (lx, ly) = (x as f32 + sx - off, y as f32 + sy);
                    if !tab_inside(lx, ly, bw) {
                        continue;
                    }
                    fi += 1;
                    if dirs.iter().any(|(dx, dy)| !tab_inside(lx + dx, ly + dy, bw)) {
                        ri += 1;
                    }
                }
            }
            fill[(y * w + x) as usize] = fi as f32 / 9.0;
            ring[(y * w + x) as usize] = ri as f32 / 9.0;
        }
    }
    Tab { w, h, body_w, fill, ring }
}

unsafe fn render_text(text: &str) -> TextMask {
    unsafe {
        let face = wide("Segoe UI Light");
        // height, width, escapement, orientation, weight, italic, underline, strikeout,
        // charset, out precision, clip precision, quality (4 = antialiased), pitch, face
        let font = CreateFontW(-19, 0, 0, 0, 300, 0, 0, 0, 1, 0, 0, 4, 0, face.as_ptr());
        let wtext = wide(text);
        let n = (wtext.len() - 1) as i32;

        let probe = Dib::new(1, 1).expect("probe dib");
        let old_font = SelectObject(probe.hdc, font as HGDIOBJ);
        let mut r = RECT { left: 0, top: 0, right: 0, bottom: 0 };
        DrawTextW(probe.hdc, wtext.as_ptr(), n, &mut r, DT_CALCRECT | DT_SINGLELINE | DT_NOPREFIX);
        SelectObject(probe.hdc, old_font);
        drop(probe);

        let (w, h) = (TEXT_W, (r.bottom - r.top).max(1) + 2);
        let mut dib = Dib::new(w, h).expect("text dib");
        let old_font = SelectObject(dib.hdc, font as HGDIOBJ);
        SetBkMode(dib.hdc, TRANSPARENT as i32);
        SetTextColor(dib.hdc, 0x00FF_FFFF);
        let mut rr = RECT { left: 1, top: 1, right: w, bottom: h };
        DrawTextW(dib.hdc, wtext.as_ptr(), n, &mut rr, DT_SINGLELINE | DT_NOPREFIX | DT_END_ELLIPSIS);
        SelectObject(dib.hdc, old_font);
        DeleteObject(font as HGDIOBJ);
        let a = dib.pixels().iter().map(|p| (p & 0xFF) as u8).collect();
        TextMask { w, h, a }
    }
}

/// Premultiplied "over" compositing of an (r,g,b) colour with coverage `a` onto a pixel.
#[inline]
fn over(dst: &mut u32, r: f32, g: f32, b: f32, a: f32) {
    let a = a.clamp(0.0, 1.0);
    let inv = 1.0 - a;
    let da = ((*dst >> 24) & 0xFF) as f32;
    let dr = ((*dst >> 16) & 0xFF) as f32;
    let dg = ((*dst >> 8) & 0xFF) as f32;
    let db = (*dst & 0xFF) as f32;
    let na = a * 255.0 + da * inv;
    let nr = r * a + dr * inv;
    let ng = g * a + dg * inv;
    let nb = b * a + db * inv;
    *dst = ((na as u32) << 24) | ((nr as u32) << 16) | ((ng as u32) << 8) | (nb as u32);
}



fn paint(dib: &mut Dib, t: f32, text: &TextMask) {
    let (w, h) = (dib.w, dib.h);
    let px = dib.pixels();
    px.fill(0);

    // Glow band around the edge: brightest at the edge, fading inward, pulsing slowly.
    let pulse = 0.6 + 0.4 * (0.5 + 0.5 * (t * 1.6).sin());
    let band = |x: i32, y: i32, px: &mut [u32]| {
        let d = x.min(y).min(w - 1 - x).min(h - 1 - y);
        if d >= BORDER {
            return;
        }
        let f = 1.0 - d as f32 / BORDER as f32;
        let a = 0.9 * f * f * pulse;
        over(&mut px[(y * w + x) as usize], 255.0, 255.0, 255.0, a);
    };
    for y in 0..BORDER.min(h) {
        for x in 0..w {
            band(x, y, px);
            band(x, h - 1 - y, px);
        }
    }
    for y in BORDER..(h - BORDER).max(BORDER) {
        for x in 0..BORDER.min(w) {
            band(x, y, px);
            band(w - 1 - x, y, px);
        }
    }

    // Banner: a black tab hanging from the top edge, with a white outline that flows into
    // the edge glow, a breathing dot, and the status text.
    let tab = tab_shape();
    let tx0 = (w - tab.body_w) / 2 - TAB_FLARE;
    for y in 0..tab.h.min(h) {
        for x in 0..tab.w {
            let sx = tx0 + x;
            if sx < 0 || sx >= w {
                continue;
            }
            let i = (y * tab.w + x) as usize;
            let (fill, ring) = (tab.fill[i], tab.ring[i]);
            let d = &mut px[(y * w + sx) as usize];
            if fill > 0.0 {
                over(d, 0.0, 0.0, 0.0, 0.92 * fill);
            }
            if ring > 0.0 {
                over(d, 255.0, 255.0, 255.0, ring);
            }
        }
    }
    let body_x0 = tx0 + TAB_FLARE;
    let gx = body_x0 + TAB_PAD;
    let cy = (tab.h - 4) / 2 + 1;
    let (dx, dy) = ((gx + 4) as f32, cy as f32);
    let dot_r = 4.0;
    let mix = 0.5 + 0.5 * (t * 1.6).sin();
    let dot_alpha = 0.35 + 0.65 * mix;
    for y in (dy - dot_r - 1.0) as i32..=(dy + dot_r + 1.0) as i32 {
        for x in (dx - dot_r - 1.0) as i32..=(dx + dot_r + 1.0) as i32 {
            let dist = ((x as f32 - dx).powi(2) + (y as f32 - dy).powi(2)).sqrt();
            let cover = (dot_r - dist + 0.5).clamp(0.0, 1.0);
            if cover > 0.0 {
                over(&mut px[(y * w + x) as usize], 255.0, 255.0, 255.0, cover * dot_alpha);
            }
        }
    }
    let (txx, ty) = (gx + 8 + 12, cy - text.h / 2);
    for y in 0..text.h {
        for x in 0..text.w {
            let a = text.a[(y * text.w + x) as usize] as f32 / 255.0;
            if a > 0.0 {
                let (sx, sy) = (txx + x, ty + y);
                if sx >= 0 && sx < w && sy >= 0 && sy < h {
                    over(&mut px[(sy * w + sx) as usize], 255.0, 255.0, 255.0, a);
                }
            }
        }
    }
}

fn run(shared: Arc<Mutex<Shared>>, quit: Arc<AtomicBool>, ready: mpsc::Sender<Result<(), String>>) {
    unsafe {
        let w = GetSystemMetrics(SM_CXSCREEN);
        let h = GetSystemMetrics(SM_CYSCREEN);
        let class = wide("SonicScrewdriverOverlay");
        let hinst = GetModuleHandleW(null());
        let wc = WNDCLASSW {
            style: 0,
            lpfnWndProc: Some(wndproc),
            cbClsExtra: 0,
            cbWndExtra: 0,
            hInstance: hinst,
            hIcon: null_mut(),
            hCursor: null_mut(),
            hbrBackground: null_mut(),
            lpszMenuName: null(),
            lpszClassName: class.as_ptr(),
        };
        RegisterClassW(&wc);
        let hwnd = CreateWindowExW(
            WS_EX_LAYERED | WS_EX_TRANSPARENT | WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE,
            class.as_ptr(),
            class.as_ptr(),
            WS_POPUP,
            0,
            0,
            w,
            h,
            null_mut(),
            null_mut(),
            hinst,
            null(),
        );
        if hwnd.is_null() {
            let _ = ready.send(Err("CreateWindowExW failed".into()));
            return;
        }
        // Keep the overlay out of the agent's own screenshots. AGENT_OVERLAY_CAPTURABLE=1
        // leaves it visible to capture, for debugging what the person sees.
        if std::env::var_os("AGENT_OVERLAY_CAPTURABLE").is_none() {
            SetWindowDisplayAffinity(hwnd, WDA_EXCLUDEFROMCAPTURE);
        }
        let Some(mut dib) = Dib::new(w, h) else {
            let _ = ready.send(Err("could not allocate overlay surface".into()));
            DestroyWindow(hwnd);
            return;
        };
        ShowWindow(hwnd, SW_SHOWNOACTIVATE);
        let _ = ready.send(Ok(()));

        let start = Instant::now();
        let mut text = render_text("AGENT AT WORK");
        let mut last_frame = Instant::now() - FRAME;
        let mut msg: MSG = std::mem::zeroed();
        'outer: loop {
            while PeekMessageW(&mut msg, null_mut(), 0, 0, PM_REMOVE) != 0 {
                if msg.message == WM_QUIT {
                    break 'outer;
                }
                TranslateMessage(&msg);
                DispatchMessageW(&msg);
            }
            if quit.load(Ordering::SeqCst) {
                break;
            }
            if last_frame.elapsed() >= FRAME {
                last_frame = Instant::now();
                {
                    let mut s = shared.lock().unwrap();
                    if s.changed {
                        s.changed = false;
                        let label = if s.status.is_empty() {
                            "AGENT AT WORK".to_string()
                        } else {
                            format!("AGENT AT WORK   \u{00b7}   {}", s.status)
                        };
                        text = render_text(&label);
                    }
                }
                paint(&mut dib, start.elapsed().as_secs_f32(), &text);
                let src = POINT { x: 0, y: 0 };
                let dst = POINT { x: 0, y: 0 };
                let size = SIZE { cx: w, cy: h };
                let blend = BLENDFUNCTION {
                    BlendOp: AC_SRC_OVER as u8,
                    BlendFlags: 0,
                    SourceConstantAlpha: 255,
                    AlphaFormat: AC_SRC_ALPHA as u8,
                };
                UpdateLayeredWindow(hwnd, null_mut(), &dst, &size, dib.hdc, &src, 0, &blend, ULW_ALPHA);
            }
            thread::sleep(Duration::from_millis(8));
        }
        drop(dib);
        DestroyWindow(hwnd);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn glow_and_tab_are_painted() {
        unsafe {
            let mut dib = Dib::new(800, 300).unwrap();
            let text = render_text("AGENT AT WORK");
            assert!(text.a.iter().any(|&a| a > 0), "text mask empty");
            paint(&mut dib, 0.3, &text);
            let px = dib.pixels();
            let at = |x: i32, y: i32| px[(y * 800 + x) as usize];
            assert!(at(400, 2) >> 24 > 0, "tab missing at top centre");
            assert!(at(2, 150) >> 24 > 0, "left edge glow missing");
            assert_eq!(at(400, 200) >> 24, 0, "interior should be transparent");
        }
    }

    #[test]
    fn text_box_is_fixed_width_and_truncates() {
        unsafe {
            let short = render_text("AGENT AT WORK");
            let long = render_text(&"AGENT AT WORK · step 9/9: typing \"a very long value\" ".repeat(4));
            assert_eq!(short.w, TEXT_W);
            assert_eq!(long.w, TEXT_W);
            // Long text fills to the right edge (ending in the ellipsis); short text does not.
            let ink_right = |m: &TextMask| {
                (0..m.w).rev().find(|&x| (0..m.h).any(|y| m.a[(y * m.w + x) as usize] > 0)).unwrap_or(0)
            };
            assert!(ink_right(&long) > TEXT_W - 20, "long text not filling the box");
            assert!(ink_right(&short) < TEXT_W / 2, "short text unexpectedly wide");
        }
    }
}
