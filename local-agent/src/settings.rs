//! Only connection preferences live here; installation paths stay on the compute-box.
use anyhow::{Context, Result, anyhow, bail};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Path, PathBuf},
    ptr::{null, null_mut},
};
use windows_sys::Win32::{
    Foundation::{CloseHandle, ERROR_ALREADY_EXISTS, ERROR_FILE_NOT_FOUND, GetLastError},
    Security::{
        Credentials::{
            CRED_PERSIST_LOCAL_MACHINE, CRED_TYPE_GENERIC, CREDENTIALW, CredDeleteW, CredFree,
            CredReadW, CredWriteW,
        },
        Cryptography::{BCRYPT_USE_SYSTEM_PREFERRED_RNG, BCryptGenRandom},
    },
    Storage::FileSystem::{MOVEFILE_REPLACE_EXISTING, MOVEFILE_WRITE_THROUGH, MoveFileExW},
    System::{
        Registry::{
            HKEY_CURRENT_USER, KEY_SET_VALUE, REG_SZ, RegCloseKey, RegCreateKeyExW,
            RegDeleteValueW, RegSetValueExW,
        },
        Threading::CreateMutexW,
    },
};

pub fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(Some(0)).collect()
}
pub fn random_id() -> Result<String> {
    let mut bytes = [0u8; 16];
    if unsafe {
        BCryptGenRandom(
            null_mut(),
            bytes.as_mut_ptr(),
            bytes.len() as u32,
            BCRYPT_USE_SYSTEM_PREFERRED_RNG,
        )
    } < 0
    {
        bail!("Could not generate a configuration ID")
    }
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}
pub fn directory() -> Result<PathBuf> {
    if let Some(path) = std::env::var_os("SONIC_CONFIG_DIR") {
        return Ok(PathBuf::from(path));
    }
    Ok(
        PathBuf::from(std::env::var_os("LOCALAPPDATA").context("LOCALAPPDATA is not set")?)
            .join("SonicScrewdriver"),
    )
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Profile {
    pub version: u32,
    pub server_url: String,
    pub machine: String,
    pub start_at_signin: bool,
    pub instance_id: String,
    pub credential_id: String,
}
impl Profile {
    pub fn new() -> Result<Self> {
        Ok(Self {
            version: 1,
            server_url: "http://localhost:8080".into(),
            machine: "".into(),
            start_at_signin: false,
            instance_id: random_id()?,
            credential_id: random_id()?,
        })
    }
    pub fn validate(&self) -> Result<()> {
        if self.version != 1 {
            bail!("Unsupported saved settings version")
        }
        validate_machine(&self.machine)?;
        endpoint(&self.server_url)?;
        for id in [&self.instance_id, &self.credential_id] {
            if id.len() != 32 || !id.bytes().all(|b| b.is_ascii_hexdigit()) {
                bail!("Invalid saved configuration ID")
            }
        }
        Ok(())
    }
    pub fn connection(&self, token: String) -> Result<crate::agent::Settings> {
        self.validate()?;
        if token.trim().is_empty() || token.contains('\0') {
            bail!("Enter the agent authentication token printed by the compute-box")
        }
        Ok(crate::agent::Settings {
            url: endpoint(&self.server_url)?,
            machine: self.machine.clone(),
            token,
            instance_id: self.instance_id.clone(),
            max_width: 1280,
            overlay: true,
        })
    }
}
pub fn validate_machine(name: &str) -> Result<()> {
    if name.is_empty()
        || name.len() > 64
        || !name.as_bytes()[0].is_ascii_alphabetic()
        || !name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
    {
        bail!(
            "Machine name must start with a letter and use at most 64 letters, digits, underscores or hyphens"
        )
    }
    Ok(())
}
pub fn endpoint(address: &str) -> Result<String> {
    let mut url = url::Url::parse(address.trim()).map_err(|_| {
        anyhow!(
            "Enter a full server URL, such as http://localhost:8080 or https://sonic.example.com"
        )
    })?;
    if !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        bail!("Server URL cannot contain credentials, a query or a fragment")
    }
    let scheme = match url.scheme() {
        "http" => "ws",
        "https" => "wss",
        "ws" => "ws",
        "wss" => "wss",
        _ => bail!("Use http, https, ws or wss"),
    };
    url.host_str().context("Server URL needs a hostname")?;
    let scheme = scheme.to_string();
    url.set_scheme(&scheme)
        .map_err(|_| anyhow!("Invalid server URL"))?;
    let path = url.path().trim_end_matches('/');
    let path = if path.ends_with("/v1/agent") {
        path.to_string()
    } else {
        format!("{path}/v1/agent")
    };
    url.set_path(&path);
    Ok(url.to_string())
}
/// True when the address is valid but sends the token and screenshots unencrypted to
/// another machine. Plain connections are allowed; the settings screen warns about them.
pub fn is_unencrypted_remote(address: &str) -> bool {
    let Ok(endpoint) = endpoint(address) else {
        return false;
    };
    let Ok(url) = url::Url::parse(&endpoint) else {
        return false;
    };
    url.scheme() == "ws"
        && !matches!(
            url.host_str(),
            Some("localhost" | "127.0.0.1" | "[::1]" | "::1")
        )
}
pub fn load() -> Result<Option<Profile>> {
    load_from(&directory()?)
}
fn load_from(dir: &Path) -> Result<Option<Profile>> {
    let path = dir.join("config.json");
    let raw = match fs::read(path) {
        Ok(raw) => raw,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e.into()),
    };
    let profile: Profile = serde_json::from_slice(&raw)
        .context("Saved settings could not be read. Open Settings to repair them.")?;
    profile.validate()?;
    Ok(Some(profile))
}
fn credential_target(id: &str) -> Vec<u16> {
    wide(&format!("SonicScrewdriver/agent/{id}"))
}
pub fn read_token(profile: &Profile) -> Result<String> {
    unsafe {
        let mut credential = null_mut();
        if CredReadW(
            credential_target(&profile.credential_id).as_ptr(),
            CRED_TYPE_GENERIC,
            0,
            &mut credential,
        ) == 0
        {
            bail!(
                "Agent token is unavailable in Windows Credential Manager. Enter it again in Settings."
            )
        }
        if (*credential).CredentialBlobSize == 0 || (*credential).CredentialBlob.is_null() {
            CredFree(credential.cast());
            bail!("Stored agent token is empty. Enter it again in Settings.")
        }
        let raw = std::slice::from_raw_parts(
            (*credential).CredentialBlob,
            (*credential).CredentialBlobSize as usize,
        )
        .to_vec();
        CredFree(credential.cast());
        String::from_utf8(raw).context("Stored agent token is invalid")
    }
}
fn write_token(profile: &Profile, token: &str) -> Result<()> {
    if token.is_empty() || token.len() > 2500 {
        bail!("Agent token must contain 1 to 2500 bytes")
    }
    let target = credential_target(&profile.credential_id);
    let user = wide(&profile.machine);
    let mut credential: CREDENTIALW = unsafe { std::mem::zeroed() };
    credential.Type = CRED_TYPE_GENERIC;
    credential.TargetName = target.as_ptr() as *mut _;
    credential.CredentialBlobSize = token.len() as u32;
    credential.CredentialBlob = token.as_ptr() as *mut _;
    credential.Persist = CRED_PERSIST_LOCAL_MACHINE;
    credential.UserName = user.as_ptr() as *mut _;
    if unsafe { CredWriteW(&credential, 0) } == 0 {
        return Err(std::io::Error::last_os_error())
            .context("Saving agent token to Windows Credential Manager");
    }
    Ok(())
}
pub fn save(profile: &mut Profile, token: &str) -> Result<()> {
    save_in(profile, token, &directory()?, set_startup)
}
fn save_in(
    profile: &mut Profile,
    token: &str,
    dir: &Path,
    mut startup: impl FnMut(bool) -> Result<()>,
) -> Result<()> {
    profile.validate()?;
    let previous = load_from(dir).ok().flatten();
    let startup_changed =
        profile.start_at_signin != previous.as_ref().is_some_and(|p| p.start_at_signin);
    fs::create_dir_all(dir)?;
    let old_id = profile.credential_id.clone();
    let temporary = dir.join(format!("config-{}.tmp", random_id()?));
    profile.credential_id = random_id()?;
    if let Err(error) = write_token(profile, token) {
        profile.credential_id = old_id;
        return Err(error);
    }
    let result = (|| -> Result<()> {
        fs::write(&temporary, serde_json::to_vec_pretty(profile)?)?;
        if startup_changed {
            startup(profile.start_at_signin)?;
        }
        replace_file(&temporary, &dir.join("config.json"))?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::remove_file(&temporary);
        if startup_changed {
            let _ = startup(previous.as_ref().is_some_and(|p| p.start_at_signin));
        }
        unsafe {
            CredDeleteW(
                credential_target(&profile.credential_id).as_ptr(),
                CRED_TYPE_GENERIC,
                0,
            );
        }
        profile.credential_id = old_id;
    } else if let Some(previous) = previous {
        unsafe {
            CredDeleteW(
                credential_target(&previous.credential_id).as_ptr(),
                CRED_TYPE_GENERIC,
                0,
            );
        }
    }
    result
}
fn replace_file(from: &Path, to: &Path) -> Result<()> {
    if unsafe {
        MoveFileExW(
            wide(&from.to_string_lossy()).as_ptr(),
            wide(&to.to_string_lossy()).as_ptr(),
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
        )
    } == 0
    {
        return Err(std::io::Error::last_os_error()).context("Saving settings");
    }
    Ok(())
}
pub fn set_startup(enabled: bool) -> Result<()> {
    unsafe {
        let mut key = null_mut();
        let code = RegCreateKeyExW(
            HKEY_CURRENT_USER,
            wide(r"Software\Microsoft\Windows\CurrentVersion\Run").as_ptr(),
            0,
            null_mut(),
            0,
            KEY_SET_VALUE,
            null(),
            &mut key,
            null_mut(),
        );
        if code != 0 {
            return Err(std::io::Error::from_raw_os_error(code as i32))
                .context("Opening sign-in preferences");
        }
        let name = wide("SonicScrewdriver");
        let code = if enabled {
            let value = wide(&format!("\"{}\"", std::env::current_exe()?.display()));
            RegSetValueExW(
                key,
                name.as_ptr(),
                0,
                REG_SZ,
                value.as_ptr().cast(),
                (value.len() * 2) as u32,
            )
        } else {
            RegDeleteValueW(key, name.as_ptr())
        };
        RegCloseKey(key);
        check_startup_result(code, enabled)?;
    }
    Ok(())
}

fn check_startup_result(code: u32, enabled: bool) -> Result<()> {
    // Deleting an absent Run value is already the desired disabled state.
    if code == 0 || (!enabled && code == ERROR_FILE_NOT_FOUND) {
        return Ok(());
    }
    bail!(
        "Could not update start at sign-in: {}",
        std::io::Error::from_raw_os_error(code as i32)
    )
}

pub struct SingleInstance(windows_sys::Win32::Foundation::HANDLE);
impl SingleInstance {
    pub fn acquire() -> Result<Option<Self>> {
        Self::acquire_for(&directory()?)
    }
    fn acquire_for(directory: &Path) -> Result<Option<Self>> {
        let dir = directory.to_string_lossy().to_lowercase();
        // A deterministic name per user configuration, independent of executable path.
        let hash = dir.bytes().fold(0xcbf29ce484222325u64, |v, b| {
            (v ^ b as u64).wrapping_mul(0x100000001b3)
        });
        let handle = unsafe {
            CreateMutexW(
                null(),
                0,
                wide(&format!("Local\\SonicScrewdriver-{hash:x}")).as_ptr(),
            )
        };
        if handle.is_null() {
            return Err(std::io::Error::last_os_error()).context("Starting local agent");
        }
        if unsafe { GetLastError() } == ERROR_ALREADY_EXISTS {
            unsafe { CloseHandle(handle) };
            return Ok(None);
        }
        Ok(Some(Self(handle)))
    }
}
impl Drop for SingleInstance {
    fn drop(&mut self) {
        unsafe {
            CloseHandle(self.0);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn addresses_are_normalized_and_plain_remote_is_flagged() {
        assert_eq!(
            endpoint("http://localhost:8080/").unwrap(),
            "ws://localhost:8080/v1/agent"
        );
        assert_eq!(
            endpoint("http://203.0.113.7:6080").unwrap(),
            "ws://203.0.113.7:6080/v1/agent"
        );
        assert!(is_unencrypted_remote("http://203.0.113.7:6080"));
        assert!(is_unencrypted_remote("ws://example.com/v1/agent"));
        for quiet in [
            "http://localhost:8080",
            "http://127.0.0.1:6080",
            "https://example.com",
            "wss://example.com",
            "not a url",
        ] {
            assert!(!is_unencrypted_remote(quiet), "{quiet}")
        }
        assert_eq!(
            endpoint("https://example.com/sonic/").unwrap(),
            "wss://example.com/sonic/v1/agent"
        );
        assert_eq!(
            endpoint("wss://example.com/v1/agent").unwrap(),
            "wss://example.com/v1/agent"
        );
        for bad in [
            "https://user:pass@example.com",
            "https://example.com?token=x",
            "file:///c:/foo",
        ] {
            assert!(endpoint(bad).is_err(), "{bad}")
        }
    }
    #[test]
    fn machine_names_match_server_rules() {
        for good in ["desk", "desk-1", "A_b"] {
            assert!(validate_machine(good).is_ok())
        }
        for bad in ["", "1desk", "my desk", "desk/one"] {
            assert!(validate_machine(bad).is_err())
        }
    }
    #[test]
    fn preferences_contain_only_credential_reference() {
        let mut p = Profile::new().unwrap();
        p.machine = "desk".into();
        let raw = serde_json::to_string(&p).unwrap();
        assert!(!raw.contains("token"));
        assert!(raw.contains("credential_id"));
        p.validate().unwrap();
    }
    #[test]
    fn credential_manager_roundtrip_uses_an_isolated_test_entry() {
        let mut p = Profile::new().unwrap();
        p.machine = "test-desk".into();
        struct Cleanup(String);
        impl Drop for Cleanup {
            fn drop(&mut self) {
                unsafe {
                    CredDeleteW(credential_target(&self.0).as_ptr(), CRED_TYPE_GENERIC, 0);
                }
            }
        }
        let _cleanup = Cleanup(p.credential_id.clone());
        let token = format!("test-only-{}", random_id().unwrap());
        write_token(&p, &token).unwrap();
        assert!(read_token(&p).unwrap() == token);
    }
    #[test]
    fn saved_settings_can_be_changed_without_storing_a_secret_in_json() {
        let dir =
            std::env::temp_dir().join(format!("sonic-settings-test-{}", random_id().unwrap()));
        let mut p = Profile::new().unwrap();
        p.machine = "test-desk".into();
        let token = format!("test-only-{}", random_id().unwrap());
        save_in(&mut p, &token, &dir, |_| {
            panic!("Saving with sign-in disabled must not touch the registry")
        })
        .unwrap();
        let first = load_from(&dir).unwrap().unwrap();
        assert!(read_token(&first).unwrap() == token);
        let raw = fs::read_to_string(dir.join("config.json")).unwrap();
        assert!(!raw.contains(&token));
        p.machine = "renamed-desk".into();
        p.server_url = "https://example.com".into();
        save_in(&mut p, &token, &dir, |_| {
            panic!("Unchanged sign-in settings must not touch the registry")
        })
        .unwrap();
        let second = load_from(&dir).unwrap().unwrap();
        assert_eq!(second.machine, "renamed-desk");
        assert_eq!(second.instance_id, first.instance_id);
        assert!(read_token(&first).is_err());
        assert!(read_token(&second).unwrap() == token);
        unsafe {
            CredDeleteW(
                credential_target(&second.credential_id).as_ptr(),
                CRED_TYPE_GENERIC,
                0,
            );
        }
        fs::remove_file(dir.join("config.json")).unwrap();
        fs::remove_dir(dir).unwrap();
    }
    #[test]
    fn repeated_launches_do_not_acquire_the_same_instance() {
        let path = std::env::temp_dir().join(random_id().unwrap());
        let first = SingleInstance::acquire_for(&path).unwrap().unwrap();
        assert!(SingleInstance::acquire_for(&path).unwrap().is_none());
        drop(first);
        assert!(SingleInstance::acquire_for(&path).unwrap().is_some());
    }
    #[test]
    fn disabling_absent_startup_value_succeeds() {
        check_startup_result(ERROR_FILE_NOT_FOUND, false).unwrap();
        assert!(check_startup_result(ERROR_FILE_NOT_FOUND, true).is_err());
        assert!(check_startup_result(5, false).is_err());
    }
}
