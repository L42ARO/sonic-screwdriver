//! local-agent: the dumb executor half of the system. It owns the screen, mouse and
//! keyboard of this machine and does exactly what it is told. No AI lives here.
//!
//! Connected mode (`connect`) dials out to the compute-box and carries out the actions it
//! sends. Scripted mode (`exec`) runs a JSON list of actions and writes every screenshot to
//! a folder, which tests the executor without any compute-box or model involved.

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod agent;
mod desktop;
mod settings;
mod executor;
mod input;
mod overlay;
mod protocol;
mod programs;
mod screen;
mod win;

use std::fs;
use std::path::PathBuf;

use anyhow::{Context, Result};
use clap::{Parser, Subcommand};

use crate::executor::Executor;
use crate::protocol::Action;

#[derive(Parser)]
#[command(name = "local-agent", about = "Executes screen, mouse and keyboard actions on this machine")]
struct Cli {
    /// Screenshots are downscaled so they are at most this wide.
    #[arg(long, default_value_t = 1280)]
    max_width: u32,
    /// Do not show the on-screen "agent at work" overlay.
    #[arg(long)]
    no_overlay: bool,
    #[command(subcommand)]
    cmd: Option<Cmd>,
}

#[derive(Subcommand)]
enum Cmd {
    /// Connect to the compute-box and carry out its actions, reconnecting forever.
    Connect {
        /// The compute-box's agent endpoint.
        #[arg(long, env = "SONIC_AGENT_URL")]
        url: Option<String>,
        /// This machine's name, as callers address it.
        #[arg(long, env = "SONIC_MACHINE")]
        machine: Option<String>,
        /// Shared secret the compute-box expects.
        #[arg(long, env = "SONIC_AGENT_TOKEN")]
        token: Option<String>,
    },
    /// Take one screenshot and write it to a PNG file.
    Screenshot {
        #[arg(long, default_value = "screenshot.png")]
        out: PathBuf,
    },
    /// List visible top-level windows (front to back) with their rects in screenshot space.
    Windows,
    /// Run a JSON array of actions, saving each resulting screenshot into --out-dir.
    Exec {
        script: PathBuf,
        #[arg(long, default_value = "run-out")]
        out_dir: PathBuf,
        /// Stop at the first failed action instead of continuing.
        #[arg(long)]
        fail_fast: bool,
    },
}

fn main() {
    let desktop = std::env::args_os().len() == 1;
    unsafe {
        if desktop { windows_sys::Win32::System::Console::FreeConsole(); }
        else { windows_sys::Win32::System::Console::AttachConsole(windows_sys::Win32::System::Console::ATTACH_PARENT_PROCESS); }
    }
    if let Err(error) = run() {
        if desktop { unsafe { windows_sys::Win32::UI::WindowsAndMessaging::MessageBoxW(std::ptr::null_mut(),settings::wide(&error.to_string()).as_ptr(),settings::wide("Sonic Screwdriver").as_ptr(),0); } }
        else { eprintln!("{error:#}"); }
        std::process::exit(1);
    }
}

fn run() -> Result<()> {
    win::enable_dpi_awareness();
    let cli = Cli::parse();
    match cli.cmd {
        None => desktop::run()?,
        Some(Cmd::Connect { url, machine, token }) => {
            let Some(_instance) = settings::SingleInstance::acquire()? else { return Ok(()) };
            let saved = settings::load()?;
            let url = url.or_else(|| saved.as_ref().map(|p|p.server_url.clone())).unwrap_or_else(||"http://localhost:8080".into());
            let machine = machine.or_else(||saved.as_ref().map(|p|p.machine.clone())).context("Set --machine, SONIC_MACHINE, or configure the desktop app")?;
            settings::validate_machine(&machine)?;
            let token = match token { Some(token)=>token, None=>settings::read_token(saved.as_ref().context("Set --token, SONIC_AGENT_TOKEN, or configure the desktop app")?)? };
            if token.trim().is_empty() { anyhow::bail!("Agent token cannot be empty") }
            let instance_id = match saved { Some(p)=>p.instance_id,None=>settings::random_id()? };
            agent::run(agent::Settings { url:settings::endpoint(&url)?, machine, token, instance_id, max_width: cli.max_width, overlay: !cli.no_overlay })?;
        }
        Some(Cmd::Screenshot { out }) => {
            let ex = Executor::new(cli.max_width)?;
            let shot = ex.screen.screenshot()?;
            fs::write(&out, &shot.png).with_context(|| format!("writing {}", out.display()))?;
            println!(
                "wrote {} ({}x{}, scale {:.3}, physical {}x{})",
                out.display(), shot.width, shot.height, shot.scale, ex.screen.phys_w, ex.screen.phys_h
            );
        }
        Some(Cmd::Windows) => {
            let ex = Executor::new(cli.max_width)?;
            for w in win::list_windows() {
                let r = ex.screen.to_shot_rect(w.x, w.y, w.w, w.h);
                println!("{:>5},{:>5} {:>5}x{:<5}  {}", r[0], r[1], r[2], r[3], w.title);
            }
        }
        Some(Cmd::Exec { script, out_dir, fail_fast }) => {
            let text = fs::read_to_string(&script).with_context(|| format!("reading {}", script.display()))?;
            let actions: Vec<Action> = serde_json::from_str(&text).context("parsing script")?;
            fs::create_dir_all(&out_dir)?;
            let mut ex = Executor::new(cli.max_width)?;
            if !cli.no_overlay {
                ex.show_overlay(&format!("running {}", script.file_name().unwrap_or_default().to_string_lossy()));
            }
            eprintln!(
                "screen {}x{} physical, screenshots {}x{} (scale {:.3}); {} actions",
                ex.screen.phys_w, ex.screen.phys_h, ex.screen.shot_w, ex.screen.shot_h, ex.screen.scale, actions.len()
            );
            let mut failures = 0;
            for (i, a) in actions.iter().enumerate() {
                ex.set_status(&format!("step {}/{}: {}", i + 1, actions.len(), a.describe()));
                let mut r = ex.run(a);
                if let Some(shot) = r.screenshot.as_mut() {
                    let p = out_dir.join(format!("step_{:02}_{}.png", i, a.name()));
                    fs::write(&p, &shot.png)?;
                    shot.path = Some(p.display().to_string());
                }
                println!("{}", serde_json::to_string(&r)?);
                if !r.ok {
                    failures += 1;
                    if fail_fast {
                        break;
                    }
                }
            }
            eprintln!("done: {} actions, {} failed", actions.len(), failures);
            if failures > 0 {
                std::process::exit(1);
            }
        }
    }
    Ok(())
}
