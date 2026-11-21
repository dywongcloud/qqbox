use clap::{Parser, Subcommand};
use std::io::Write as _;
use std::net::{TcpListener, TcpStream};
use std::path::PathBuf;

#[derive(Parser)]
#[command(
    name = "qqbox",
    version,
    about = "Run untrusted Linux programs and OCI containers safely, without a VM"
)]
struct Cli {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand)]
enum Commands {
    Version,
    Invariants,
    Abi,
    Backend {
        #[command(subcommand)]
        action: BackendAction,
    },
    AddressSpace,
    Run {
        program: String,
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
        #[arg(long)]
        root: Option<String>,
        #[arg(long)]
        net: bool,
        #[arg(long)]
        no_net: bool,
        #[arg(long)]
        allow_domain: Vec<String>,
        #[arg(long)]
        publish: Vec<String>,
        #[arg(long)]
        volume: Vec<String>,
        #[arg(long)]
        label: Vec<String>,
        #[arg(long)]
        no_rewrite: bool,
        #[arg(long)]
        reject_rwx: bool,
        #[arg(long)]
        max_memory: Option<u64>,
    },
    RunImage {
        reference: String,
        #[arg(long)]
        platform: Option<String>,
        #[arg(long)]
        pull: bool,
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    ConfigValidate {
        file: PathBuf,
    },
    OciInspect {
        file: PathBuf,
    },
    PortsCheck {
        specs: Vec<String>,
    },
    VfsResolve {
        guest_path: String,
        #[arg(long)]
        root: String,
    },
    ComposePlan {
        file: PathBuf,
    },
    ComposeValidate {
        file: PathBuf,
    },
    #[command(name = "runtime-spec")]
    RuntimeSpec {
        #[command(subcommand)]
        action: RuntimeAction,
    },
    ImagePlan {
        reference: String,
    },
    NetworkPlan {
        file: PathBuf,
    },
    DaemonProbe {
        path: String,
        #[arg(long)]
        method: Option<String>,
        #[arg(long)]
        body: Option<PathBuf>,
        #[arg(long)]
        token: Option<String>,
        #[arg(long)]
        auth: Option<String>,
    },
    Daemon {
        #[arg(long, default_value = "127.0.0.1:2375")]
        listen: String,
        #[arg(long)]
        token: Option<String>,
        /// Independent restore capability secret. When omitted, the daemon
        /// generates one from OS entropy and prints its hex digest at startup.
        #[arg(long)]
        migration_secret: Option<String>,
    },
    Migrate {
        #[command(subcommand)]
        action: MigrateAction,
    },
}

#[derive(Subcommand)]
enum MigrateAction {
    Features,
    Checkpoint {
        #[arg(long)]
        config: PathBuf,
        #[arg(long)]
        out: PathBuf,
    },
    Restore {
        #[arg(long)]
        input: PathBuf,
        #[arg(long)]
        out: PathBuf,
    },
    Verify {
        #[arg(long)]
        input: PathBuf,
    },
    Serve {
        #[arg(long)]
        secret: String,
        #[arg(long)]
        port_file: PathBuf,
        #[arg(long)]
        ready_fifo: PathBuf,
        #[arg(long)]
        out: PathBuf,
    },
    Send {
        #[arg(long)]
        secret: String,
        #[arg(long)]
        endpoint: String,
        #[arg(long)]
        input: PathBuf,
        #[arg(long)]
        delta: Vec<PathBuf>,
        #[arg(long)]
        final_out: Option<PathBuf>,
    },
}

#[derive(Subcommand)]
enum BackendAction {
    Capabilities,
    Witness,
    Install,
    Dispatch { syscall: u64, args: Vec<u64> },
    Memory,
    ExecElf { guest: PathBuf },
}

#[derive(Subcommand)]
enum RuntimeAction {
    Validate {
        file: PathBuf,
        #[arg(long)]
        advisory: bool,
    },
    Features,
}

fn read(path: &PathBuf) -> Result<Vec<u8>, String> {
    std::fs::read(path).map_err(|e| format!("{}: {e}", path.display()))
}

fn main() {
    let cli = Cli::parse();
    let outcome = dispatch(cli);
    if let Err(message) = outcome {
        eprintln!("qqbox: {message}");
        std::process::exit(1);
    }
}

fn dispatch(cli: Cli) -> Result<(), String> {
    match cli.command {
        Commands::Version => {
            println!("qqbox {}", env!("CARGO_PKG_VERSION"));
            println!("host: {}", qqbox::HostOs::current().as_str());
            println!("arch: {}", qqbox::Arch::host().as_str());
            println!("oci: {}", qqbox::oci::runtime::SUPPORTED_OCI_VERSION);
        }
        Commands::Invariants => {
            for statement in qqbox::security::audit() {
                println!("{statement}");
            }
        }
        Commands::Abi => {
            qqbox::zig::validate_abi().map_err(|e| e.to_string())?;
            println!("{}", qqbox::zig::abi_layout());
        }
        Commands::Backend { action } => {
            let platform = qqbox::backend::current();
            match action {
                BackendAction::Capabilities => {
                    let caps = platform.capabilities().map_err(|e| e.to_string())?;
                    println!(
                        "{}",
                        serde_json::to_string_pretty(&caps).unwrap_or_default()
                    );
                }
                BackendAction::Install => {
                    platform.install_backstop().map_err(|e| e.to_string())?;
                    println!("backend={} backstop_installed=true", platform.name());
                }
                BackendAction::Witness => {
                    let report = platform.witness().map_err(|e| e.to_string())?;
                    println!(
                        "{}",
                        serde_json::to_string_pretty(&report).unwrap_or_default()
                    );
                    if !report.match_ok {
                        return Err(
                            "backend witness failed: guest result did not match supervisor".into(),
                        );
                    }
                }
                BackendAction::Memory => {
                    #[cfg(target_os = "linux")]
                    {
                        let (clock_rc, uname_rc, tv_sec, sysname) =
                            qqbox::backend::linux::memory_witness().map_err(|e| e.to_string())?;
                        println!(
                            "{}",
                            serde_json::json!({
                                "clock_gettime_result": clock_rc,
                                "uname_result": uname_rc,
                                "tv_sec": tv_sec,
                                "sysname": sysname,
                                "guest_memory": "bounded process_vm_writev, real buffer",
                            })
                        );
                        if clock_rc != 16 || uname_rc != 390 || tv_sec == 0 || sysname != "Linux" {
                            return Err("bounded guest-memory witness failed".into());
                        }
                    }
                    #[cfg(not(target_os = "linux"))]
                    return Err("backend memory requires a Linux host".into());
                }
                BackendAction::ExecElf { guest } => {
                    #[cfg(target_os = "linux")]
                    {
                        let report = qqbox::backend::guest::run_elf(
                            &guest,
                            &qqbox::backend::guest::GuestOptions::default(),
                        )
                        .map_err(|e| e.to_string())?;
                        println!(
                            "{}",
                            serde_json::to_string_pretty(&report).unwrap_or_default()
                        );
                        if !report.match_ok {
                            return Err(
                                "real ELF guest did not exit with the supervisor sentinel".into()
                            );
                        }
                    }
                    #[cfg(not(target_os = "linux"))]
                    {
                        let _ = guest;
                        return Err("exec-elf requires a Linux aarch64 host".into());
                    }
                }
                BackendAction::Dispatch { syscall, args } => {
                    let mut padded = [0u64; 6];
                    for (i, v) in args.iter().take(6).enumerate() {
                        padded[i] = *v;
                    }
                    #[cfg(target_os = "linux")]
                    {
                        let (nr, ret) = qqbox::backend::linux::dispatch_witness(syscall, padded)
                            .map_err(|e| e.to_string())?;
                        println!(
                            "{}",
                            serde_json::json!({
                                "syscall": nr, "args": padded, "result": ret,
                                "host": platform.name(),
                            })
                        );
                    }
                    #[cfg(not(target_os = "linux"))]
                    {
                        let _ = (syscall, padded);
                        return Err(
                            "backend dispatch requires a Linux host; no syscall was served".into(),
                        );
                    }
                }
            }
        }
        Commands::AddressSpace => {
            println!("decision: one address space per sandbox instance");
            println!("reason: process-scoped seccomp/Landlock/Seatbelt/LPAC backstops, the PROT_R hop alias, fork copy-on-write and the process-global stub window all require separate page tables");
            println!("shared: supervisor control plane, OCI/compose state, image cache, rewrite cache, daemon");
            println!("detail: docs/SINGLE_ADDRESS_SPACE.md");
        }
        Commands::Run {
            program,
            args,
            root,
            net,
            no_net,
            allow_domain,
            publish,
            volume,
            label,
            no_rewrite,
            reject_rwx,
            max_memory,
        } => {
            let mut opts = qqbox::Options::new()
                .with_args(&args.iter().map(String::as_str).collect::<Vec<_>>())
                .with_no_rewrite(no_rewrite)
                .with_reject_rwx(reject_rwx);
            if let Some(bytes) = max_memory {
                opts = opts.with_max_memory(bytes);
            }
            if let Some(r) = root {
                opts = opts.with_root(&r);
            }
            opts = opts.with_net(net).with_no_net(no_net);
            for domain in &allow_domain {
                opts = opts.with_allow_domains(&[domain.as_str()]);
            }
            for spec in &publish {
                opts = opts.with_publish(spec);
            }
            for v in &volume {
                let (source, target) = v.split_once(':').unwrap_or((v.as_str(), v.as_str()));
                opts = opts.with_volume(source, target);
            }
            for l in &label {
                let (k, value) = l.split_once('=').unwrap_or((l.as_str(), ""));
                let mut labels = opts.labels.clone();
                labels.insert(k.to_string(), value.to_string());
                opts = opts.with_labels(labels);
            }
            let (prog, cfg) =
                qqbox::run::program_config(&program, &opts).map_err(|e| e.to_string())?;
            let ctx = qqbox::Context::default();
            let plan = qqbox::sandbox::prepare(&ctx, &prog, &cfg).map_err(|e| e.to_string())?;
            let mut value = serde_json::to_value(&plan).unwrap_or_default();
            value["backend"] = serde_json::json!(
                "native guest execution is not wired on this host; the control plane above is live"
            );
            println!(
                "{}",
                serde_json::to_string_pretty(&value).unwrap_or_default()
            );
        }
        Commands::RunImage {
            reference,
            platform,
            pull,
            args,
        } => {
            let mut opts = qqbox::Options::new()
                .with_args(&args.iter().map(String::as_str).collect::<Vec<_>>())
                .with_pull(pull);
            if let Some(p) = platform {
                opts = opts.with_platform(&p);
            }
            let ctx = qqbox::Context::default();
            match qqbox::run_image(&ctx, &reference, &opts) {
                Ok(code) => println!("exit: {code}"),
                Err(e) => {
                    // A failed pull or an unprepared guest is a command failure,
                    // not a successful run. Print the diagnostic AND exit 1 so
                    // callers (and CI) do not mistake it for a completed run.
                    println!("image: {reference}");
                    println!(
                        "ref: {}",
                        qqbox::oci::ImageRef::parse(&reference)
                            .map_err(|x| x.to_string())?
                            .name()
                    );
                    println!("status: {e}");
                    return Err(e.to_string());
                }
            }
        }
        Commands::ConfigValidate { file } => {
            let bytes = read(&file)?;
            let raw: serde_json::Value =
                serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
            if raw.get("HostConfig").is_some() || raw.get("Image").is_some() {
                return Err(format!(
                    "{} is a Docker container-create body, not an OCI image config",
                    file.display()
                ));
            }
            let config = qqbox::oci::parse_image_config(&bytes).map_err(|e| e.to_string())?;
            let cfg = qqbox::oci::to_run_config(&config).map_err(|e| e.to_string())?;
            let value = serde_json::json!({
                "architecture": config.architecture,
                "os": config.os,
                "variant": config.variant,
                "os_version": config.os_version,
                "os_features": config.os_features,
                "created": config.created,
                "author": config.author,
                "history": config.history.len(),
                "rootfs": {"type": config.rootfs.type_, "diff_ids": config.rootfs.diff_ids.len()},
                "entrypoint": cfg.entrypoint,
                "cmd": cfg.cmd,
                "env": cfg.env,
                "working_dir": cfg.working_dir,
                "user": cfg.user_spec,
                "labels": cfg.labels,
                "exposed_ports": cfg.exposed_ports.len(),
                "volumes": cfg.volumes.len(),
                "stop_signal": cfg.stop_signal,
                "healthcheck": cfg.healthcheck.is_some(),
                "hostname": cfg.hostname,
                "domainname": cfg.domainname,
                "tty": cfg.tty,
                "attach_stdin": config.config.attach_stdin,
                "attach_stdout": config.config.attach_stdout,
                "attach_stderr": config.config.attach_stderr,
                "network_disabled": config.config.network_disabled,
                "mac_address": config.config.mac_address,
                "shell": config.config.shell,
                "args_escaped": config.config.args_escaped,
                "on_build": config.config.on_build,
            });
            println!(
                "{}",
                serde_json::to_string_pretty(&value).unwrap_or_default()
            );
        }
        Commands::OciInspect { file } => {
            let bytes = read(&file)?;
            let config = qqbox::oci::parse_image_config(&bytes).map_err(|e| e.to_string())?;
            let cfg = qqbox::oci::to_run_config(&config).map_err(|e| e.to_string())?;
            println!(
                "{}",
                serde_json::to_string_pretty(&config).unwrap_or_default()
            );
            println!(
                "constructs: labels={} exposed={} volumes={} healthcheck={} history={}",
                cfg.labels.len(),
                cfg.exposed_ports.len(),
                cfg.volumes.len(),
                cfg.healthcheck.is_some(),
                config.history.len()
            );
        }
        Commands::VfsResolve { guest_path, root } => {
            let ns = qqbox::vfs::Namespace::new(std::path::Path::new(&root))
                .map_err(|e| e.to_string())?;
            let r = ns.resolve(&guest_path).map_err(|e| e.to_string())?;
            println!(
                "{}",
                serde_json::to_string_pretty(&serde_json::json!({
                    "guest": r.guest, "host": r.host.to_string_lossy(),
                    "read_only": r.read_only,
                }))
                .unwrap_or_default()
            );
        }
        Commands::PortsCheck { specs } => {
            let mut published = Vec::new();
            for spec in &specs {
                published.push(qqbox::net::parse_published_port(spec).map_err(|e| e.to_string())?);
            }
            let forwarder = qqbox::net::Forwarder::new(&published).map_err(|e| e.to_string())?;
            let rules = forwarder.plan();
            let mut denied = qqbox::net::ForwardRule::from_published(
                &qqbox::net::parse_published_port("203.0.113.9:9999:9999/tcp")
                    .map_err(|e| e.to_string())?,
            )
            .map_err(|e| e.to_string())?;
            let mut wildcard_spoof = denied.clone();
            wildcard_spoof.published = true;
            denied.published = false;
            println!(
                "{}",
                serde_json::to_string_pretty(&serde_json::json!({
                    "published": published,
                    "forward_plan": rules,
                    "unpublished_rule": denied,
                    "denied": forwarder.check(&denied).is_err(),
                    "wildcard_spoof_denied": forwarder.check(&wildcard_spoof).is_err(),
                    "hosts": forwarder.hosts_file("qqbox", &Default::default(), &[]),
                    "resolv_conf": forwarder.resolv_conf(&[], &[]),
                }))
                .unwrap_or_default()
            );
        }
        Commands::ComposePlan { file } => {
            let bytes = read(&file)?;
            let compose = qqbox::compose::parse(&bytes).map_err(|e| e.to_string())?;
            let name = file
                .file_stem()
                .map(|s| s.to_string_lossy().into_owned())
                .unwrap_or_else(|| "qqbox".to_string());
            let plan = qqbox::compose::plan(&compose, &name).map_err(|e| e.to_string())?;
            println!(
                "{}",
                qqbox::compose::to_json(&plan).map_err(|e| e.to_string())?
            );
        }
        Commands::ComposeValidate { file } => {
            let bytes = read(&file)?;
            let compose = qqbox::compose::parse(&bytes).map_err(|e| e.to_string())?;
            qqbox::compose::validate(&compose).map_err(|e| e.to_string())?;
            println!("services: {}", compose.services.len());
            for (name, service) in &compose.services {
                let labels = qqbox::compose::service_label_count(&service.labels);
                println!(
                    "  {name}: image={} ports={} volumes={} labels={}",
                    service.image,
                    service.ports.len(),
                    service.volumes.len(),
                    labels
                );
            }
            println!("networks: {}", compose.networks.len());
            println!("volumes: {}", compose.volumes.len());
        }
        Commands::RuntimeSpec { action } => match action {
            RuntimeAction::Features => {
                println!(
                    "{}",
                    serde_json::to_string_pretty(&qqbox::oci::runtime::features())
                        .unwrap_or_default()
                );
            }
            RuntimeAction::Validate { file, advisory } => {
                let bytes = read(&file)?;
                let spec = qqbox::oci::runtime::parse(&bytes).map_err(|e| e.to_string())?;
                let (cfg, rejected) =
                    qqbox::oci::runtime::apply(&spec).map_err(|e| e.to_string())?;
                println!(
                    "{}",
                    serde_json::to_string_pretty(&serde_json::json!({
                        "oci_version": spec.oci_version,
                        "root": spec.root,
                        "readonly_root": cfg.readonly_rootfs,
                        "mounts": cfg.mounts,
                        "volumes": cfg.volumes,
                        "hostname": cfg.hostname,
                        "annotations": spec.annotations,
                        "namespaces": spec.linux.as_ref().map(|l| &l.namespaces),
                        "rejected_fail_closed": rejected,
                        "args": cfg.args,
                        "env": cfg.env,
                    }))
                    .unwrap_or_default()
                );
                if !advisory && !rejected.is_empty() {
                    return Err(format!(
                        "runtime spec contains {} unsupported security constructs; rerun with --advisory to inspect them",
                        rejected.len()
                    ));
                }
            }
        },
        Commands::ImagePlan { reference } => {
            let r = qqbox::oci::ImageRef::parse(&reference).map_err(|e| e.to_string())?;
            println!("{}", serde_json::to_string_pretty(&serde_json::json!({
                "name": r.name(),
                "reference": r.reference(),
                "registry": r.registry,
                "repository": r.repository,
                "tag": r.tag,
                "digest": r.digest,
                "manifest_url": r.manifest_url(),
                "blob_url": r.blob_url("sha256:0000000000000000000000000000000000000000000000000000000000000000"),
                "platform": format!("linux/{}", qqbox::Arch::host().as_str()),
            })).unwrap_or_default());
        }
        Commands::NetworkPlan { file } => {
            let bytes = read(&file)?;
            let compose = qqbox::compose::parse(&bytes).map_err(|e| e.to_string())?;
            let name = file
                .file_stem()
                .map(|s| s.to_string_lossy().into_owned())
                .unwrap_or_default();
            let plan = qqbox::compose::plan(&compose, &name).map_err(|e| e.to_string())?;
            let forwarder = qqbox::net::Forwarder::new(
                &plan
                    .services
                    .iter()
                    .flat_map(|s| s.config.published_ports.clone())
                    .collect::<Vec<_>>(),
            )
            .map_err(|e| e.to_string())?;
            println!("{}", serde_json::to_string_pretty(&serde_json::json!({
                "project": plan.project,
                "networks": plan.networks,
                "volumes": plan.volumes,
                "forward_plan": forwarder.plan(),
                "hosts": forwarder.hosts_file("qqbox", &Default::default(),
                    &plan.services.iter().map(|s| (s.name.clone(), std::net::IpAddr::V4(std::net::Ipv4Addr::new(10, 88, 0, 2)))).collect::<Vec<_>>()),
                "resolv_conf": forwarder.resolv_conf(&[], &[]),
                "order": plan.order,
            })).unwrap_or_default());
        }
        Commands::DaemonProbe {
            path,
            method,
            body,
            token,
            auth,
        } => {
            let daemon = qqbox::daemon::Daemon::new(token).map_err(|e| e.to_string())?;
            let effective_auth = auth.or_else(|| daemon.token().map(|t| format!("Bearer {t}")));
            let bytes = match body {
                Some(p) => read(&p)?,
                None => Vec::new(),
            };
            let response = daemon.handle(
                method.as_deref().unwrap_or("GET"),
                &path,
                "",
                &bytes,
                effective_auth.as_deref(),
            );
            println!("status: {}", response.status);
            println!("content-type: {}", response.content_type);
            println!("{}", String::from_utf8_lossy(&response.body));
        }
        Commands::Daemon {
            listen,
            token,
            migration_secret,
        } => {
            let daemon = qqbox::daemon::Daemon::with_migration_secret(token, migration_secret)
                .map_err(|e| e.to_string())?;
            println!("qqboxd listening on {listen}");
            println!("bearer token: {}", daemon.token().unwrap_or_default());
            println!("migration capability: {}", daemon.migration_capability());
            daemon.serve(&listen).map_err(|e| e.to_string())?;
        }
        Commands::Migrate { action } => dispatch_migrate(action)?,
    }
    Ok(())
}

fn migration_state_from(config: &PathBuf) -> Result<qqbox::migrate::MigrationState, String> {
    let bytes = read(config)?;
    let cfg: qqbox::Config =
        serde_json::from_slice(&bytes).map_err(|e| format!("config parse: {e}"))?;
    let root = cfg.root.as_deref().unwrap_or("/");
    let mut ns =
        qqbox::vfs::Namespace::new(std::path::Path::new(root)).map_err(|e| e.to_string())?;
    for mount in &cfg.mounts {
        ns.mount(mount).map_err(|e| e.to_string())?;
    }
    Ok(qqbox::migrate::MigrationState::capture(
        &cfg,
        &qqbox::sandbox::GuestState::default(),
        &ns,
    ))
}

fn decode_state_file(path: &PathBuf) -> Result<qqbox::migrate::MigrationState, String> {
    let mut file = std::fs::File::open(path).map_err(|e| e.to_string())?;
    let frame = qqbox::migrate::read_frame(&mut file).map_err(|e| e.to_string())?;
    if frame.kind != qqbox::migrate::Kind::State {
        return Err(format!("expected State frame, got {:?}", frame.kind));
    }
    qqbox::migrate::decode_state(&frame.payload).map_err(|e| e.to_string())
}

fn write_state_file(path: &PathBuf, state: &qqbox::migrate::MigrationState) -> Result<(), String> {
    let mut file = std::fs::File::create(path).map_err(|e| e.to_string())?;
    let payload = qqbox::migrate::state_payload(state).map_err(|e| e.to_string())?;
    qqbox::migrate::write_frame(&mut file, qqbox::migrate::Kind::State, &payload)
        .map_err(|e| e.to_string())
}

fn dispatch_migrate(action: MigrateAction) -> Result<(), String> {
    use qqbox::migrate as m;
    match action {
        MigrateAction::Features => {
            println!(
                "{}",
                serde_json::to_string_pretty(&m::features()).unwrap_or_default()
            );
        }
        MigrateAction::Checkpoint { config, out } => {
            let state = migration_state_from(&config)?.normalize();
            write_state_file(&out, &state)?;
            let d = state.digest().map_err(|e| e.to_string())?;
            println!("checkpoint_digest={}", hex::encode(d));
            println!("guestExecution={:?}", state.boundary.guest_execution);
            println!(
                "frame_bytes={}",
                std::fs::metadata(&out).map(|m| m.len()).unwrap_or(0)
            );
        }
        MigrateAction::Restore { input, out } => {
            let state = decode_state_file(&input)?;
            let applied = state.apply().map_err(|e| e.to_string())?;
            let mut recaptured =
                m::MigrationState::capture(&applied.config, &applied.guest, &applied.namespace);
            recaptured.generation = state.generation;
            let ok = recaptured.normalize() == state.normalize();
            let d = state.digest().map_err(|e| e.to_string())?;
            let mut file = std::fs::File::create(&out).map_err(|e| e.to_string())?;
            file.write_all(
                serde_json::to_string_pretty(&state.normalize())
                    .unwrap_or_default()
                    .as_bytes(),
            )
            .map_err(|e| e.to_string())?;
            println!("restore_digest={}", hex::encode(d));
            println!("capture_apply_identity={ok}");
            println!(
                "normalize_idempotent={}",
                state.normalize().normalize() == state.normalize()
            );
            if !ok {
                return Err("capture(apply(state)) != state".to_owned());
            }
        }
        MigrateAction::Verify { input } => {
            let raw = read(&input)?;
            let mut cursor = std::io::Cursor::new(raw.as_slice());
            let frame = m::read_frame(&mut cursor).map_err(|e| e.to_string())?;
            let state = m::decode_state(&frame.payload).map_err(|e| e.to_string())?;
            let applied = state.apply().map_err(|e| e.to_string())?;
            let mut recaptured =
                m::MigrationState::capture(&applied.config, &applied.guest, &applied.namespace);
            recaptured.generation = state.generation;
            let laws = [
                (
                    "capture_apply_identity",
                    recaptured.normalize() == state.normalize(),
                ),
                (
                    "normalize_idempotent",
                    state.normalize().normalize() == state.normalize(),
                ),
                (
                    "digest_canonical",
                    state.digest().map_err(|e| e.to_string())?
                        == state.normalize().digest().map_err(|e| e.to_string())?,
                ),
                ("delta_idempotent", {
                    let d = state.delta(&state);
                    let one = state.apply_delta(&d).map_err(|e| e.to_string())?;
                    let two = one.apply_delta(&d).map_err(|e| e.to_string())?;
                    one == two && m::MigrationState::is_empty_delta(&d)
                }),
            ];
            for (name, ok) in laws {
                println!("{name}={ok}");
                if !ok {
                    return Err(format!("law {name} failed"));
                }
            }
            // Live rejection probes, all in memory: no test files on disk.
            let mut corrupted = raw.clone();
            if let Some(b) = corrupted.get_mut(m::HEADER_LEN + 2) {
                *b ^= 0x01;
            }
            let corrupt = m::read_frame(&mut std::io::Cursor::new(corrupted.as_slice()))
                .err()
                .map(|e| e.to_string())
                .unwrap_or_default();
            let truncated = m::read_frame(&mut std::io::Cursor::new(
                &raw[..raw.len().saturating_sub(1)],
            ))
            .err()
            .map(|e| e.to_string())
            .unwrap_or_default();
            let mut oversize = raw.clone();
            if oversize.len() > 10 {
                oversize[6..10].copy_from_slice(&(m::MAX_FRAME + 1).to_be_bytes());
            }
            let oversized = m::read_frame(&mut std::io::Cursor::new(oversize.as_slice()))
                .err()
                .map(|e| e.to_string())
                .unwrap_or_default();
            println!("corrupt_rejected={}", corrupt.contains("digest"));
            println!(
                "truncated_rejected={}",
                truncated.contains("digest read") || truncated.contains("payload read")
            );
            println!("oversize_rejected={}", oversized.contains("MAX_FRAME"));
            println!("corrupt_error={corrupt}");
            println!("truncated_error={truncated}");
            println!("oversize_error={oversized}");
            if !corrupt.contains("digest")
                || !oversized.contains("MAX_FRAME")
                || !(truncated.contains("digest read") || truncated.contains("payload read"))
            {
                return Err("a malformed frame was accepted".to_owned());
            }
        }
        MigrateAction::Serve {
            secret,
            port_file,
            ready_fifo,
            out,
        } => {
            let listener = TcpListener::bind("127.0.0.1:0").map_err(|e| e.to_string())?;
            let port = listener.local_addr().map_err(|e| e.to_string())?.port();
            std::fs::write(&port_file, port.to_string()).map_err(|e| e.to_string())?;
            // The FIFO write blocks until a reader opens it: event-driven
            // readiness, no sleep or periodic recheck.
            let mut fifo = std::fs::File::create(&ready_fifo).map_err(|e| e.to_string())?;
            fifo.write_all(b"READY\n").map_err(|e| e.to_string())?;
            fifo.flush().map_err(|e| e.to_string())?;
            drop(fifo);
            let (stream, _) = listener.accept().map_err(|e| e.to_string())?;
            let mut reader = stream.try_clone().map_err(|e| e.to_string())?;
            let mut writer = stream;
            let cap = m::Capability::derive(secret.as_bytes());
            let done = m::receive(&mut reader, &mut writer, &cap).map_err(|e| e.to_string())?;
            write_state_file(&out, done.state())?;
            println!("receive_digest={}", hex::encode(done.digest()));
            println!("receive_rounds={}", done.rounds());
            println!(
                "receive_peer_digest={}",
                done.peer_digest().map(hex::encode).unwrap_or_default()
            );
        }
        MigrateAction::Send {
            secret,
            endpoint,
            input,
            delta,
            final_out,
        } => {
            let state = decode_state_file(&input)?;
            let mut deltas = Vec::with_capacity(delta.len());
            for path in &delta {
                deltas.push(migration_state_from(path)?.normalize());
            }
            let stream = TcpStream::connect(&endpoint).map_err(|e| e.to_string())?;
            let mut reader = stream.try_clone().map_err(|e| e.to_string())?;
            let mut writer = stream;
            let cap = m::Capability::derive(secret.as_bytes());
            let done = m::send(&mut reader, &mut writer, state, &cap, &deltas)
                .map_err(|e| e.to_string())?;
            println!("send_digest={}", hex::encode(done.digest()));
            println!(
                "send_peer_digest={}",
                done.peer_digest().map(hex::encode).unwrap_or_default()
            );
            println!("send_rounds={}", done.rounds());
            if let Some(path) = &final_out {
                write_state_file(path, done.state())?;
            }
            match done.peer_digest() {
                Some(p) if p == done.digest() => println!("source_destination_match=true"),
                _ => return Err("source/destination digest mismatch".to_owned()),
            }
        }
    }
    Ok(())
}
