// Embeds assets/icon.ico in local-agent.exe (see assets/local-agent.rc). Without a Windows
// resource compiler the build still succeeds; the agent then falls back to a drawn icon.
fn main() {
    println!("cargo:rerun-if-changed=assets/local-agent.rc");
    println!("cargo:rerun-if-changed=assets/icon.ico");
    if let Err(e) =
        embed_resource::compile("assets/local-agent.rc", embed_resource::NONE).manifest_optional()
    {
        println!("cargo:warning=local-agent.exe will have no icon: {e}");
    }
}
