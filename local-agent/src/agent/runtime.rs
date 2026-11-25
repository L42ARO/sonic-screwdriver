use super::*;
use anyhow::{Result, anyhow};
use futures_util::{SinkExt, StreamExt};
use serde_json::{Value, json};
use std::collections::VecDeque;
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use tokio::sync::{mpsc, watch};
use tokio_tungstenite::tungstenite::Message;

#[derive(Clone)]
struct Desired {
    settings: Option<Settings>,
    paused: bool,
    shutdown: bool,
    revision: u64,
}

#[derive(Clone)]
pub struct Status {
    pub message: String,
    pub connected: bool,
    pub busy: bool,
    pub paused: bool,
    pub stopped: bool,
    pub logs: VecDeque<String>,
}
impl Default for Status {
    fn default() -> Self {
        Self {
            message: "Not configured".into(),
            connected: false,
            busy: false,
            paused: false,
            stopped: false,
            logs: VecDeque::new(),
        }
    }
}
type Shared = Arc<Mutex<Status>>;
fn report(state: &Shared, message: impl Into<String>, connected: bool, busy: bool) {
    let message = message.into();
    let mut state = state.lock().unwrap();
    if state.message != message {
        // Errors never include hello payloads, credentials, screenshots or typed content.
        state.logs.push_back(message.clone());
        if state.logs.len() > 100 {
            state.logs.pop_front();
        }
    }
    state.message = message;
    state.connected = connected;
    state.busy = busy;
}

pub struct Controller {
    desired: watch::Sender<Desired>,
    state: Shared,
    thread: Option<JoinHandle<()>>,
}
impl Controller {
    pub fn start(settings: Option<Settings>) -> Result<Self> {
        let (work, screen) = start_screen_thread(
            settings.as_ref().map_or(1280, |s| s.max_width),
            settings.as_ref().is_none_or(|s| s.overlay),
        )?;
        let (tx, rx) = watch::channel(Desired {
            settings,
            paused: false,
            shutdown: false,
            revision: 0,
        });
        let state = Arc::new(Mutex::new(Status::default()));
        let shared = state.clone();
        let thread = thread::spawn(move || {
            match tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
            {
                Ok(rt) => rt.block_on(drive(rx, shared.clone(), work, screen)),
                Err(_) => report(
                    &shared,
                    "Could not start the connection runtime",
                    false,
                    false,
                ),
            }
            shared.lock().unwrap().stopped = true;
        });
        Ok(Self {
            desired: tx,
            state,
            thread: Some(thread),
        })
    }
    pub fn status(&self) -> Status {
        self.state.lock().unwrap().clone()
    }
    pub fn configure(&self, settings: Settings) {
        self.desired.send_modify(|d| {
            d.settings = Some(settings);
            d.paused = false;
            d.revision += 1
        });
    }
    pub fn set_paused(&self, paused: bool) {
        self.desired.send_modify(|d| {
            d.paused = paused;
            d.revision += 1
        });
        self.state.lock().unwrap().paused = paused;
    }
    pub fn shutdown(&self) {
        self.desired.send_modify(|d| {
            d.shutdown = true;
            d.revision += 1
        });
    }
    pub fn wait(mut self) {
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}

fn can_switch(busy: bool, revision: u64, desired: &Desired) -> bool {
    !busy && revision != desired.revision
}

async fn drive(
    mut desired: watch::Receiver<Desired>,
    state: Shared,
    work: std_mpsc::Sender<Work>,
    screen: Value,
) {
    let mut backoff = Duration::from_secs(1);
    let mut revision = 0;
    loop {
        let current = desired.borrow_and_update().clone();
        if current.shutdown {
            break;
        }
        if current.revision != revision {
            backoff = Duration::from_secs(1);
            revision = current.revision;
        }
        state.lock().unwrap().paused = current.paused;
        if current.paused || current.settings.is_none() {
            report(
                &state,
                if current.paused {
                    "Paused"
                } else {
                    "Not configured"
                },
                false,
                false,
            );
            if desired.changed().await.is_err() {
                break;
            }
            continue;
        }
        let settings = current.settings.as_ref().unwrap();
        report(&state, "Connecting…", false, false);
        let outcome = connect(
            settings,
            &screen,
            &work,
            &state,
            &mut desired,
            current.revision,
        )
        .await;
        let _ = work.send(Work::JobEnd);
        let rejection = match outcome {
            Ok(SessionExit::Changed) => continue,
            Ok(SessionExit::Rejected(message, retry)) => {
                report(&state, message, false, false);
                !retry
            }
            Ok(SessionExit::Disconnected) => {
                report(&state, "Disconnected; reconnecting…", false, false);
                backoff = Duration::from_secs(1);
                false
            }
            Err(_) => {
                report(
                    &state,
                    "Server unavailable or secure connection failed; retrying…",
                    false,
                    false,
                );
                false
            }
        };
        if rejection {
            // Invalid credentials/name require a settings change, not endless retries.
            if desired.changed().await.is_err() {
                break;
            }
        } else {
            tokio::select! {
                _=tokio::time::sleep(jitter(backoff))=>{},
                changed=desired.changed()=>{if changed.is_err(){break}},
            }
            backoff = (backoff * 2).min(MAX_BACKOFF);
        }
    }
    let _ = work.send(Work::JobEnd);
    drop(work);
    report(&state, "Stopped", false, false);
}

enum SessionExit {
    Changed,
    Disconnected,
    Rejected(String, bool),
}

async fn handshake(
    settings: &Settings,
    screen: &Value,
    probe: bool,
) -> Result<(
    tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>,
    Inbound,
)> {
    let (mut ws, _) = tokio::time::timeout(
        Duration::from_secs(10),
        tokio_tungstenite::connect_async(settings.url.as_str()),
    )
    .await??;
    ws.send(Message::text(json!({"type":"hello","token":settings.token,"machine":settings.machine,
        "instance_id":settings.instance_id,"probe":probe,"version":env!("CARGO_PKG_VERSION"),"screen":screen}).to_string())).await?;
    let welcome = tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            let message = ws
                .next()
                .await
                .ok_or_else(|| anyhow!("Server closed before welcome"))??;
            if let Message::Ping(p) = message {
                ws.send(Message::Pong(p)).await?;
                continue;
            }
            return Ok::<Inbound, anyhow::Error>(serde_json::from_str(message.to_text()?)?);
        }
    })
    .await??;
    Ok((ws, welcome))
}

pub async fn probe(settings: &Settings) -> Result<()> {
    let (mut ws,welcome)=handshake(settings,&json!({"width":0,"height":0,"scale":1}),true).await.map_err(|_|anyhow!("Cannot reach the server or verify its secure connection. Check the URL and certificate."))?;
    if welcome.kind != "welcome" {
        return Err(anyhow!(rejection_message(&welcome)));
    }
    let _ = ws.close(None).await;
    Ok(())
}
fn rejection_message(welcome: &Inbound) -> String {
    // Never render arbitrary server text into the UI/log; a malicious server can echo a token.
    match welcome.code.as_str(){
        "authentication_failed"=>"Machine token rejected. Add this machine in the dashboard, or regenerate its token, and paste the new token.",
        "machine_in_use"=>"Another computer is already connected with this machine name. Names are unique: give this computer its own name and token, or close the other agent.",
        // The server names the token's machine; show it only if it is a plain machine name.
        "machine_mismatch" if crate::settings::valid_machine_name(&welcome.machine) => {
            return format!("This token is for the machine \"{}\". Set Machine name to {} or use the token made for this name.", welcome.machine, welcome.machine);
        }
        "machine_mismatch"=>"This token is for a different machine. Check the Machine name.",
        _=>"Connection rejected by the server. Check the URL and machine token.",
    }.into()
}

async fn connect(
    settings: &Settings,
    screen: &Value,
    work: &std_mpsc::Sender<Work>,
    state: &Shared,
    desired: &mut watch::Receiver<Desired>,
    revision: u64,
) -> Result<SessionExit> {
    let (ws, welcome) = tokio::select! {
        biased;
        _=desired.changed()=>return Ok(SessionExit::Changed),
        result=handshake(settings,screen,false)=>result?,
    };
    if welcome.kind != "welcome" {
        return Ok(SessionExit::Rejected(
            rejection_message(&welcome),
            welcome.code == "machine_in_use",
        ));
    }
    // The token decides which machine this is; the server says which in its welcome.
    let connected = if welcome.machine.is_empty() {
        "Connected".to_string()
    } else {
        format!("Connected as {}", welcome.machine)
    };
    report(state, format!("{connected} — ready"), true, false);
    let (mut sink, mut stream) = ws.split();
    let (out_tx, mut out_rx) = mpsc::unbounded_channel::<Message>();
    let writer = tokio::spawn(async move {
        while let Some(message) = out_rx.recv().await {
            if sink.send(message).await.is_err() {
                break;
            }
        }
    });
    let mut busy = false;
    let result = loop {
        let next = tokio::select! {
            biased;
            changed=desired.changed()=>{
                if changed.is_err() || can_switch(busy,revision,&desired.borrow()){break Ok(SessionExit::Changed)}
                report(state,"Finishing current job before applying changes…",true,busy);
                continue
            },
            message=tokio::time::timeout(SILENCE_LIMIT,stream.next())=>message,
        };
        let message = match next {
            Ok(Some(Ok(m))) => m,
            _ => break Ok(SessionExit::Disconnected),
        };
        let text = match message {
            Message::Text(t) => t.to_string(),
            Message::Ping(p) => {
                let _ = out_tx.send(Message::Pong(p));
                continue;
            }
            Message::Close(_) => break Ok(SessionExit::Disconnected),
            _ => continue,
        };
        let Ok(message) = serde_json::from_str::<Inbound>(&text) else {
            continue;
        };
        match message.kind.as_str() {
            "action" => {
                let out = out_tx.clone();
                let work = work.clone();
                tokio::task::spawn_blocking(move || {
                    let result = match serde_json::from_value::<Action>(
                        message.action.unwrap_or(Value::Null),
                    ) {
                        Ok(action) => {
                            let (tx, rx) = std_mpsc::channel();
                            if work.send(Work::Run(action, tx)).is_err() {
                                return;
                            }
                            match rx.recv() {
                                Ok(r) => serde_json::to_value(r).unwrap_or(Value::Null),
                                Err(_) => return,
                            }
                        }
                        Err(_) => {
                            json!({"ok":false,"action":"unknown","elapsed_ms":0,"error":"Unsupported action; update the local-agent."})
                        }
                    };
                    let _ = out.send(Message::text(
                        json!({"type":"result","id":message.id,"result":result}).to_string(),
                    ));
                });
            }
            "job_start" => {
                busy = true;
                let _ = work.send(Work::JobStart(message.operation));
                report(state, format!("{connected} — job running"), true, true);
            }
            "status" => {
                let _ = work.send(Work::Status(message.text));
            }
            "job_end" => {
                busy = false;
                let _ = work.send(Work::JobEnd);
                if can_switch(busy, revision, &desired.borrow()) {
                    break Ok(SessionExit::Changed);
                }
                report(state, format!("{connected} — ready"), true, false);
            }
            _ => {}
        }
    };
    writer.abort();
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn configuration_changes_wait_until_job_finishes() {
        let desired = Desired {
            settings: None,
            paused: true,
            shutdown: false,
            revision: 2,
        };
        assert!(!can_switch(true, 1, &desired));
        assert!(can_switch(false, 1, &desired));
        assert!(!can_switch(false, 2, &desired));
    }
    #[test]
    fn rejected_server_text_cannot_echo_credentials() {
        let message: Inbound =
            serde_json::from_value(json!({"type":"error","message":"secret-token"})).unwrap();
        assert!(!rejection_message(&message).contains("secret-token"));
    }
    #[tokio::test(flavor = "current_thread")]
    async fn pause_keeps_active_session_until_job_end() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (release, wait) = tokio::sync::oneshot::channel();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_async(stream).await.unwrap();
            ws.next().await.unwrap().unwrap();
            ws.send(Message::text(r#"{"type":"welcome"}"#))
                .await
                .unwrap();
            ws.send(Message::text(
                r#"{"type":"job_start","operation":"lookup"}"#,
            ))
            .await
            .unwrap();
            wait.await.unwrap();
            ws.send(Message::text(r#"{"type":"job_end"}"#))
                .await
                .unwrap();
            let _ = ws.next().await;
        });
        let settings = Settings {
            url: format!("ws://{address}"),
            token: "test-only-agent-secret".into(),
            machine: "test-machine".into(),
            instance_id: "test".into(),
            max_width: 1280,
            overlay: false,
        };
        let (tx, mut rx) = watch::channel(Desired {
            settings: Some(settings.clone()),
            paused: false,
            shutdown: false,
            revision: 0,
        });
        let state = Arc::new(Mutex::new(Status::default()));
        let observed = state.clone();
        let (work, _received) = std_mpsc::channel();
        let client = tokio::spawn(async move {
            connect(&settings, &json!({}), &work, &state, &mut rx, 0)
                .await
                .unwrap()
        });
        tokio::time::timeout(Duration::from_secs(3), async {
            while !observed.lock().unwrap().busy {
                tokio::task::yield_now().await
            }
        })
        .await
        .unwrap();
        tx.send_modify(|d| {
            d.paused = true;
            d.revision += 1;
        });
        tokio::time::timeout(Duration::from_secs(3), async {
            while !observed.lock().unwrap().message.contains("Finishing") {
                tokio::task::yield_now().await
            }
        })
        .await
        .unwrap();
        assert!(!client.is_finished());
        assert!(observed.lock().unwrap().connected);
        release.send(()).unwrap();
        assert!(matches!(
            tokio::time::timeout(Duration::from_secs(3), client)
                .await
                .unwrap()
                .unwrap(),
            SessionExit::Changed
        ));
        server.await.unwrap();
    }
}
