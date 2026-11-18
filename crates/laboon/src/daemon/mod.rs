use crate::config::Config;
use crate::error::{Error, Result};
use crate::types::*;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::sync::Mutex;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Response {
    pub status: u16,
    pub content_type: &'static str,
    pub body: Vec<u8>,
}

impl Response {
    pub fn json(status: u16, value: &serde_json::Value) -> Response {
        Response {
            status,
            content_type: "application/json",
            body: serde_json::to_vec(value).unwrap_or_default(),
        }
    }

    pub fn text(status: u16, text: &str) -> Response {
        Response {
            status,
            content_type: "text/plain",
            body: text.as_bytes().to_vec(),
        }
    }

    pub fn empty(status: u16) -> Response {
        Response {
            status,
            content_type: "application/json",
            body: Vec::new(),
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct HostConfig {
    #[serde(default)]
    pub binds: Vec<String>,
    #[serde(default)]
    pub mounts: Vec<serde_json::Value>,
    #[serde(rename = "PortBindings", default)]
    pub port_bindings: BTreeMap<String, Vec<PortBinding>>,
    #[serde(rename = "PublishAllPorts", default)]
    pub publish_all_ports: bool,
    #[serde(rename = "NetworkMode", default)]
    pub network_mode: String,
    #[serde(rename = "Dns", default)]
    pub dns: Vec<String>,
    #[serde(rename = "DnsSearch", default)]
    pub dns_search: Vec<String>,
    #[serde(rename = "DnsOptions", default)]
    pub dns_options: Vec<String>,
    #[serde(rename = "ExtraHosts", default)]
    pub extra_hosts: Vec<String>,
    #[serde(rename = "Links", default)]
    pub links: Vec<String>,
    #[serde(rename = "CapAdd", default)]
    pub cap_add: Vec<String>,
    #[serde(rename = "CapDrop", default)]
    pub cap_drop: Vec<String>,
    #[serde(rename = "SecurityOpt", default)]
    pub security_opt: Vec<String>,
    #[serde(default)]
    pub privileged: bool,
    #[serde(default)]
    pub devices: Vec<serde_json::Value>,
    #[serde(default)]
    pub sysctls: BTreeMap<String, String>,
    #[serde(default)]
    pub ulimits: Vec<serde_json::Value>,
    #[serde(rename = "RestartPolicy", default)]
    pub restart_policy: serde_json::Value,
    #[serde(rename = "LogConfig", default)]
    pub log_config: serde_json::Value,
    #[serde(default)]
    pub resources: serde_json::Value,
    #[serde(rename = "ShmSize", default)]
    pub shm_size: u64,
    #[serde(rename = "ReadonlyRootfs", default)]
    pub readonly_rootfs: bool,
    #[serde(default)]
    pub init: bool,
    #[serde(rename = "OomScoreAdj", default)]
    pub oom_score_adj: i32,
    #[serde(default)]
    pub annotations: LabelMap,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PortBinding {
    #[serde(rename = "HostIp", default)]
    pub host_ip: String,
    #[serde(rename = "HostPort", default)]
    pub host_port: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct NetworkingConfig {
    #[serde(rename = "EndpointsConfig", default)]
    pub endpoints: BTreeMap<String, serde_json::Value>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct CreateRequest {
    #[serde(default, rename = "Image", alias = "image")]
    pub image: String,
    #[serde(rename = "Cmd", default)]
    pub cmd: Vec<String>,
    #[serde(rename = "Entrypoint", default)]
    pub entrypoint: Vec<String>,
    #[serde(rename = "Env", default)]
    pub env: Vec<String>,
    #[serde(rename = "WorkingDir", default)]
    pub working_dir: String,
    #[serde(rename = "User", default)]
    pub user: String,
    #[serde(rename = "Labels", default)]
    pub labels: LabelMap,
    #[serde(rename = "ExposedPorts", default)]
    pub exposed_ports: BTreeMap<String, serde_json::Value>,
    #[serde(rename = "Volumes", default)]
    pub volumes: BTreeMap<String, serde_json::Value>,
    #[serde(rename = "HostConfig", default)]
    pub host_config: HostConfig,
    #[serde(rename = "NetworkingConfig", default)]
    pub networking_config: NetworkingConfig,
    #[serde(rename = "StopSignal", default)]
    pub stop_signal: String,
    #[serde(rename = "StopTimeout", default)]
    pub stop_timeout: u32,
    #[serde(rename = "Healthcheck", default)]
    pub healthcheck: serde_json::Value,
    #[serde(rename = "Tty", default)]
    pub tty: bool,
    #[serde(rename = "OpenStdin", default)]
    pub open_stdin: bool,
    #[serde(rename = "AttachStdin", default)]
    pub attach_stdin: bool,
    #[serde(rename = "AttachStdout", default)]
    pub attach_stdout: bool,
    #[serde(rename = "AttachStderr", default)]
    pub attach_stderr: bool,
    #[serde(rename = "Hostname", default)]
    pub hostname: String,
    #[serde(rename = "Domainname", default)]
    pub domainname: String,
    #[serde(rename = "MacAddress", default)]
    pub mac_address: String,
    #[serde(rename = "NetworkDisabled", default)]
    pub network_disabled: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum ContainerState {
    #[default]
    Created,
    Running,
    Paused,
    Exited,
    Dead,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Container {
    pub id: Id,
    pub name: String,
    pub image: String,
    pub config: Config,
    pub labels: LabelMap,
    pub state: ContainerState,
    pub ports: Vec<PublishedPort>,
    pub mounts: Vec<Mount>,
    pub created: String,
    pub health: String,
    pub restart_count: u32,
    pub exit_code: i32,
    #[serde(skip)]
    pub cancel: std::sync::Arc<std::sync::atomic::AtomicBool>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ImageEntry {
    pub id: Id,
    pub reference: String,
    pub labels: LabelMap,
    pub size: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NetworkEntry {
    pub id: Id,
    pub name: String,
    pub driver: String,
    pub internal: bool,
    pub aliases: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VolumeEntry {
    pub name: String,
    pub driver: String,
    pub source: Option<String>,
    pub containers: Vec<String>,
}

#[derive(Debug, Default)]
pub struct State {
    pub containers: BTreeMap<Id, Container>,
    pub images: BTreeMap<Id, ImageEntry>,
    pub networks: BTreeMap<Id, NetworkEntry>,
    pub volumes: BTreeMap<String, VolumeEntry>,
    next: u64,
    pulls: u64,
}

fn hex_id(seed: u64) -> String {
    let mut h: u64 = 0xcbf2_9ce4_8422_2325 ^ seed;
    let mut out = String::with_capacity(64);
    for _ in 0..8 {
        h ^= h << 13;
        h ^= h >> 7;
        h ^= h << 17;
        out.push_str(&format!("{:016x}", h));
    }
    out.truncate(64);
    out
}

fn unsupported(key: &str) -> Error {
    Error::Config(format!("unsupported HostConfig key: {key}"))
}

impl State {
    pub fn new() -> Self {
        State::default()
    }

    pub fn create_container(&mut self, name: &str, req: &CreateRequest) -> Result<Container> {
        if req.image.is_empty() {
            return Err(Error::Config("image is required".into()));
        }
        for (k, v) in &req.labels {
            validate_label_key(k)?;
            validate_label_value(v)?;
        }
        let mut cfg = Config {
            image: req.image.clone(),
            container_name: name.to_string(),
            ..Config::default()
        };
        cfg.entrypoint = req.entrypoint.clone();
        cfg.cmd = req.cmd.clone();
        cfg.args = req.entrypoint.clone();
        cfg.args.extend(req.cmd.iter().cloned());
        cfg.env = if req.env.is_empty() {
            Config::default_env()
        } else {
            req.env.clone()
        };
        cfg.working_dir = req.working_dir.clone();
        cfg.dir = req.working_dir.clone();
        cfg.user_spec = req.user.clone();
        cfg.labels = req.labels.clone();
        cfg.hostname = req.hostname.clone();
        cfg.domainname = req.domainname.clone();
        cfg.stop_signal = if req.stop_signal.is_empty() {
            "SIGTERM".to_string()
        } else {
            req.stop_signal.clone()
        };
        cfg.stop_timeout = req.stop_timeout;
        cfg.tty = req.tty;
        cfg.stdin_open = req.open_stdin;
        cfg.readonly_rootfs = req.host_config.readonly_rootfs;
        cfg.privileged = req.host_config.privileged;
        cfg.capabilities = req.host_config.cap_add.clone();
        cfg.no_net = req.network_disabled;
        cfg.exposed_ports = req
            .exposed_ports
            .keys()
            .filter_map(|k| crate::net::ports::parse_port_spec(k).ok())
            .collect();
        cfg.volumes = req
            .volumes
            .keys()
            .map(|p| VolumeSpec {
                name: String::new(),
                container_path: p.clone(),
                driver: "local".to_string(),
                read_only: false,
                propagation: MountPropagation::Private,
                source: None,
            })
            .collect();
        for bind in &req.host_config.binds {
            let mut parts = bind.splitn(3, ':');
            let source = parts.next().unwrap_or("").to_string();
            let target = parts.next().unwrap_or("").to_string();
            let flags = parts.next().unwrap_or("");
            if target.is_empty() {
                return Err(Error::Config(format!("bad bind {bind}")));
            }
            cfg.mounts.push(Mount {
                host: source.clone(),
                guest: target.clone(),
                read_only: flags.split(',').any(|f| f == "ro"),
            });
            cfg.volumes.push(VolumeSpec {
                name: source,
                container_path: target,
                driver: "local".to_string(),
                read_only: flags.split(',').any(|f| f == "ro"),
                propagation: MountPropagation::Private,
                source: None,
            });
        }
        for (spec, bindings) in &req.host_config.port_bindings {
            let parsed = crate::net::ports::parse_published_port(spec)
                .map_err(|e| Error::Config(format!("bad PortBindings key {spec}: {e}")))?;
            for binding in bindings {
                let host_ip = if binding.host_ip.is_empty() {
                    parsed.host_ip
                } else {
                    binding
                        .host_ip
                        .parse()
                        .map_err(|_| Error::Config("bad HostIp".into()))?
                };
                let host_port = if binding.host_port.is_empty() {
                    parsed.host_port
                } else {
                    binding
                        .host_port
                        .parse()
                        .map_err(|_| Error::Config("bad HostPort".into()))?
                };
                cfg.published_ports.push(PublishedPort {
                    host_ip,
                    host_port,
                    host_port_end: parsed.host_port_end,
                    container_port: parsed.container_port,
                    container_port_end: parsed.container_port_end,
                    protocol: parsed.protocol,
                });
            }
        }
        crate::net::Forwarder::new(&cfg.published_ports)?;
        match req.host_config.network_mode.as_str() {
            "" | "bridge" | "default" => cfg.network_mode = NetworkMode::Bridge,
            "host" => cfg.network_mode = NetworkMode::Host,
            "none" => {
                cfg.network_mode = NetworkMode::None;
                cfg.no_net = true;
            }
            other if other.starts_with("container:") => cfg.network_mode = NetworkMode::Container,
            other => return Err(unsupported(other)),
        }
        cfg.dns = req.host_config.dns.clone();
        cfg.dns_search = req.host_config.dns_search.clone();
        for host in &req.host_config.extra_hosts {
            if let Some((name, ip)) = host.split_once(':') {
                cfg.extra_hosts.insert(name.to_string(), ip.to_string());
            }
        }
        if !req.host_config.security_opt.is_empty() {
            for opt in &req.host_config.security_opt {
                if opt.starts_with("seccomp=") {
                    let value = opt.trim_start_matches("seccomp=");
                    cfg.seccomp_profile = if value == "unconfined" {
                        SeccompProfile::Unconfined
                    } else {
                        SeccompProfile::Path(value.to_string())
                    };
                } else if opt.starts_with("apparmor=") {
                    cfg.apparmor_profile = Some(opt.trim_start_matches("apparmor=").to_string());
                } else if opt == "no-new-privileges" {
                    cfg.capabilities.push("no_new_privs".to_string());
                } else {
                    return Err(unsupported(opt));
                }
            }
        }
        if req.host_config.privileged {
            cfg.seccomp_profile = SeccompProfile::Unconfined;
        }
        if req.host_config.restart_policy.is_object() {
            if let Some(name) = req
                .host_config
                .restart_policy
                .get("Name")
                .and_then(|v| v.as_str())
            {
                cfg.restart_policy = RestartPolicy::parse(name)?;
            }
        }
        if req.host_config.log_config.is_object() {
            if let Some(driver) = req
                .host_config
                .log_config
                .get("Type")
                .and_then(|v| v.as_str())
            {
                cfg.log_config.driver = driver.to_string();
            }
        }
        if req.host_config.resources.is_object() {
            if let Some(memory) = req
                .host_config
                .resources
                .get("Memory")
                .and_then(|v| v.as_u64())
            {
                cfg.resource_limits.memory_bytes = memory;
                cfg.max_bytes = memory;
            }
            if let Some(pids) = req
                .host_config
                .resources
                .get("PidsLimit")
                .and_then(|v| v.as_i64())
            {
                cfg.resource_limits.pids_limit = pids;
            }
        }
        cfg.annotations = req.host_config.annotations.clone();
        self.next = self.next.saturating_add(1);
        let id = hex_id(self.next);
        let container = Container {
            id: id.clone(),
            name: name.to_string(),
            image: req.image.clone(),
            config: cfg,
            labels: req.labels.clone(),
            state: ContainerState::Created,
            ports: Vec::new(),
            mounts: Vec::new(),
            created: "1970-01-01T00:00:00Z".to_string(),
            health: "starting".to_string(),
            restart_count: 0,
            exit_code: 0,
            cancel: std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false)),
        };
        let ports = container.config.published_ports.clone();
        let mounts = container.config.mounts.clone();
        self.containers.insert(
            id.clone(),
            Container {
                ports,
                mounts,
                ..container
            },
        );
        Ok(self.containers[&id].clone())
    }

    pub fn list_containers(&self, filters: &LabelMap, all: bool) -> Vec<Container> {
        self.containers
            .values()
            .filter(|c| all || c.state == ContainerState::Running)
            .filter(|c| labels_match(&c.labels, filters))
            .cloned()
            .collect()
    }

    pub fn inspect(&self, id: &str) -> Result<Container> {
        self.containers
            .get(id)
            .cloned()
            .ok_or_else(|| Error::NotFound(id.to_string()))
    }

    pub fn start(&mut self, id: &str) -> Result<()> {
        let c = self
            .containers
            .get_mut(id)
            .ok_or_else(|| Error::NotFound(id.to_string()))?;
        c.state = ContainerState::Running;
        c.health = "healthy".to_string();
        c.cancel.store(false, std::sync::atomic::Ordering::SeqCst);
        Ok(())
    }

    pub fn stop(&mut self, id: &str, signal: &str) -> Result<()> {
        let c = self
            .containers
            .get_mut(id)
            .ok_or_else(|| Error::NotFound(id.to_string()))?;
        let _ = signal;
        c.state = ContainerState::Exited;
        c.cancel.store(true, std::sync::atomic::Ordering::SeqCst);
        Ok(())
    }

    pub fn remove(&mut self, id: &str, force: bool) -> Result<()> {
        let c = self
            .containers
            .get(id)
            .ok_or_else(|| Error::NotFound(id.to_string()))?;
        if c.state == ContainerState::Running && !force {
            return Err(Error::Config("container is running".into()));
        }
        c.cancel.store(true, std::sync::atomic::Ordering::SeqCst);
        self.containers.remove(id);
        Ok(())
    }

    pub fn pull_image(&mut self, reference: &str) -> Result<ImageEntry> {
        let r = crate::oci::ImageRef::parse(reference)?;
        self.pulls = self.pulls.saturating_add(1);
        let id = hex_id(self.pulls.wrapping_mul(7919));
        let entry = ImageEntry {
            id: id.clone(),
            reference: r.reference(),
            labels: LabelMap::new(),
            size: 0,
        };
        self.images.insert(id, entry.clone());
        Ok(entry)
    }

    pub fn create_network(
        &mut self,
        name: &str,
        driver: &str,
        internal: bool,
    ) -> Result<NetworkEntry> {
        self.next = self.next.saturating_add(1);
        let entry = NetworkEntry {
            id: hex_id(self.next),
            name: name.to_string(),
            driver: driver.to_string(),
            internal,
            aliases: Vec::new(),
        };
        self.networks.insert(entry.id.clone(), entry.clone());
        Ok(entry)
    }

    pub fn create_volume(&mut self, name: &str, driver: &str) -> Result<VolumeEntry> {
        let entry = VolumeEntry {
            name: name.to_string(),
            driver: if driver.is_empty() {
                "local".to_string()
            } else {
                driver.to_string()
            },
            source: None,
            containers: Vec::new(),
        };
        self.volumes.insert(name.to_string(), entry.clone());
        Ok(entry)
    }
}

#[derive(Debug)]
pub struct Daemon {
    pub state: Mutex<State>,
    pub token: Option<String>,
}

impl Daemon {
    pub fn new(token: Option<String>) -> Self {
        Daemon {
            state: Mutex::new(State::new()),
            token,
        }
    }

    pub fn routes() -> &'static [&'static str] {
        &[
            "GET /_ping",
            "GET /version",
            "GET /info",
            "GET /containers/json",
            "POST /containers/create",
            "GET /containers/{id}/json",
            "POST /containers/{id}/start",
            "POST /containers/{id}/stop",
            "POST /containers/{id}/restart",
            "POST /containers/{id}/kill",
            "POST /containers/{id}/pause",
            "POST /containers/{id}/unpause",
            "POST /containers/{id}/wait",
            "POST /containers/{id}/resize",
            "GET /containers/{id}/logs",
            "GET /containers/{id}/top",
            "GET /containers/{id}/stats",
            "GET /containers/{id}/changes",
            "POST /containers/{id}/rename",
            "POST /containers/{id}/update",
            "POST /containers/{id}/exec",
            "DELETE /containers/{id}",
            "GET /images/json",
            "POST /images/create",
            "GET /images/{name}/json",
            "POST /images/{name}/tag",
            "POST /images/{name}/push",
            "POST /images/load",
            "DELETE /images/{name}",
            "POST /images/prune",
            "GET /networks",
            "POST /networks/create",
            "GET /networks/{id}",
            "DELETE /networks/{id}",
            "POST /networks/{id}/connect",
            "POST /networks/{id}/disconnect",
            "GET /volumes",
            "POST /volumes/create",
            "GET /volumes/{name}",
            "DELETE /volumes/{name}",
            "POST /volumes/prune",
            "POST /exec/{id}/start",
            "GET /exec/{id}/json",
            "POST /exec/{id}/resize",
            "POST /build",
            "GET /events",
            "GET /system/df",
            "POST /auth",
            "GET /laboon/v1/invariants",
            "GET /laboon/v1/routes",
        ]
    }

    fn authorized(&self, auth: Option<&str>) -> bool {
        match &self.token {
            None => true,
            Some(expected) => auth
                .map(|given| given.trim() == format!("Bearer {expected}"))
                .unwrap_or(false),
        }
    }

    pub fn handle(
        &self,
        method: &str,
        path: &str,
        query: &str,
        body: &[u8],
        auth: Option<&str>,
    ) -> Response {
        if !self.authorized(auth) {
            return Response::json(401, &serde_json::json!({"message":"unauthorized"}));
        }
        let state = match self.state.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        };
        let mut state = state;
        let path = path.trim_end_matches('/');
        if path == "/_ping" {
            return Response::text(200, "OK");
        }
        if path == "/version" {
            return Response::json(
                200,
                &serde_json::json!({
                    "ApiVersion":"1.43","MinAPIVersion":"1.24",
                    "Version":env!("CARGO_PKG_VERSION"),"Os":"linux",
                    "Arch":Arch::host().as_str(),"KernelVersion":"laboon"
                }),
            );
        }
        if path == "/info" {
            return Response::json(
                200,
                &serde_json::json!({
                    "Images":state.images.len(),"Containers":state.containers.len(),
                    "ContainersRunning":state.containers.values().filter(|c| c.state==ContainerState::Running).count(),
                    "Driver":"laboon-vfs","Runtimes":{"laboon":{"path":"laboon-runtime"}},
                    "ServerVersion":env!("CARGO_PKG_VERSION")
                }),
            );
        }
        if path == "/laboon/v1/invariants" {
            let items: Vec<&str> = crate::security::audit();
            return Response::json(200, &serde_json::json!({"invariants":items}));
        }
        if path == "/laboon/v1/routes" {
            return Response::json(200, &serde_json::json!({"routes":Self::routes()}));
        }
        if path == "/containers/json" {
            let filters = parse_label_filter(query);
            let list = state.list_containers(&filters, query.contains("all=1"));
            let value: Vec<serde_json::Value> = list
                .iter()
                .map(|c| {
                    serde_json::json!({
                        "Id":c.id,"Names":[format!("/{}",c.name)],"Image":c.image,
                        "State":format!("{:?}",c.state).to_ascii_lowercase(),
                        "Status":format!("{:?}",c.state).to_ascii_lowercase(),
                        "Labels":c.labels,"Ports":c.ports.iter().map(|p| serde_json::json!({
                            "IP":p.host_ip.to_string(),"PrivatePort":p.container_port,
                            "PublicPort":p.host_port,"Type":p.protocol.as_str()
                        })).collect::<Vec<_>>(),
                        "Mounts":c.mounts.iter().map(|m| serde_json::json!({
                            "Source":m.host,"Destination":m.guest,"RW":!m.read_only
                        })).collect::<Vec<_>>()
                    })
                })
                .collect();
            return Response::json(200, &serde_json::Value::Array(value));
        }
        if path == "/containers/create" && method == "POST" {
            let req: CreateRequest = serde_json::from_slice(body).unwrap_or_default();
            let name = query_param(query, "name").unwrap_or_else(|| format!("c{}", state.next + 1));
            return match state.create_container(&name, &req) {
                Ok(c) => Response::json(201, &serde_json::json!({"Id":c.id,"Warnings":[]})),
                Err(e) => Response::json(400, &serde_json::json!({"message":e.to_string()})),
            };
        }
        if let Some(id) = path.strip_prefix("/containers/") {
            let id = id.split('/').next().unwrap_or("");
            let action = path.rsplit('/').next().unwrap_or("");
            if action == "json" && method == "GET" {
                return match state.inspect(id) {
                    Ok(c) => Response::json(
                        200,
                        &serde_json::json!({
                            "Id":c.id,"Name":format!("/{}",c.name),"Image":c.image,
                            "Config":{"Cmd":c.config.cmd,"Entrypoint":c.config.entrypoint,
                                "Env":c.config.env,"Labels":c.labels,"Image":c.image,
                                "Tty":c.config.tty,"ExposedPorts":c.config.exposed_ports,
                                "Volumes":c.config.volumes,"StopSignal":c.config.stop_signal},
                            "State":{"Status":format!("{:?}",c.state).to_ascii_lowercase(),
                                "Running":c.state==ContainerState::Running,"ExitCode":c.exit_code,
                                "Health":{"Status":c.health}},
                            "HostConfig":{"NetworkMode":format!("{:?}",c.config.network_mode).to_ascii_lowercase(),
                                "PortBindings":c.ports,"Privileged":c.config.privileged,
                                "ReadonlyRootfs":c.config.readonly_rootfs},
                            "NetworkSettings":{"Ports":c.ports,"Networks":state.networks},
                            "Mounts":c.mounts,"Labels":c.labels,"RestartCount":c.restart_count
                        }),
                    ),
                    Err(e) => Response::json(404, &serde_json::json!({"message":e.to_string()})),
                };
            }
            if method == "DELETE" && action == id {
                return match state.remove(id, query.contains("force=1")) {
                    Ok(()) => Response::empty(204),
                    Err(e) => Response::json(404, &serde_json::json!({"message":e.to_string()})),
                };
            }
            if method == "POST" {
                let result = match action {
                    "start" => state.start(id),
                    "stop" | "kill" => state.stop(id, "SIGTERM"),
                    "restart" | "unpause" => state.start(id),
                    "pause" => {
                        if let Some(c) = state.containers.get_mut(id) {
                            c.state = ContainerState::Paused;
                        }
                        Ok(())
                    }
                    "wait" => Ok(()),
                    "resize" | "update" | "rename" | "exec" => Ok(()),
                    _ => Err(Error::NotFound(path.to_string())),
                };
                return match result {
                    Ok(()) if action == "wait" => {
                        Response::json(200, &serde_json::json!({"StatusCode":0}))
                    }
                    Ok(()) => Response::empty(204),
                    Err(e) => Response::json(404, &serde_json::json!({"message":e.to_string()})),
                };
            }
            if method == "GET" {
                let value = match action {
                    "logs" => serde_json::json!({"logs":[]}),
                    "top" => serde_json::json!({"Titles":["PID","USER","COMMAND"],"Processes":[]}),
                    "stats" => serde_json::json!({"cpu_stats":{},"memory_stats":{}}),
                    "changes" => serde_json::Value::Array(Vec::new()),
                    _ => serde_json::json!({}),
                };
                return Response::json(200, &value);
            }
        }
        if path == "/images/json" {
            let list: Vec<serde_json::Value> = state
                .images
                .values()
                .map(|i| serde_json::json!({"Id":i.id,"RepoTags":[i.reference],"Labels":i.labels,"Size":i.size}))
                .collect();
            return Response::json(200, &serde_json::Value::Array(list));
        }
        if path == "/images/create" && method == "POST" {
            let reference =
                query_param(query, "fromImage").unwrap_or_else(|| "alpine:latest".into());
            return match state.pull_image(&reference) {
                Ok(i) => Response::json(
                    200,
                    &serde_json::json!({"status":format!("pulled {}",i.reference),"id":i.id}),
                ),
                Err(e) => Response::json(400, &serde_json::json!({"message":e.to_string()})),
            };
        }
        if path == "/images/load" && method == "POST" {
            return Response::json(200, &serde_json::json!({"stream":"loaded\n"}));
        }
        if path.starts_with("/images/") && method == "GET" {
            return Response::json(200, &serde_json::json!({"Id":"sha256:local","RepoTags":[]}));
        }
        if path == "/networks" {
            let list: Vec<serde_json::Value> = state
                .networks
                .values()
                .map(|n| serde_json::json!({"Name":n.name,"Id":n.id,"Driver":n.driver,"Internal":n.internal}))
                .collect();
            return Response::json(200, &serde_json::Value::Array(list));
        }
        if path == "/networks/create" && method == "POST" {
            let value: serde_json::Value = serde_json::from_slice(body).unwrap_or_default();
            let name = value
                .get("Name")
                .and_then(|v| v.as_str())
                .unwrap_or("bridge");
            let driver = value
                .get("Driver")
                .and_then(|v| v.as_str())
                .unwrap_or("bridge");
            let internal = value
                .get("Internal")
                .and_then(|v| v.as_bool())
                .unwrap_or(false);
            return match state.create_network(name, driver, internal) {
                Ok(n) => Response::json(201, &serde_json::json!({"Id":n.id,"Warning":""})),
                Err(e) => Response::json(400, &serde_json::json!({"message":e.to_string()})),
            };
        }
        if path == "/volumes" {
            let list: Vec<serde_json::Value> = state
                .volumes
                .values()
                .map(|v| serde_json::json!({"Name":v.name,"Driver":v.driver}))
                .collect();
            return Response::json(200, &serde_json::json!({"Volumes":list,"Warnings":[]}));
        }
        if path == "/volumes/create" && method == "POST" {
            let value: serde_json::Value = serde_json::from_slice(body).unwrap_or_default();
            let name = value
                .get("Name")
                .and_then(|v| v.as_str())
                .unwrap_or("volume");
            let driver = value
                .get("Driver")
                .and_then(|v| v.as_str())
                .unwrap_or("local");
            return match state.create_volume(name, driver) {
                Ok(v) => Response::json(201, &serde_json::json!({"Name":v.name,"Driver":v.driver})),
                Err(e) => Response::json(400, &serde_json::json!({"message":e.to_string()})),
            };
        }
        if path.starts_with("/exec/") {
            return Response::json(
                200,
                &serde_json::json!({"Id":"exec","Running":false,"ExitCode":0}),
            );
        }
        if path == "/events" {
            return Response::json(200, &serde_json::json!({"events":[]}));
        }
        if path == "/system/df" {
            return Response::json(
                200,
                &serde_json::json!({"ImagesSize":0,"ContainersSize":0,"VolumesSize":0}),
            );
        }
        if path == "/auth" {
            return Response::json(200, &serde_json::json!({"Status":"ok"}));
        }
        Response::json(404, &serde_json::json!({"message":"not found"}))
    }

    pub fn serve(&self, addr: &str) -> Result<()> {
        let server = tiny_http::Server::http(addr)
            .map_err(|e| Error::Message(format!("cannot listen on {addr}: {e}")))?;
        for mut request in server.incoming_requests() {
            let method = request.method().as_str().to_string();
            let url = request.url().to_string();
            let mut body = Vec::new();
            let _ = request.as_reader().read_to_end(&mut body);
            let auth = request
                .headers()
                .iter()
                .find(|h| {
                    h.field
                        .as_str()
                        .as_str()
                        .eq_ignore_ascii_case("authorization")
                })
                .map(|h| h.value.as_str().to_string());
            let (path, query) = match url.split_once('?') {
                Some((p, q)) => (p.to_string(), q.to_string()),
                None => (url, String::new()),
            };
            let response = self.handle(&method, &path, &query, &body, auth.as_deref());
            let mut built = tiny_http::Response::from_data(response.body)
                .with_status_code(tiny_http::StatusCode(response.status));
            if let Ok(header) = tiny_http::Header::from_bytes(
                &b"Content-Type"[..],
                response.content_type.as_bytes(),
            ) {
                built = built.with_header(header);
            }
            let _ = request.respond(built);
        }
        Ok(())
    }
}

fn query_param(query: &str, key: &str) -> Option<String> {
    query.split('&').find_map(|pair| {
        let (k, v) = pair.split_once('=')?;
        if k == key {
            Some(percent_decode(v))
        } else {
            None
        }
    })
}

fn percent_decode(s: &str) -> String {
    let mut out = Vec::new();
    let bytes = s.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' && i + 2 < bytes.len() {
            if let (Some(a), Some(b)) = (hex_value(bytes[i + 1]), hex_value(bytes[i + 2])) {
                out.push(a * 16 + b);
                i += 3;
                continue;
            }
        }
        out.push(bytes[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

fn hex_value(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}

fn parse_label_filter(query: &str) -> LabelMap {
    let mut out = LabelMap::new();
    if let Some(raw) = query_param(query, "filters") {
        if let Ok(value) = serde_json::from_str::<serde_json::Value>(&raw) {
            if let Some(labels) = value.get("label").and_then(|v| v.as_array()) {
                for entry in labels {
                    if let Some(text) = entry.as_str() {
                        let (k, v) = match text.split_once('=') {
                            Some((k, v)) => (k.to_string(), v.to_string()),
                            None => (text.to_string(), String::new()),
                        };
                        out.insert(k, v);
                    }
                }
            }
        }
    }
    out
}
