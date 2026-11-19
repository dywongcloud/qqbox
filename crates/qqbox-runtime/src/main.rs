//! OCI runtime shim for qqbox. Implements the runc/crun command surface
//! (`create`, `start`, `state`, `kill`, `delete`, `features`, `--version`)
//! against the Rust control plane. `create` parses and validates the bundle's
//! `config.json` and writes real state; `start` reports the honest
//! `UnsupportedPlatform` diagnostic because the three native guest-execution
//! backends are not ported to Rust yet. See docs/PORT_STATUS.md.

use clap::{Parser, Subcommand};
use qqbox::oci::runtime;
use serde_json::json;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

#[derive(Parser)]
#[command(
    name = "qqbox-runtime",
    version,
    about = "OCI runtime shim for qqbox sandboxes"
)]
struct Cli {
    /// Directory holding instance state (must be an explicit root, never a host path guess).
    #[arg(long, default_value = "/run/qqbox-runtime")]
    root: PathBuf,
    #[command(subcommand)]
    command: Option<Commands>,
}

#[derive(Subcommand)]
enum Commands {
    /// Validate config.json and create the instance state.
    Create { id: String, bundle: PathBuf },
    /// Attempt to start the guest; reports the native-backend boundary.
    Start { id: String },
    /// Print OCI runtime state as JSON.
    State { id: String },
    /// Record a signal for the instance.
    Kill { id: String, signal: Option<String> },
    /// Remove the instance state.
    Delete { id: String },
    /// Print OCI runtime-spec features.
    Features,
}

fn state_path(root: &Path, id: &str) -> PathBuf {
    root.join(format!("{id}.json"))
}

fn read_state(root: &Path, id: &str) -> Result<serde_json::Value, String> {
    let p = state_path(root, id);
    let bytes = std::fs::read(&p).map_err(|e| format!("cannot read {}: {e}", p.display()))?;
    serde_json::from_slice(&bytes).map_err(|e| format!("bad state {}: {e}", p.display()))
}

fn write_state(root: &Path, id: &str, value: &serde_json::Value) -> Result<(), String> {
    std::fs::create_dir_all(root).map_err(|e| format!("cannot create {}: {e}", root.display()))?;
    let p = state_path(root, id);
    std::fs::write(&p, serde_json::to_vec(value).map_err(|e| e.to_string())?)
        .map_err(|e| format!("cannot write {}: {e}", p.display()))
}

fn fail(msg: impl AsRef<str>) -> ExitCode {
    eprintln!("qqbox-runtime: {}", msg.as_ref());
    ExitCode::FAILURE
}

fn run(cli: Cli) -> ExitCode {
    let Some(command) = cli.command else {
        eprintln!("qqbox-runtime: a command is required (create|start|state|kill|delete|features)");
        return ExitCode::FAILURE;
    };
    let root = &cli.root;
    match command {
        Commands::Features => {
            println!(
                "{}",
                serde_json::to_string_pretty(&runtime::features()).unwrap_or_default()
            );
            ExitCode::SUCCESS
        }
        Commands::Create { id, bundle } => {
            if id.is_empty() || id.contains('/') || id.contains("..") {
                return fail("invalid container id");
            }
            let config = bundle.join("config.json");
            let bytes = match std::fs::read(&config) {
                Ok(b) => b,
                Err(e) => return fail(format!("cannot read {}: {e}", config.display())),
            };
            let spec = match runtime::parse(&bytes) {
                Ok(s) => s,
                Err(e) => return fail(format!("config.json parse: {e}")),
            };
            if let Err(e) = runtime::validate(&spec) {
                return fail(format!("config.json validation: {e}"));
            }
            let (cfg, rejected) = match runtime::apply(&spec) {
                Ok(v) => v,
                Err(e) => return fail(format!("config.json apply: {e}")),
            };
            let state = runtime::state(
                &id,
                "created",
                0,
                &bundle.to_string_lossy(),
                &spec.annotations,
            );
            if let Err(e) = write_state(root, &id, &state) {
                return fail(e);
            }
            println!(
                "{}",
                json!({
                    "id": id,
                    "status": "created",
                    "bundle": bundle.to_string_lossy(),
                    "ociVersion": spec.oci_version,
                    "root": cfg.root,
                    "args": cfg.args,
                    "unsupportedSecurityKeysRejected": rejected,
                    "nativeBackend": "not ported to Rust; start will report UnsupportedPlatform"
                })
            );
            ExitCode::SUCCESS
        }
        Commands::Start { id } => {
            let mut state = match read_state(root, &id) {
                Ok(s) => s,
                Err(e) => return fail(e),
            };
            if state.get("status").and_then(|v| v.as_str()) != Some("created") {
                return fail("instance is not in created state");
            }
            state["status"] = json!("stopped");
            let _ = write_state(root, &id, &state);
            fail("unsupported platform: native guest execution backends (Linux memfd/seccomp/hop, macOS Mach/Seatbelt/HVF, Windows LPAC) are not ported to Rust; see docs/PORT_STATUS.md")
        }
        Commands::State { id } => match read_state(root, &id) {
            Ok(s) => {
                println!("{s}");
                ExitCode::SUCCESS
            }
            Err(e) => fail(e),
        },
        Commands::Kill { id, signal } => {
            let mut state = match read_state(root, &id) {
                Ok(s) => s,
                Err(e) => return fail(e),
            };
            state["signal"] = json!(signal.unwrap_or_else(|| "SIGTERM".into()));
            match write_state(root, &id, &state) {
                Ok(()) => ExitCode::SUCCESS,
                Err(e) => fail(e),
            }
        }
        Commands::Delete { id } => {
            let p = state_path(root, &id);
            match std::fs::remove_file(&p) {
                Ok(()) => ExitCode::SUCCESS,
                Err(e) => fail(format!("cannot remove {}: {e}", p.display())),
            }
        }
    }
}

fn main() -> ExitCode {
    run(Cli::parse())
}
