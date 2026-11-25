//! Application lifecycle metadata only; application contents remain vision-only.
use crate::win::{self, TopWindow};
use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::os::windows::process::CommandExt;
use std::{
    collections::HashMap,
    fmt, mem,
    path::Path,
    process::{Command, Stdio},
    thread,
    time::{Duration, Instant},
};
use windows_sys::Win32::{
    Foundation::{CloseHandle, INVALID_HANDLE_VALUE},
    System::{
        Diagnostics::ToolHelp::{
            CreateToolhelp32Snapshot, PROCESSENTRY32W, Process32FirstW, Process32NextW,
            TH32CS_SNAPPROCESS,
        },
        Threading::{OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION, QueryFullProcessImageNameW},
    },
};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Installation {
    pub executable: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub working_directory: String,
    pub window_title_contains: String,
    #[serde(default)]
    pub startup_timeout_ms: u64,
}

impl Installation {
    /// Expands Windows-style `%NAME%` environment variables in the executable, arguments
    /// and working directory, on this machine. Configurations can then name locations that
    /// differ per computer without revealing anyone's user folder. `%SONIC_AGENT_DIR%` is
    /// the folder holding local-agent.exe, so programs shipped beside it work wherever the
    /// release was unpacked. Text like `50%` that is not a variable is left alone.
    pub fn expanded(&self) -> Result<Installation> {
        let lookup = |name: &str| -> Option<String> {
            if let Some(value) = std::env::var(name).ok().filter(|v| !v.is_empty()) {
                return Some(value);
            }
            if name.eq_ignore_ascii_case("SONIC_AGENT_DIR") {
                return std::env::current_exe()
                    .ok()?
                    .parent()
                    .map(|dir| dir.display().to_string());
            }
            None
        };
        Ok(Installation {
            executable: expand_vars(&self.executable, &lookup)?,
            args: self
                .args
                .iter()
                .map(|a| expand_vars(a, &lookup))
                .collect::<Result<_>>()?,
            working_directory: expand_vars(&self.working_directory, &lookup)?,
            ..self.clone()
        })
    }
}

fn expand_vars(text: &str, lookup: &dyn Fn(&str) -> Option<String>) -> Result<String> {
    let is_name = |name: &str| {
        name.chars()
            .next()
            .is_some_and(|c| c.is_ascii_alphabetic() || c == '_')
            && name
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || "_()".contains(c))
    };
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find('%') {
        out.push_str(&rest[..start]);
        let after = &rest[start + 1..];
        match after.find('%') {
            Some(end) if is_name(&after[..end]) => {
                let name = &after[..end];
                let value = lookup(name).ok_or_else(|| {
                    error(
                        "invalid_launch_configuration",
                        format!("%{name}% is not set on this machine"),
                    )
                })?;
                out.push_str(&value);
                rest = &after[end + 1..];
            }
            _ => {
                out.push('%');
                rest = after;
            }
        }
    }
    out.push_str(rest);
    Ok(out)
}

#[derive(Debug)]
pub struct AppError {
    pub code: &'static str,
    message: String,
}
impl fmt::Display for AppError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.message)
    }
}
impl std::error::Error for AppError {}
fn error(code: &'static str, message: impl Into<String>) -> anyhow::Error {
    AppError {
        code,
        message: message.into(),
    }
    .into()
}

#[derive(Debug, Serialize)]
pub struct ProcessInfo {
    pub pid: u32,
    pub name: String,
    pub executable: Option<String>,
}

// Inaccessible system processes still appear with executable=null. No command lines or
// application data are read. Snapshot handles are always closed.
pub fn processes() -> Result<Vec<ProcessInfo>> {
    unsafe {
        let snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
        if snapshot == INVALID_HANDLE_VALUE {
            return Err(std::io::Error::last_os_error()).context("enumerating processes");
        }
        let mut entry: PROCESSENTRY32W = mem::zeroed();
        entry.dwSize = mem::size_of::<PROCESSENTRY32W>() as u32;
        let mut out = Vec::new();
        let mut found = Process32FirstW(snapshot, &mut entry);
        while found != 0 {
            let n = entry
                .szExeFile
                .iter()
                .position(|c| *c == 0)
                .unwrap_or(entry.szExeFile.len());
            out.push(ProcessInfo {
                pid: entry.th32ProcessID,
                name: String::from_utf16_lossy(&entry.szExeFile[..n]),
                executable: executable_path(entry.th32ProcessID),
            });
            found = Process32NextW(snapshot, &mut entry);
        }
        CloseHandle(snapshot);
        Ok(out)
    }
}

fn executable_path(pid: u32) -> Option<String> {
    unsafe {
        let handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
        if handle.is_null() {
            return None;
        }
        let mut buf = vec![0u16; 32768];
        let mut n = buf.len() as u32;
        let ok = QueryFullProcessImageNameW(handle, 0, buf.as_mut_ptr(), &mut n);
        CloseHandle(handle);
        (ok != 0).then(|| String::from_utf16_lossy(&buf[..n as usize]))
    }
}

pub fn inventory() -> Result<Value> {
    let windows = win::list_windows();
    Ok(json!({"applications": processes()?.into_iter().map(|p| {
        let windows: Vec<_> = windows.iter().filter(|w| w.pid == p.pid).map(|w| json!({
            "title": w.title, "minimized": w.minimized, "foreground": w.foreground,
            "rect_physical": [w.x, w.y, w.w, w.h]
        })).collect();
        json!({"pid": p.pid, "name": p.name, "executable": p.executable, "windows": windows})
    }).collect::<Vec<_>>()}))
}

fn same_path(a: &str, b: &str) -> bool {
    a.trim_start_matches(r"\\?\")
        .replace('/', "\\")
        .eq_ignore_ascii_case(&b.trim_start_matches(r"\\?\").replace('/', "\\"))
}

fn matching_windows<'a>(
    installation: &Installation,
    windows: &'a [TopWindow],
    processes: &[ProcessInfo],
) -> Vec<&'a TopWindow> {
    let title = installation.window_title_contains.to_lowercase();
    windows
        .iter()
        .filter(|w| {
            w.title.to_lowercase().contains(&title)
                && processes.iter().any(|p| {
                    p.pid == w.pid
                        && p.executable
                            .as_deref()
                            .is_some_and(|path| same_path(path, &installation.executable))
                })
        })
        .collect()
}

#[derive(Default)]
pub struct Launcher {
    // Retained after timeout so retries cannot spawn another still-running instance.
    // Arguments distinguish hosted applications (e.g. two scripts using python.exe).
    pending: HashMap<String, u32>,
}

impl Launcher {
    pub fn open(&mut self, installation: &Installation) -> Result<TopWindow> {
        if !Path::new(&installation.executable).is_absolute()
            || installation.window_title_contains.trim().is_empty()
            || installation.startup_timeout_ms > 120000
            || (!installation.working_directory.is_empty()
                && !Path::new(&installation.working_directory).is_absolute())
        {
            return Err(error(
                "invalid_launch_configuration",
                "absolute paths, a nonempty window matcher and a startup timeout <=120000 ms are required",
            ));
        }
        let key = serde_json::to_string(&(
            &installation.executable,
            &installation.args,
            &installation.working_directory,
            &installation.window_title_contains,
        ))?;
        let started = Instant::now();
        let timeout = Duration::from_millis(if installation.startup_timeout_ms == 0 {
            30000
        } else {
            installation.startup_timeout_ms
        });
        let mut attempted = false;
        loop {
            let processes = processes()?;
            let windows = win::list_windows();
            let matches = matching_windows(installation, &windows, &processes);
            if matches.len() > 1 {
                return Err(error(
                    "application_ambiguous",
                    "multiple matching windows; use a more specific window_title_contains",
                ));
            }
            if let Some(window) = matches.first() {
                return Ok((*window).clone());
            }
            let pending = self.pending.get(&key).is_some_and(|pid| {
                processes.iter().any(|p| {
                    p.pid == *pid
                        && p.executable
                            .as_deref()
                            .is_some_and(|path| same_path(path, &installation.executable))
                })
            });
            // Without command-line inspection, an interpreter's executable alone cannot
            // identify which script it hosts. Only infer a native process when args=[];
            // otherwise rely on the matching window or an instance we launched ourselves.
            let running = pending
                || (installation.args.is_empty()
                    && processes.iter().any(|p| {
                        p.executable
                            .as_deref()
                            .is_some_and(|path| same_path(path, &installation.executable))
                    }));
            if !running {
                if attempted {
                    return Err(error(
                        "launch_failed",
                        "program exited before a matching window appeared",
                    ));
                }
                let mut command = Command::new(&installation.executable);
                // GUI windows still appear; console hosts do not create an extra terminal.
                command.creation_flags(0x08000000); // CREATE_NO_WINDOW
                command
                    .args(&installation.args)
                    .stdin(Stdio::null())
                    .stdout(Stdio::null())
                    .stderr(Stdio::null());
                if !installation.working_directory.is_empty() {
                    command.current_dir(&installation.working_directory);
                }
                let child = command.spawn().map_err(|e| {
                    error(
                        "launch_failed",
                        format!("could not launch configured executable: {e}"),
                    )
                })?;
                self.pending.insert(key.clone(), child.id());
                attempted = true;
                // Closing our process handle leaves the desktop application running.
                drop(child);
            }
            if started.elapsed() >= timeout {
                return Err(error(
                    "startup_timeout",
                    "program is running but no matching window appeared; inspect running applications before retrying",
                ));
            }
            thread::sleep(Duration::from_millis(200));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn variables_expand_on_this_machine() {
        let lookup = |name: &str| match name {
            "SONIC_AGENT_DIR" => Some(r"D:\Tools\sonic".to_string()),
            "LOCALAPPDATA" => Some(r"C:\Users\someone\AppData\Local".to_string()),
            _ => None,
        };
        let expand = |s: &str| expand_vars(s, &lookup);
        assert_eq!(
            expand(r"%SONIC_AGENT_DIR%\demo\DentaSoft.exe").unwrap(),
            r"D:\Tools\sonic\demo\DentaSoft.exe"
        );
        assert_eq!(
            expand(r"%LOCALAPPDATA%\x %SONIC_AGENT_DIR%").unwrap(),
            r"C:\Users\someone\AppData\Local\x D:\Tools\sonic"
        );
        assert_eq!(expand(r"C:\Apps\app.exe").unwrap(), r"C:\Apps\app.exe");
        // Not variables: left as written.
        assert_eq!(expand("50% off, 100%").unwrap(), "50% off, 100%");
        assert_eq!(expand("%").unwrap(), "%");
        assert_eq!(expand("%% %1 %").unwrap(), "%% %1 %");
        // A variable this machine lacks is an error, not a half-expanded path.
        let err = expand(r"%NOPE%\app.exe").unwrap_err();
        assert!(err.to_string().contains("%NOPE% is not set"), "{err}");
    }
    #[test]
    fn agent_dir_defaults_to_the_executable_folder() {
        if std::env::var("SONIC_AGENT_DIR").is_ok() {
            return;
        }
        let mut app = installation();
        app.executable = r"%SONIC_AGENT_DIR%\demo\app.exe".into();
        app.args = vec![r"%SONIC_AGENT_DIR%\demo\data.json".into(), "50%".into()];
        let expanded = app.expanded().unwrap();
        let dir = std::env::current_exe()
            .unwrap()
            .parent()
            .unwrap()
            .display()
            .to_string();
        assert_eq!(expanded.executable, format!(r"{dir}\demo\app.exe"));
        assert_eq!(
            expanded.args,
            vec![format!(r"{dir}\demo\data.json"), "50%".to_string()]
        );
    }
    fn installation() -> Installation {
        Installation {
            executable: r"C:\Apps\app.exe".into(),
            args: vec![],
            working_directory: String::new(),
            window_title_contains: "Records".into(),
            startup_timeout_ms: 100,
        }
    }
    #[test]
    fn window_matching_requires_executable_and_title() {
        let windows = vec![TopWindow {
            hwnd: 0,
            pid: 10,
            title: "Patient Records".into(),
            minimized: true,
            foreground: false,
            x: 0,
            y: 0,
            w: 100,
            h: 100,
        }];
        let mut processes = vec![ProcessInfo {
            pid: 10,
            name: "app.exe".into(),
            executable: Some(r"c:/apps/APP.exe".into()),
        }];
        assert_eq!(
            matching_windows(&installation(), &windows, &processes).len(),
            1
        );
        processes[0].executable = Some(r"C:\Other\app.exe".into());
        assert!(matching_windows(&installation(), &windows, &processes).is_empty());
        processes[0].executable = None;
        assert!(matching_windows(&installation(), &windows, &processes).is_empty());
    }
    #[test]
    fn missing_executable_is_a_launch_error() {
        let mut app = installation();
        app.executable = r"C:\sonic-does-not-exist\missing.exe".into();
        let err = Launcher::default().open(&app).unwrap_err();
        assert_eq!(
            err.downcast_ref::<AppError>().unwrap().code,
            "launch_failed"
        );
    }

    #[test]
    #[ignore = "subprocess helper, invoked explicitly by lifecycle test"]
    fn lifecycle_child() {
        thread::sleep(Duration::from_secs(30));
    }

    #[test]
    fn startup_timeout_does_not_launch_duplicate_on_retry() {
        let mut app = installation();
        app.executable = std::env::current_exe()
            .unwrap()
            .to_string_lossy()
            .into_owned();
        app.args = vec![
            "--exact".into(),
            "programs::tests::lifecycle_child".into(),
            "--ignored".into(),
        ];
        app.window_title_contains = "sonic-nonexistent-test-window".into();
        let mut launcher = Launcher::default();
        let first = launcher.open(&app).unwrap_err();
        let pid = *launcher.pending.values().next().unwrap();
        // Always terminate only the child this test launched, even if an assertion fails.
        struct ChildGuard(u32);
        impl Drop for ChildGuard {
            fn drop(&mut self) {
                unsafe {
                    use windows_sys::Win32::System::Threading::{
                        OpenProcess, PROCESS_TERMINATE, TerminateProcess,
                    };
                    let h = OpenProcess(PROCESS_TERMINATE, 0, self.0);
                    if !h.is_null() {
                        TerminateProcess(h, 0);
                        CloseHandle(h);
                    }
                }
            }
        }
        let _guard = ChildGuard(pid);
        assert_eq!(
            first.downcast_ref::<AppError>().unwrap().code,
            "startup_timeout"
        );
        // Changing only a timeout must not bypass duplicate prevention.
        app.startup_timeout_ms = 150;
        let second = launcher.open(&app).unwrap_err();
        assert_eq!(
            second.downcast_ref::<AppError>().unwrap().code,
            "startup_timeout"
        );
        assert_eq!(launcher.pending.len(), 1);
        assert_eq!(*launcher.pending.values().next().unwrap(), pid);
        assert!(processes().unwrap().iter().any(|p| p.pid == pid));
    }
}
