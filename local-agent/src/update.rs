//! Updates. The hosted server says which agent version is the newest
//! (GET /updates/local-agent); when it is newer than this one, the desktop app offers it,
//! and only after the user accepts does it download, verify and install it.
//!
//! Every update must be signed with the release key, whose public half is built in below.
//! The download is checked against the SHA-256 the server lists, and that digest, with the
//! version, against the signature. So neither a changed server row nor a swapped download
//! can make agents run code the release key didn't sign.
//!
//! Installing swaps the program file in place: Windows lets a running .exe be renamed, so
//! the old file becomes `<exe>.old` (deleted on the next start) and the new one takes its
//! name. Settings, the token and "Start at sign-in" stay as they are.

use std::fs;
use std::io::Read;
use std::path::{Path, PathBuf};
use std::time::Duration;

use anyhow::{Context, Result, bail};
use base64::Engine;
use serde::Deserialize;

/// This build's version.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");
/// Where agents ask for the newest version. SONIC_UPDATE_URL overrides it ("off" disables).
const DEFAULT_UPDATE_URL: &str = "https://app.sonicscrew.click/updates/local-agent";
/// The release key (ed25519). Its private half never leaves the maintainer's machine.
const UPDATE_PUBLIC_KEY: &str = "EGcVsCTvewjp9ADdhhBw4sz8P8554XiX59R9UFNfXfY=";
/// How often the desktop app asks.
pub const CHECK_EVERY: Duration = Duration::from_secs(60 * 60);
/// Where people can download the agent by hand if an update can't install itself.
pub const DOWNLOAD_PAGE: &str = "https://github.com/L42ARO/sonic-screwdriver/releases/latest";
const MAX_DOWNLOAD: u64 = 200 * 1024 * 1024;

/// A newer version the server offers.
#[derive(Clone, Debug, Deserialize)]
pub struct Offer {
    pub version: String,
    pub url: String,
    pub sha256: String,
    pub signature: String,
    #[serde(default)]
    pub notes: String,
}

fn check_url() -> Option<String> {
    match std::env::var("SONIC_UPDATE_URL") {
        Ok(v) if v.trim().eq_ignore_ascii_case("off") => None,
        Ok(v) if !v.trim().is_empty() => Some(v.trim().to_string()),
        _ => Some(DEFAULT_UPDATE_URL.to_string()),
    }
}

fn agent() -> ureq::Agent {
    ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(15 * 60)))
        .timeout_connect(Some(Duration::from_secs(15)))
        .http_status_as_error(false)
        .user_agent(format!("SonicScrew-Local-Agent/{VERSION}"))
        .build()
        .into()
}

/// Asks the server for the newest version. Returns it only if it is newer than this one.
pub fn check() -> Result<Option<Offer>> {
    let Some(url) = check_url() else {
        return Ok(None);
    };
    let mut response = agent()
        .get(&url)
        .query("version", VERSION)
        .call()
        .context("asking for updates")?;
    match response.status().as_u16() {
        200 => {}
        404 => return Ok(None), // nothing published yet
        code => bail!("the update server answered {code}"),
    }
    let text = response
        .body_mut()
        .with_config()
        .limit(64 * 1024)
        .read_to_string()
        .context("reading the update offer")?;
    let offer: Offer = serde_json::from_str(&text).context("reading the update offer")?;
    Ok(is_newer(&offer.version, VERSION).then_some(offer))
}

/// Compares dotted versions numerically ("0.10.0" is newer than "0.9.3").
pub fn is_newer(candidate: &str, current: &str) -> bool {
    fn parts(v: &str) -> Option<Vec<u64>> {
        v.trim().split('.').map(|p| p.parse().ok()).collect()
    }
    match (parts(candidate), parts(current)) {
        (Some(a), Some(b)) => a > b,
        _ => false,
    }
}

/// What the release key signs; the server's `signedMessage` builds the same text.
fn signed_message(version: &str, sha256_hex: &str) -> String {
    format!(
        "sonic-screwdriver/local-agent {version} {}",
        sha256_hex.to_ascii_lowercase()
    )
}

/// Checks `bytes` against the offer's digest and the digest against the signature.
fn verify(offer: &Offer, bytes: &[u8], public_key: &[u8]) -> Result<()> {
    let digest = ring::digest::digest(&ring::digest::SHA256, bytes);
    let actual: String = digest.as_ref().iter().map(|b| format!("{b:02x}")).collect();
    if !actual.eq_ignore_ascii_case(offer.sha256.trim()) {
        bail!("the download doesn't match the published checksum")
    }
    let signature = base64::engine::general_purpose::STANDARD
        .decode(offer.signature.trim())
        .context("the update's signature is malformed")?;
    ring::signature::UnparsedPublicKey::new(&ring::signature::ED25519, public_key)
        .verify(
            signed_message(&offer.version, &actual).as_bytes(),
            &signature,
        )
        .map_err(|_| anyhow::anyhow!("the update isn't signed by the SonicScrew release key"))
}

fn release_key() -> Vec<u8> {
    base64::engine::general_purpose::STANDARD
        .decode(UPDATE_PUBLIC_KEY)
        .expect("built-in key")
}

fn sibling(exe: &Path, suffix: &str) -> PathBuf {
    let mut name = exe.as_os_str().to_owned();
    name.push(suffix);
    PathBuf::from(name)
}

/// Downloads and verifies the offered version next to this program, as `<exe>.new`.
pub fn download(offer: &Offer) -> Result<PathBuf> {
    // http only for a server on this computer (testing); the signature is checked either way.
    let local = ["http://127.0.0.1:", "http://localhost:"]
        .iter()
        .any(|p| offer.url.starts_with(p));
    if !offer.url.starts_with("https://") && !local {
        bail!("the update's download link isn't https")
    }
    let mut response = agent()
        .get(&offer.url)
        .call()
        .context("downloading the update")?;
    if response.status().as_u16() != 200 {
        bail!(
            "downloading the update: the server answered {}",
            response.status()
        )
    }
    let mut bytes = Vec::new();
    response
        .body_mut()
        .with_config()
        .limit(MAX_DOWNLOAD)
        .reader()
        .read_to_end(&mut bytes)
        .context("downloading the update")?;
    verify(offer, &bytes, &release_key())?;
    let exe = std::env::current_exe().context("finding this program")?;
    let staged = sibling(&exe, ".new");
    fs::write(&staged, &bytes)
        .with_context(|| format!("saving the update next to {}", exe.display()))?;
    Ok(staged)
}

/// Puts the staged file in place of this program. The running copy keeps working until
/// the process exits; `relaunch` then starts the new one.
pub fn install(staged: &Path) -> Result<()> {
    let exe = std::env::current_exe().context("finding this program")?;
    let old = sibling(&exe, ".old");
    let _ = fs::remove_file(&old);
    fs::rename(&exe, &old).with_context(|| format!("moving {} aside", exe.display()))?;
    if let Err(e) = fs::rename(staged, &exe) {
        let _ = fs::rename(&old, &exe); // put the working copy back
        return Err(e).with_context(|| format!("installing the update as {}", exe.display()));
    }
    Ok(())
}

/// Starts the (now updated) program again, as the desktop app.
pub fn relaunch() -> Result<()> {
    let exe = std::env::current_exe().context("finding this program")?;
    std::process::Command::new(&exe)
        .spawn()
        .with_context(|| format!("starting {}", exe.display()))?;
    Ok(())
}

/// Removes what a previous update left behind. Best effort.
pub fn clean_up() {
    if let Ok(exe) = std::env::current_exe() {
        for suffix in [".old", ".new"] {
            let _ = fs::remove_file(sibling(&exe, suffix));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ring::signature::KeyPair;

    #[test]
    fn versions_compare_numerically() {
        assert!(is_newer("0.2.0", "0.1.0"));
        assert!(is_newer("0.10.0", "0.9.3"));
        assert!(is_newer("1.0.0", "0.99.99"));
        assert!(!is_newer("0.1.0", "0.1.0"));
        assert!(!is_newer("0.0.9", "0.1.0"));
        assert!(!is_newer("garbage", "0.1.0"));
    }

    #[test]
    fn built_in_key_is_a_valid_ed25519_key() {
        assert_eq!(release_key().len(), 32);
    }

    #[test]
    fn only_signed_matching_downloads_pass() {
        let rng = ring::rand::SystemRandom::new();
        let pkcs8 = ring::signature::Ed25519KeyPair::generate_pkcs8(&rng).unwrap();
        let pair = ring::signature::Ed25519KeyPair::from_pkcs8(pkcs8.as_ref()).unwrap();
        let file = b"new agent".to_vec();
        let sha: String = ring::digest::digest(&ring::digest::SHA256, &file)
            .as_ref()
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        let sign = |version: &str| {
            base64::engine::general_purpose::STANDARD
                .encode(pair.sign(signed_message(version, &sha).as_bytes()))
        };
        let offer = Offer {
            version: "0.2.0".into(),
            url: "https://example.test/a.exe".into(),
            sha256: sha.to_uppercase(),
            signature: sign("0.2.0"),
            notes: String::new(),
        };
        let key = pair.public_key().as_ref();
        verify(&offer, &file, key).unwrap();
        assert!(verify(&offer, b"tampered", key).is_err(), "changed file");
        let moved = Offer {
            signature: sign("0.1.9"),
            ..offer.clone()
        };
        assert!(
            verify(&moved, &file, key).is_err(),
            "signature from another version"
        );
        assert!(verify(&offer, &file, &release_key()).is_err(), "other key");
    }
}
