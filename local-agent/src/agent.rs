//! Connected mode: dial out to the compute-box, stay connected for as long as the program
//! runs, and carry out whatever actions arrive.
//!
//! The connection never gives up: on any failure it waits (1 s, doubling to 30 s, with
//! jitter) and reconnects. The compute-box pings every 15 s; 45 s of silence counts as a
//! dead connection. Screen work runs on its own thread, so heartbeats keep flowing while
//! a slow action (a 10 s wait, a laggy app) is in progress.

use std::sync::mpsc as std_mpsc;
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use base64::Engine;
use serde::Deserialize;
use serde_json::{Value, json};
mod runtime;
pub use runtime::Controller;

use crate::executor::Executor;
use crate::protocol::{Action, ActionResult};

const SILENCE_LIMIT: Duration = Duration::from_secs(45);
const MAX_BACKOFF: Duration = Duration::from_secs(30);

#[derive(Clone)]
pub struct Settings {
    pub url: String,
    pub token: String,
    /// The machine name; the server checks it is the one the token was made for.
    pub machine: String,
    pub instance_id: String,
    pub max_width: u32,
    pub overlay: bool,
}

/// What the network side asks the screen thread to do.
enum Work {
    Run(Action, std_mpsc::Sender<ActionResult>),
    JobStart(String),
    Status(String),
    JobEnd,
}

#[derive(Deserialize)]
struct Inbound {
    #[serde(rename = "type")]
    kind: String,
    #[serde(default)]
    id: String,
    #[serde(default)]
    action: Option<Value>,
    #[serde(default)]
    text: String,
    #[serde(default)]
    operation: String,
    #[serde(default)]
    code: String,
    /// In "welcome": the machine name the token belongs to.
    #[serde(default)]
    machine: String,
}

/// Runs forever (until the process is stopped).
pub fn run(settings: Settings) -> Result<()> {
    Controller::start(Some(settings))?.wait();
    Ok(())
}

pub fn probe(settings: &Settings) -> Result<()> {
    let rt = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    rt.block_on(runtime::probe(settings))
}

/// Starts the thread that owns the screen, mouse, keyboard and overlay. Returns a handle
/// to it and the screen geometry for the hello message.
fn start_screen_thread(max_width: u32, overlay: bool) -> Result<(std_mpsc::Sender<Work>, Value)> {
    let (tx, rx) = std_mpsc::channel::<Work>();
    let (ready_tx, ready_rx) = std_mpsc::channel::<Result<Value>>();
    thread::spawn(move || {
        let mut ex = match Executor::new(max_width) {
            Ok(ex) => ex,
            Err(e) => {
                let _ = ready_tx.send(Err(e));
                return;
            }
        };
        let screen = json!({"width": ex.screen.shot_w, "height": ex.screen.shot_h, "scale": ex.screen.scale});
        let _ = ready_tx.send(Ok(screen));
        let engine = base64::engine::general_purpose::STANDARD;
        for work in rx {
            match work {
                Work::Run(action, reply) => {
                    ex.set_status(&action.describe());
                    let mut r = ex.run(&action);
                    if let Some(shot) = r.screenshot.as_mut() {
                        shot.png_base64 = Some(engine.encode(&shot.png));
                    }
                    eprintln!(
                        "{} {} ({} ms)",
                        if r.ok { "ok  " } else { "FAIL" },
                        action.describe(),
                        r.elapsed_ms
                    );
                    let _ = reply.send(r);
                }
                Work::JobStart(operation) => {
                    if overlay {
                        ex.show_overlay(&operation);
                        ex.set_status(&format!("starting {operation}"));
                    }
                }
                Work::Status(text) => ex.set_status(&text),
                Work::JobEnd => ex.hide_overlay(),
            }
        }
    });
    let screen = ready_rx.recv().context("screen thread died")??;
    Ok((tx, screen))
}

/// Spreads reconnects by +/-20% so many agents don't retry in lockstep.
fn jitter(d: Duration) -> Duration {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|t| t.subsec_nanos())
        .unwrap_or(0);
    let factor = 0.8 + (nanos % 1000) as f64 / 1000.0 * 0.4;
    d.mul_f64(factor)
}
