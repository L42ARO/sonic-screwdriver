// Embeds the icon and version info (assets/local-agent.rc) in local-agent.exe. Without a
// Windows resource compiler the build still succeeds; the agent then falls back to a drawn
// tray icon and Windows shows the file name instead of the app name.
use std::{env, fs, path::PathBuf};

/// The name people see, in Windows and in the app (settings::APP_NAME).
const APP_NAME: &str = "SonicScrew Local Agent";

fn main() {
    println!("cargo:rerun-if-changed=assets/local-agent.rc");
    println!("cargo:rerun-if-changed=assets/icon.ico");
    println!("cargo:rustc-env=SONIC_APP_NAME={APP_NAME}");
    let dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let icon = dir.join("assets").join("icon.ico");
    let rc = fs::read_to_string(dir.join("assets").join("local-agent.rc"))
        .unwrap()
        .replace("@ICON@", &icon.display().to_string().replace('\\', "/"))
        .replace("@NAME@", APP_NAME)
        .replace("@VERSION@", &env::var("CARGO_PKG_VERSION").unwrap())
        .replace("@MAJOR@", &env::var("CARGO_PKG_VERSION_MAJOR").unwrap())
        .replace("@MINOR@", &env::var("CARGO_PKG_VERSION_MINOR").unwrap())
        .replace("@PATCH@", &env::var("CARGO_PKG_VERSION_PATCH").unwrap());
    let out = PathBuf::from(env::var("OUT_DIR").unwrap()).join("local-agent.rc");
    fs::write(&out, rc).unwrap();
    if let Err(e) = embed_resource::compile(&out, embed_resource::NONE).manifest_optional() {
        println!("cargo:warning=local-agent.exe will have no icon or app name: {e}");
    }
}
