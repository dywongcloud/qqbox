use clap::{Parser, Subcommand};
use std::path::PathBuf;

#[derive(Parser)]
#[command(
    name = "laboon",
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
    },
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
        eprintln!("laboon: {message}");
        std::process::exit(1);
    }
}

fn dispatch(cli: Cli) -> Result<(), String> {
    match cli.command {
        Commands::Version => {
            println!("laboon {}", env!("CARGO_PKG_VERSION"));
            println!("host: {}", laboon::HostOs::current().as_str());
            println!("arch: {}", laboon::Arch::host().as_str());
            println!("oci: {}", laboon::oci::runtime::SUPPORTED_OCI_VERSION);
        }
        Commands::Invariants => {
            for statement in laboon::security::audit() {
                println!("{statement}");
            }
        }
        Commands::Abi => {
            laboon::zig::validate_abi().map_err(|e| e.to_string())?;
            println!("{}", laboon::zig::abi_layout());
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
            let mut opts = laboon::Options::new()
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
                laboon::run::program_config(&program, &opts).map_err(|e| e.to_string())?;
            let ctx = laboon::Context::default();
            let plan = laboon::sandbox::prepare(&ctx, &prog, &cfg).map_err(|e| e.to_string())?;
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
            let mut opts = laboon::Options::new()
                .with_args(&args.iter().map(String::as_str).collect::<Vec<_>>())
                .with_pull(pull);
            if let Some(p) = platform {
                opts = opts.with_platform(&p);
            }
            let ctx = laboon::Context::default();
            match laboon::run_image(&ctx, &reference, &opts) {
                Ok(code) => println!("exit: {code}"),
                Err(e) => {
                    println!("image: {reference}");
                    println!(
                        "ref: {}",
                        laboon::oci::ImageRef::parse(&reference)
                            .map_err(|x| x.to_string())?
                            .name()
                    );
                    println!("status: {e}");
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
            let config = laboon::oci::parse_image_config(&bytes).map_err(|e| e.to_string())?;
            let cfg = laboon::oci::to_run_config(&config).map_err(|e| e.to_string())?;
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
            let config = laboon::oci::parse_image_config(&bytes).map_err(|e| e.to_string())?;
            let cfg = laboon::oci::to_run_config(&config).map_err(|e| e.to_string())?;
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
        Commands::PortsCheck { specs } => {
            let mut published = Vec::new();
            for spec in &specs {
                published.push(laboon::net::parse_published_port(spec).map_err(|e| e.to_string())?);
            }
            let forwarder = laboon::net::Forwarder::new(&published).map_err(|e| e.to_string())?;
            let rules = forwarder.plan();
            let mut denied = laboon::net::ForwardRule::from_published(
                &laboon::net::parse_published_port("203.0.113.9:9999:9999/tcp")
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
                    "hosts": forwarder.hosts_file("laboon", &Default::default(), &[]),
                    "resolv_conf": forwarder.resolv_conf(&[], &[]),
                }))
                .unwrap_or_default()
            );
        }
        Commands::ComposePlan { file } => {
            let bytes = read(&file)?;
            let compose = laboon::compose::parse(&bytes).map_err(|e| e.to_string())?;
            let name = file
                .file_stem()
                .map(|s| s.to_string_lossy().into_owned())
                .unwrap_or_else(|| "laboon".to_string());
            let plan = laboon::compose::plan(&compose, &name).map_err(|e| e.to_string())?;
            println!(
                "{}",
                laboon::compose::to_json(&plan).map_err(|e| e.to_string())?
            );
        }
        Commands::ComposeValidate { file } => {
            let bytes = read(&file)?;
            let compose = laboon::compose::parse(&bytes).map_err(|e| e.to_string())?;
            laboon::compose::validate(&compose).map_err(|e| e.to_string())?;
            println!("services: {}", compose.services.len());
            for (name, service) in &compose.services {
                let labels = laboon::compose::service_label_count(&service.labels);
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
                    serde_json::to_string_pretty(&laboon::oci::runtime::features())
                        .unwrap_or_default()
                );
            }
            RuntimeAction::Validate { file, advisory } => {
                let bytes = read(&file)?;
                let spec = laboon::oci::runtime::parse(&bytes).map_err(|e| e.to_string())?;
                let (cfg, rejected) =
                    laboon::oci::runtime::apply(&spec).map_err(|e| e.to_string())?;
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
            let r = laboon::oci::ImageRef::parse(&reference).map_err(|e| e.to_string())?;
            println!("{}", serde_json::to_string_pretty(&serde_json::json!({
                "name": r.name(),
                "reference": r.reference(),
                "registry": r.registry,
                "repository": r.repository,
                "tag": r.tag,
                "digest": r.digest,
                "manifest_url": r.manifest_url(),
                "blob_url": r.blob_url("sha256:0000000000000000000000000000000000000000000000000000000000000000"),
                "platform": format!("linux/{}", laboon::Arch::host().as_str()),
            })).unwrap_or_default());
        }
        Commands::NetworkPlan { file } => {
            let bytes = read(&file)?;
            let compose = laboon::compose::parse(&bytes).map_err(|e| e.to_string())?;
            let name = file
                .file_stem()
                .map(|s| s.to_string_lossy().into_owned())
                .unwrap_or_default();
            let plan = laboon::compose::plan(&compose, &name).map_err(|e| e.to_string())?;
            let forwarder = laboon::net::Forwarder::new(
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
                "hosts": forwarder.hosts_file("laboon", &Default::default(),
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
            let daemon = laboon::daemon::Daemon::new(token);
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
        Commands::Daemon { listen, token } => {
            let daemon = laboon::daemon::Daemon::new(token);
            println!("laboond listening on {listen}");
            println!("bearer token: {}", daemon.token().unwrap_or_default());
            daemon.serve(&listen).map_err(|e| e.to_string())?;
        }
    }
    Ok(())
}
