//! Native monochrome setup and tray shell. No web server or embedded browser is needed.
use crate::{
    agent,
    settings::{self, Profile, wide},
};
use anyhow::{Context, Result};
use std::{
    mem,
    ptr::{null, null_mut},
    sync::mpsc,
    thread,
};
use windows_sys::Win32::{
    Foundation::{HWND, LPARAM, LRESULT, POINT, RECT, WPARAM},
    Graphics::Gdi::{
        BLACK_BRUSH, CreateFontW, CreateSolidBrush, DT_CENTER, DT_LEFT, DT_SINGLELINE, DT_VCENTER,
        DeleteObject, DrawFocusRect, DrawTextW, FillRect, FrameRect, GetStockObject,
        InvalidateRect, SelectObject, SetBkColor, SetBkMode, SetTextColor, TRANSPARENT,
        WHITE_BRUSH,
    },
    System::LibraryLoader::GetModuleHandleW,
    UI::{
        Controls::*,
        Input::KeyboardAndMouse::{GetFocus, SetFocus},
        Shell::{
            NIF_ICON, NIF_MESSAGE, NIF_TIP, NIM_ADD, NIM_DELETE, NIM_MODIFY, NOTIFYICONDATAW,
            Shell_NotifyIconW,
        },
        WindowsAndMessaging::*,
    },
};

const INPUT_BACKGROUND: u32 = 0xf5f5f5;
const INPUT_FOCUS_BACKGROUND: u32 = 0xeeeeee;
/// COLORREF is 0x00BBGGRR: a dark amber for the unencrypted-connection warning.
const WARNING_TEXT: u32 = 0x00_1a_6b_b4;
const PLAIN_WARNING: &str = "Unencrypted: the token and screenshots travel in plain text.";
const TRAY: u32 = WM_APP + 1;
const URL: i32 = 101;
const MACHINE: i32 = 102;
const TOKEN: i32 = 103;
const CHECK: i32 = 104;
const TEST: i32 = 105;
const SAVE: i32 = 106;
const SETTINGS: u32 = 201;
const PAUSE: u32 = 202;
const LOGS: u32 = 203;
const STARTUP: u32 = 204;
const QUIT: u32 = 205;

struct Ui {
    controller: agent::Controller,
    profile: Option<Profile>,
    draft: Profile,
    hwnd: HWND,
    status: HWND,
    warning: HWND,
    header: HWND,
    url: HWND,
    machine: HWND,
    token: HWND,
    checkbox: HWND,
    checked: bool,
    fonts: Vec<*mut std::ffi::c_void>,
    icon: HICON,
    input_brush: *mut std::ffi::c_void,
    input_focus_brush: *mut std::ffi::c_void,
    feedback: Option<String>,
    test: Option<mpsc::Receiver<std::result::Result<(), String>>>,
    taskbar_created: u32,
}

pub fn run() -> Result<()> {
    let Some(_instance) = settings::SingleInstance::acquire()? else {
        return Ok(());
    };
    let loaded = settings::load();
    let mut feedback = loaded.as_ref().err().map(|e| e.to_string());
    let profile = loaded.ok().flatten();
    let connection = profile.as_ref().and_then(|p| {
        match settings::read_token(p).and_then(|token| p.connection(token)) {
            Ok(settings) => Some(settings),
            Err(e) => {
                feedback = Some(e.to_string());
                None
            }
        }
    });
    let needs_setup = connection.is_none();
    let mut draft = profile.clone().unwrap_or(Profile::new()?);
    if draft.machine.is_empty() {
        let name = std::env::var("COMPUTERNAME")
            .unwrap_or("my-machine".into())
            .to_lowercase();
        draft.machine = name
            .chars()
            .map(|c| {
                if c.is_ascii_alphanumeric() || c == '-' {
                    c
                } else {
                    '-'
                }
            })
            .collect();
        if settings::validate_machine(&draft.machine).is_err() {
            draft.machine = "my-machine".into()
        }
    }
    let controller = agent::Controller::start(connection)?;
    let mut ui = Box::new(Ui {
        controller,
        profile,
        draft,
        hwnd: null_mut(),
        status: null_mut(),
        warning: null_mut(),
        header: null_mut(),
        url: null_mut(),
        machine: null_mut(),
        token: null_mut(),
        checkbox: null_mut(),
        checked: false,
        fonts: vec![],
        icon: null_mut(),
        input_brush: unsafe { CreateSolidBrush(INPUT_BACKGROUND) },
        input_focus_brush: unsafe { CreateSolidBrush(INPUT_FOCUS_BACKGROUND) },
        feedback,
        test: None,
        taskbar_created: 0,
    });
    unsafe {
        let instance = GetModuleHandleW(null());
        let class = wide("SonicScrewdriverSettings");
        let mut wc: WNDCLASSW = mem::zeroed();
        wc.hInstance = instance;
        wc.lpfnWndProc = Some(wndproc);
        wc.lpszClassName = class.as_ptr();
        wc.hbrBackground = GetStockObject(WHITE_BRUSH).cast();
        wc.hCursor = LoadCursorW(null_mut(), IDC_ARROW);
        if RegisterClassW(&wc) == 0 {
            return Err(std::io::Error::last_os_error()).context("Registering setup window");
        }
        let width = 460;
        let height = 496;
        let hwnd = CreateWindowExW(
            WS_EX_CONTROLPARENT,
            class.as_ptr(),
            wide("Sonic Screwdriver · Setup").as_ptr(),
            WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_MINIMIZEBOX,
            (GetSystemMetrics(SM_CXSCREEN) - width) / 2,
            (GetSystemMetrics(SM_CYSCREEN) - height) / 2,
            width,
            height,
            null_mut(),
            null_mut(),
            instance,
            (&mut *ui as *mut Ui).cast(),
        );
        if hwnd.is_null() {
            return Err(std::io::Error::last_os_error()).context("Creating setup window");
        }
        ui.hwnd = hwnd;
        build_controls(&mut ui);
        ui.taskbar_created = RegisterWindowMessageW(wide("TaskbarCreated").as_ptr());
        ui.icon = monochrome_icon();
        tray(&ui, NIM_ADD);
        SetTimer(hwnd, 1, 300, None);
        populate(&mut ui);
        if needs_setup {
            ShowWindow(hwnd, SW_SHOW);
            SetForegroundWindow(hwnd);
            SetFocus(ui.url);
        }
        let mut message: MSG = mem::zeroed();
        while GetMessageW(&mut message, null_mut(), 0, 0) > 0 {
            if IsDialogMessageW(hwnd, &message) == 0 {
                TranslateMessage(&message);
                DispatchMessageW(&message);
            }
        }
        tray(&ui, NIM_DELETE);
        if !ui.icon.is_null() {
            DestroyIcon(ui.icon);
        }
        DeleteObject(ui.input_brush);
        DeleteObject(ui.input_focus_brush);
        for font in &ui.fonts {
            DeleteObject(*font);
        }
    }
    ui.controller.shutdown();
    ui.controller.wait();
    Ok(())
}

unsafe fn font(size: i32, weight: i32, face: &str) -> *mut std::ffi::c_void {
    unsafe {
        CreateFontW(
            -size,
            0,
            0,
            0,
            weight,
            0,
            0,
            0,
            1,
            0,
            0,
            5,
            0,
            wide(face).as_ptr(),
        )
    }
}
unsafe fn control(
    ui: &Ui,
    class: &str,
    text: &str,
    id: i32,
    x: i32,
    y: i32,
    w: i32,
    h: i32,
    style: u32,
    font: usize,
) -> HWND {
    unsafe {
        let hwnd = CreateWindowExW(
            0,
            wide(class).as_ptr(),
            wide(text).as_ptr(),
            WS_CHILD | WS_VISIBLE | style,
            x,
            y,
            w,
            h,
            ui.hwnd,
            id as usize as _,
            GetModuleHandleW(null()),
            null(),
        );
        SendMessageW(hwnd, WM_SETFONT, ui.fonts[font] as usize, 1);
        hwnd
    }
}
unsafe fn build_controls(ui: &mut Ui) {
    unsafe {
        ui.fonts = vec![
            font(15, 400, "Segoe UI"),
            font(26, 600, "Segoe UI"),
            font(13, 400, "Segoe UI"),
            font(15, 600, "Segoe UI"),
        ];
        ui.header = control(ui, "STATIC", "Connect", 0, 28, 22, 388, 36, 0, 1);
        for (label, y) in [
            ("Server URL", 72),
            ("Machine name", 168),
            ("Agent token", 238),
        ] {
            control(ui, "STATIC", label, 0, 28, y, 388, 20, 0, 2);
        }
        ui.url = control(
            ui,
            "EDIT",
            "",
            URL,
            28,
            94,
            388,
            32,
            WS_TABSTOP | ES_AUTOHSCROLL as u32,
            0,
        );
        ui.machine = control(
            ui,
            "EDIT",
            "",
            MACHINE,
            28,
            190,
            388,
            32,
            WS_TABSTOP | ES_AUTOHSCROLL as u32,
            0,
        );
        ui.token = control(
            ui,
            "EDIT",
            "",
            TOKEN,
            28,
            260,
            388,
            32,
            WS_TABSTOP | ES_AUTOHSCROLL as u32 | ES_PASSWORD as u32,
            0,
        );
        for edit in [ui.url, ui.machine, ui.token] {
            SendMessageW(edit, EM_SETMARGINS, 3, 10 | (10 << 16));
        }
        ui.checkbox = control(
            ui,
            "BUTTON",
            "Start at sign-in",
            CHECK,
            28,
            306,
            388,
            28,
            WS_TABSTOP | BS_OWNERDRAW as u32,
            0,
        );
        control(
            ui,
            "BUTTON",
            "Test",
            TEST,
            28,
            350,
            132,
            36,
            WS_TABSTOP | BS_OWNERDRAW as u32,
            3,
        );
        control(
            ui,
            "BUTTON",
            "Connect",
            SAVE,
            176,
            350,
            240,
            36,
            WS_TABSTOP | BS_OWNERDRAW as u32,
            3,
        );
        ui.warning = control(ui, "STATIC", "", 0, 28, 130, 388, 20, 0, 2);
        ui.status = control(ui, "STATIC", "", 0, 28, 402, 388, 48, 0, 2);
    }
}

unsafe fn populate(ui: &mut Ui) {
    unsafe {
        if let Some(profile) = &ui.profile {
            ui.draft = profile.clone();
        }
        SetWindowTextW(ui.url, wide(&ui.draft.server_url).as_ptr());
        update_warning(ui);
        SetWindowTextW(ui.machine, wide(&ui.draft.machine).as_ptr());
        SetWindowTextW(ui.token, wide("").as_ptr());
        ui.checked = ui.draft.start_at_signin;
        SetWindowTextW(
            ui.header,
            wide(if ui.profile.is_some() {
                "Settings"
            } else {
                "Connect"
            })
            .as_ptr(),
        );
        SendMessageW(
            ui.token,
            EM_SETCUEBANNER,
            0,
            wide(if ui.profile.is_some() {
                "Keep saved token"
            } else {
                ""
            })
            .as_ptr() as isize,
        );
        InvalidateRect(ui.checkbox, null(), 1);
        refresh(ui);
    }
}
unsafe fn update_warning(ui: &Ui) {
    unsafe {
        let plain = settings::is_unencrypted_remote(&text(ui.url));
        SetWindowTextW(ui.warning, wide(if plain { PLAIN_WARNING } else { "" }).as_ptr());
    }
}
unsafe fn text(hwnd: HWND) -> String {
    unsafe {
        let len = GetWindowTextLengthW(hwnd).clamp(0, 4096) as usize;
        let mut text = vec![0u16; len + 1];
        let n = GetWindowTextW(hwnd, text.as_mut_ptr(), text.len() as i32);
        String::from_utf16_lossy(&text[..n.max(0) as usize])
    }
}
unsafe fn candidate(ui: &Ui) -> Result<(Profile, String, agent::Settings)> {
    unsafe {
        let mut p = ui.draft.clone();
        p.server_url = text(ui.url).trim().into();
        p.machine = text(ui.machine).trim().into();
        p.start_at_signin = ui.checked;
        let entered = text(ui.token);
        let token = if entered.trim().is_empty() {
            if let Some(saved) = &ui.profile {
                settings::read_token(saved)?
            } else {
                anyhow::bail!("Enter the agent authentication token printed by the compute-box")
            }
        } else {
            entered.trim().to_string()
        };
        let connection = p.connection(token.clone())?;
        Ok((p, token, connection))
    }
}
unsafe fn refresh(ui: &mut Ui) {
    unsafe {
        if let Some(receiver) = &ui.test {
            if let Ok(result) = receiver.try_recv() {
                ui.feedback = Some(match result {
                    Ok(()) => "Connection verified.".into(),
                    Err(e) => e,
                });
                ui.test = None;
            }
        }
        let state = ui.controller.status();
        let message = ui.feedback.clone().unwrap_or_else(|| {
            if state.message == "Not configured" {
                String::new()
            } else {
                state.message.clone()
            }
        });
        SetWindowTextW(ui.status, wide(&message).as_ptr());
        tray(ui, NIM_MODIFY);
        if state.stopped {
            DestroyWindow(ui.hwnd);
        }
    }
}
unsafe fn command(ui: &mut Ui, id: u32) {
    unsafe {
        match id {
            CHECK_ID => {
                ui.checked = !ui.checked;
                InvalidateRect(ui.checkbox, null(), 1);
            }
            TEST_ID => {
                if ui.test.is_some() {
                    return;
                }
                match candidate(ui) {
                    Err(e) => ui.feedback = Some(format!("{e:#}")),
                    Ok((_, _, settings)) => {
                        let (tx, rx) = mpsc::channel();
                        ui.test = Some(rx);
                        ui.feedback = Some("Testing…".into());
                        thread::spawn(move || {
                            let result = agent::probe(&settings).map_err(|e| e.to_string());
                            let _ = tx.send(result);
                        });
                    }
                }
            }
            SAVE_ID => match candidate(ui) {
                Err(e) => ui.feedback = Some(format!("{e:#}")),
                Ok((mut profile, token, connection)) => {
                    match settings::save(&mut profile, &token) {
                        Err(e) => ui.feedback = Some(format!("{e:#}")),
                        Ok(()) => {
                            ui.draft = profile.clone();
                            ui.profile = Some(profile);
                            ui.feedback = None;
                            SetWindowTextW(ui.token, wide("").as_ptr());
                            ui.controller.configure(connection);
                            ShowWindow(ui.hwnd, SW_HIDE);
                        }
                    }
                }
            },
            SETTINGS => {
                ui.feedback = None;
                populate(ui);
                ShowWindow(ui.hwnd, SW_RESTORE);
                SetForegroundWindow(ui.hwnd);
                SetFocus(ui.url);
            }
            PAUSE => {
                let paused = ui.controller.status().paused;
                ui.controller.set_paused(!paused);
            }
            LOGS => show_logs(ui),
            STARTUP => {
                if let Some(mut p) = ui.profile.clone() {
                    p.start_at_signin = !p.start_at_signin;
                    match settings::read_token(&p).and_then(|token| settings::save(&mut p, &token))
                    {
                        Ok(()) => {
                            ui.draft = p.clone();
                            ui.profile = Some(p);
                        }
                        Err(e) => {
                            ui.feedback = Some(format!("{e:#}"));
                            ShowWindow(ui.hwnd, SW_SHOW);
                        }
                    }
                }
            }
            QUIT => {
                ui.feedback = None;
                ui.controller.shutdown();
            }
            _ => {}
        }
        refresh(ui);
    }
}
const CHECK_ID: u32 = CHECK as u32;
const TEST_ID: u32 = TEST as u32;
const SAVE_ID: u32 = SAVE as u32;

unsafe fn tray(ui: &Ui, operation: u32) {
    unsafe {
        let mut data: NOTIFYICONDATAW = mem::zeroed();
        data.cbSize = mem::size_of::<NOTIFYICONDATAW>() as u32;
        data.hWnd = ui.hwnd;
        data.uID = 1;
        data.uFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP;
        data.uCallbackMessage = TRAY;
        data.hIcon = ui.icon;
        let state = ui.controller.status();
        let tooltip = format!("Sonic Screwdriver — {}", state.message);
        for (to, from) in data.szTip.iter_mut().take(127).zip(tooltip.encode_utf16()) {
            *to = from
        }
        Shell_NotifyIconW(operation, &data);
    }
}
unsafe fn menu(ui: &mut Ui) {
    unsafe {
        let state = ui.controller.status();
        let menu = CreatePopupMenu();
        let status = if let Some(p) = &ui.profile {
            format!("{} · {}", p.machine, state.message)
        } else {
            state.message.clone()
        };
        AppendMenuW(menu, MF_STRING | MF_GRAYED, 0, wide(&status).as_ptr());
        AppendMenuW(menu, MF_SEPARATOR, 0, null());
        AppendMenuW(
            menu,
            MF_STRING,
            SETTINGS as usize,
            wide("Settings…").as_ptr(),
        );
        AppendMenuW(
            menu,
            MF_STRING,
            PAUSE as usize,
            wide(if state.paused {
                "Resume"
            } else if state.busy {
                "Pause after current job"
            } else {
                "Pause"
            })
            .as_ptr(),
        );
        AppendMenuW(
            menu,
            MF_STRING,
            LOGS as usize,
            wide("View connection logs…").as_ptr(),
        );
        let checked = ui.profile.as_ref().is_some_and(|p| p.start_at_signin);
        AppendMenuW(
            menu,
            MF_STRING
                | if checked { MF_CHECKED } else { 0 }
                | if ui.profile.is_none() { MF_GRAYED } else { 0 },
            STARTUP as usize,
            wide("Start at sign-in").as_ptr(),
        );
        AppendMenuW(menu, MF_SEPARATOR, 0, null());
        AppendMenuW(
            menu,
            MF_STRING,
            QUIT as usize,
            wide(if state.busy {
                "Quit after current job"
            } else {
                "Quit"
            })
            .as_ptr(),
        );
        let mut point: POINT = mem::zeroed();
        GetCursorPos(&mut point);
        SetForegroundWindow(ui.hwnd);
        let selected = TrackPopupMenu(
            menu,
            TPM_RETURNCMD | TPM_RIGHTBUTTON,
            point.x,
            point.y,
            0,
            ui.hwnd,
            null(),
        );
        DestroyMenu(menu);
        PostMessageW(ui.hwnd, WM_NULL, 0, 0);
        if selected != 0 {
            command(ui, selected as u32)
        }
    }
}

unsafe fn monochrome_icon() -> HICON {
    unsafe {
        let and = [0u8; 128];
        let mut xor = [0u8; 128];
        for y in 0..32 {
            for x in 0..32 {
                let border = (4..28).contains(&x) && (y == 4 || y == 27)
                    || (4..28).contains(&y) && (x == 4 || x == 27);
                let s = (10..22).contains(&x) && (y == 10 || y == 16 || y == 22)
                    || (10..17).contains(&y) && x == 10
                    || (16..23).contains(&y) && x == 21;
                if border || s {
                    xor[y * 4 + x / 8] |= 1 << (7 - x % 8);
                }
            }
        }
        CreateIcon(
            GetModuleHandleW(null()),
            32,
            32,
            1,
            1,
            and.as_ptr(),
            xor.as_ptr(),
        )
    }
}
unsafe fn draw_button(ui: &Ui, item: &DRAWITEMSTRUCT) {
    unsafe {
        let selected = item.itemState & ODS_SELECTED != 0;
        let primary = item.CtlID == SAVE_ID;
        let black = primary ^ selected;
        let bg = if black { BLACK_BRUSH } else { WHITE_BRUSH };
        let fg = if black { 0xffffff } else { 0 };
        FillRect(item.hDC, &item.rcItem, GetStockObject(bg).cast());
        SetTextColor(item.hDC, fg);
        SetBkMode(item.hDC, TRANSPARENT as i32);
        SelectObject(item.hDC, ui.fonts[3]);
        let mut rect = item.rcItem;
        if item.CtlID == CHECK_ID {
            let check = RECT {
                left: rect.left + 1,
                top: rect.top + 5,
                right: rect.left + 21,
                bottom: rect.top + 25,
            };
            FrameRect(item.hDC, &check, GetStockObject(BLACK_BRUSH).cast());
            if ui.checked {
                let mut mark = check;
                DrawTextW(
                    item.hDC,
                    wide("×").as_ptr(),
                    -1,
                    &mut mark,
                    DT_CENTER | DT_VCENTER | DT_SINGLELINE,
                );
            }
            rect.left += 32;
            DrawTextW(
                item.hDC,
                wide("Start at sign-in").as_ptr(),
                -1,
                &mut rect,
                DT_LEFT | DT_VCENTER | DT_SINGLELINE,
            );
        } else {
            if primary {
                FrameRect(item.hDC, &item.rcItem, GetStockObject(BLACK_BRUSH).cast());
            }
            DrawTextW(
                item.hDC,
                wide(&text(item.hwndItem)).as_ptr(),
                -1,
                &mut rect,
                DT_CENTER | DT_VCENTER | DT_SINGLELINE,
            );
        }
        if item.itemState & ODS_FOCUS != 0 {
            let focus = RECT {
                left: rect.left + 3,
                top: rect.top + 3,
                right: rect.right - 3,
                bottom: rect.bottom - 3,
            };
            DrawFocusRect(item.hDC, &focus);
        }
    }
}

unsafe extern "system" fn wndproc(hwnd: HWND, message: u32, wp: WPARAM, lp: LPARAM) -> LRESULT {
    unsafe {
        if message == WM_NCCREATE {
            let create = &*(lp as *const CREATESTRUCTW);
            SetWindowLongPtrW(hwnd, GWLP_USERDATA, create.lpCreateParams as isize);
        }
        let ptr = GetWindowLongPtrW(hwnd, GWLP_USERDATA) as *mut Ui;
        if matches!(
            message,
            WM_CTLCOLORSTATIC | WM_CTLCOLOREDIT | WM_CTLCOLORBTN
        ) {
            let dc = wp as _;
            let warning = !ptr.is_null() && lp as HWND == (*ptr).warning;
            SetTextColor(dc, if warning { WARNING_TEXT } else { 0 });
            if message == WM_CTLCOLOREDIT && !ptr.is_null() {
                let focused = GetFocus() == lp as HWND;
                SetBkColor(
                    dc,
                    if focused {
                        INPUT_FOCUS_BACKGROUND
                    } else {
                        INPUT_BACKGROUND
                    },
                );
                let brush = if focused {
                    (*ptr).input_focus_brush
                } else {
                    (*ptr).input_brush
                };
                if !brush.is_null() {
                    return brush as isize;
                }
            }
            SetBkColor(dc, 0xffffff);
            return GetStockObject(WHITE_BRUSH) as isize;
        }
        if ptr.is_null() {
            return DefWindowProcW(hwnd, message, wp, lp);
        }
        let ui = &mut *ptr;
        if ui.taskbar_created != 0 && message == ui.taskbar_created {
            tray(ui, NIM_ADD);
            return 0;
        }
        match message {
            WM_COMMAND => {
                if (wp >> 16) == BN_CLICKED as usize {
                    command(ui, (wp & 0xffff) as u32)
                } else if (wp >> 16) as u32 == EN_CHANGE && (wp & 0xffff) as i32 == URL {
                    update_warning(ui);
                } else if matches!((wp >> 16) as u32, EN_SETFOCUS | EN_KILLFOCUS) {
                    for edit in [ui.url, ui.machine, ui.token] {
                        InvalidateRect(edit, null(), 1);
                    }
                }
                0
            }
            WM_DRAWITEM => {
                draw_button(ui, &*(lp as *const DRAWITEMSTRUCT));
                1
            }
            WM_TIMER => {
                refresh(ui);
                0
            }
            TRAY => {
                if matches!(lp as u32, WM_RBUTTONUP | WM_LBUTTONUP | WM_CONTEXTMENU) {
                    menu(ui)
                }
                0
            }
            WM_CLOSE => {
                if ui.profile.is_some() {
                    ShowWindow(hwnd, SW_HIDE);
                } else {
                    ui.controller.shutdown();
                }
                0
            }
            WM_DESTROY => {
                KillTimer(hwnd, 1);
                PostQuitMessage(0);
                0
            }
            _ => DefWindowProcW(hwnd, message, wp, lp),
        }
    }
}

unsafe fn show_logs(ui: &Ui) {
    unsafe {
        // A small, read-only native window keeps the same monochrome surface as setup.
        let class = wide("SonicScrewdriverLogs");
        let mut wc: WNDCLASSW = mem::zeroed();
        wc.hInstance = GetModuleHandleW(null());
        wc.lpfnWndProc = Some(logproc);
        wc.lpszClassName = class.as_ptr();
        wc.hbrBackground = GetStockObject(WHITE_BRUSH).cast();
        wc.hCursor = LoadCursorW(null_mut(), IDC_ARROW);
        RegisterClassW(&wc);
        let hwnd = CreateWindowExW(
            0,
            class.as_ptr(),
            wide("Sonic Screwdriver · Connection logs").as_ptr(),
            WS_OVERLAPPEDWINDOW | WS_VISIBLE,
            CW_USEDEFAULT,
            CW_USEDEFAULT,
            720,
            460,
            null_mut(),
            null_mut(),
            wc.hInstance,
            null(),
        );
        if hwnd.is_null() {
            return;
        }
        let logs = ui
            .controller
            .status()
            .logs
            .into_iter()
            .collect::<Vec<_>>()
            .join("\r\n\r\n");
        let edit = CreateWindowExW(
            0,
            wide("EDIT").as_ptr(),
            wide(&logs).as_ptr(),
            WS_CHILD
                | WS_VISIBLE
                | WS_VSCROLL
                | ES_MULTILINE as u32
                | ES_READONLY as u32
                | ES_AUTOVSCROLL as u32,
            16,
            16,
            670,
            380,
            hwnd,
            1usize as _,
            wc.hInstance,
            null(),
        );
        SendMessageW(edit, WM_SETFONT, ui.fonts[2] as usize, 1);
    }
}
unsafe extern "system" fn logproc(hwnd: HWND, message: u32, wp: WPARAM, lp: LPARAM) -> LRESULT {
    unsafe {
        match message {
            WM_CTLCOLORSTATIC | WM_CTLCOLOREDIT => {
                SetTextColor(wp as _, 0);
                SetBkColor(wp as _, 0xffffff);
                GetStockObject(WHITE_BRUSH) as isize
            }
            WM_SIZE => {
                let child = GetDlgItem(hwnd, 1);
                if !child.is_null() {
                    MoveWindow(
                        child,
                        16,
                        16,
                        (lp as u32 & 0xffff) as i32 - 32,
                        ((lp as u32 >> 16) & 0xffff) as i32 - 32,
                        1,
                    );
                }
                0
            }
            _ => DefWindowProcW(hwnd, message, wp, lp),
        }
    }
}
