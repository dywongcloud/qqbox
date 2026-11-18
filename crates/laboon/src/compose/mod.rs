use crate::config::Config;
use crate::error::{Error, Result};
use crate::types::*;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct ComposeFile {
    #[serde(default)]
    pub name: Option<String>,
    #[serde(default)]
    pub version: Option<String>,
    #[serde(default)]
    pub services: BTreeMap<String, Service>,
    #[serde(default)]
    pub networks: BTreeMap<String, serde_json::Value>,
    #[serde(default)]
    pub volumes: BTreeMap<String, serde_json::Value>,
    #[serde(default)]
    pub configs: BTreeMap<String, serde_json::Value>,
    #[serde(default)]
    pub secrets: BTreeMap<String, serde_json::Value>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Service {
    #[serde(default)]
    pub image: String,
    #[serde(default)]
    pub build: serde_json::Value,
    #[serde(default)]
    pub command: serde_json::Value,
    #[serde(default)]
    pub entrypoint: serde_json::Value,
    #[serde(default)]
    pub ports: Vec<serde_json::Value>,
    #[serde(default)]
    pub expose: Vec<serde_json::Value>,
    #[serde(default)]
    pub volumes: Vec<serde_json::Value>,
    #[serde(default)]
    pub networks: serde_json::Value,
    #[serde(default)]
    pub depends_on: serde_json::Value,
    #[serde(default)]
    pub environment: serde_json::Value,
    #[serde(default)]
    pub env_file: serde_json::Value,
    #[serde(default)]
    pub labels: serde_json::Value,
    #[serde(default)]
    pub healthcheck: serde_json::Value,
    #[serde(default)]
    pub restart: String,
    #[serde(default)]
    pub deploy: serde_json::Value,
    #[serde(default)]
    pub cap_add: Vec<String>,
    #[serde(default)]
    pub cap_drop: Vec<String>,
    #[serde(default)]
    pub privileged: bool,
    #[serde(default)]
    pub read_only: bool,
    #[serde(default)]
    pub user: String,
    #[serde(default)]
    pub working_dir: String,
    #[serde(default)]
    pub hostname: String,
    #[serde(default)]
    pub domainname: String,
    #[serde(default)]
    pub mac_address: String,
    #[serde(default)]
    pub dns: serde_json::Value,
    #[serde(default)]
    pub dns_search: Vec<String>,
    #[serde(default)]
    pub extra_hosts: serde_json::Value,
    #[serde(default)]
    pub security_opt: Vec<String>,
    #[serde(default)]
    pub devices: Vec<String>,
    #[serde(default)]
    pub sysctls: serde_json::Value,
    #[serde(default)]
    pub ulimits: serde_json::Value,
    #[serde(default)]
    pub tmpfs: serde_json::Value,
    #[serde(default)]
    pub secrets: Vec<serde_json::Value>,
    #[serde(default)]
    pub configs: Vec<serde_json::Value>,
    #[serde(default)]
    pub stop_signal: String,
    #[serde(default)]
    pub stop_grace_period: String,
    #[serde(default)]
    pub stdin_open: bool,
    #[serde(default)]
    pub tty: bool,
    #[serde(default)]
    pub init: bool,
    #[serde(default)]
    pub platform: String,
    #[serde(default)]
    pub pull_policy: String,
    #[serde(default)]
    pub network_mode: String,
    #[serde(default)]
    pub shm_size: String,
    #[serde(default)]
    pub cgroup: String,
    #[serde(default)]
    pub ipc: String,
    #[serde(default)]
    pub pid: String,
    #[serde(default)]
    pub profiles: Vec<String>,
    #[serde(default)]
    pub extends: serde_json::Value,
    #[serde(default)]
    pub logging: serde_json::Value,
    #[serde(flatten)]
    pub extensions: BTreeMap<String, serde_json::Value>,
}

pub fn parse(bytes: &[u8]) -> Result<ComposeFile> {
    if let Ok(file) = serde_json::from_slice::<ComposeFile>(bytes) {
        return Ok(file);
    }
    let file: ComposeFile =
        serde_yaml::from_slice(bytes).map_err(|e| Error::Config(format!("compose parse: {e}")))?;
    Ok(file)
}

fn as_string_vec(v: &serde_json::Value) -> Vec<String> {
    match v {
        serde_json::Value::String(s) => s.split_whitespace().map(|x| x.to_string()).collect(),
        serde_json::Value::Array(items) => items
            .iter()
            .map(|x| match x {
                serde_json::Value::String(s) => s.clone(),
                other => other.to_string(),
            })
            .collect(),
        _ => Vec::new(),
    }
}

fn as_map(v: &serde_json::Value) -> LabelMap {
    let mut out = LabelMap::new();
    match v {
        serde_json::Value::Object(map) => {
            for (k, value) in map {
                let text = match value {
                    serde_json::Value::String(s) => s.clone(),
                    serde_json::Value::Null => String::new(),
                    other => other.to_string(),
                };
                out.insert(k.clone(), text);
            }
        }
        serde_json::Value::Array(items) => {
            for item in items {
                if let serde_json::Value::String(s) = item {
                    let (k, val) = match s.split_once('=') {
                        Some((k, val)) => (k.to_string(), val.to_string()),
                        None => (s.clone(), String::new()),
                    };
                    out.insert(k, val);
                }
            }
        }
        _ => {}
    }
    out
}

fn port_literal(v: &serde_json::Value) -> Option<String> {
    match v {
        serde_json::Value::String(s) => Some(s.clone()),
        serde_json::Value::Number(n) => Some(n.to_string()),
        serde_json::Value::Object(map) => {
            let target = map.get("target")?.as_u64()? as u16;
            let published = map.get("published").and_then(|p| p.as_str()).unwrap_or("0");
            let proto = map
                .get("protocol")
                .and_then(|p| p.as_str())
                .unwrap_or("tcp");
            let host_ip = map
                .get("host_ip")
                .and_then(|p| p.as_str())
                .unwrap_or("0.0.0.0");
            Some(format!("{host_ip}:{published}:{target}/{proto}"))
        }
        _ => None,
    }
}

fn volume_literal(v: &serde_json::Value) -> Option<(String, String, bool, String)> {
    match v {
        serde_json::Value::String(s) => {
            let mut parts = s.splitn(3, ':');
            let source = parts.next().unwrap_or("").to_string();
            let target = parts.next().unwrap_or("").to_string();
            let flags = parts.next().unwrap_or("").to_string();
            let read_only = flags.split(',').any(|f| f == "ro");
            Some((source, target, read_only, flags))
        }
        serde_json::Value::Object(map) => {
            let source = map
                .get("source")
                .and_then(|s| s.as_str())
                .unwrap_or("")
                .to_string();
            let target = map
                .get("target")
                .and_then(|s| s.as_str())
                .unwrap_or("")
                .to_string();
            let read_only = map
                .get("read_only")
                .and_then(|s| s.as_bool())
                .unwrap_or(false);
            Some((source, target, read_only, String::new()))
        }
        _ => None,
    }
}

fn duration_ns(s: &str) -> u64 {
    let s = s.trim();
    let (number, unit) = s
        .find(|c: char| !c.is_ascii_digit() && c != '.')
        .map(|i| (&s[..i], &s[i..]))
        .unwrap_or((s, "s"));
    let value = number.parse::<f64>().unwrap_or(0.0);
    let multiplier = match unit.trim() {
        "ns" | "n" => 1.0,
        "us" | "u" => 1_000.0,
        "ms" => 1_000_000.0,
        "s" => 1_000_000_000.0,
        "m" => 60_000_000_000.0,
        "h" => 3_600_000_000_000.0,
        _ => 1_000_000_000.0,
    };
    (value * multiplier) as u64
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Dependency {
    pub service: String,
    pub condition: String,
    pub required: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServicePlan {
    pub name: String,
    pub image: String,
    pub config: Config,
    pub depends_on: Vec<Dependency>,
    pub healthcheck: Option<Healthcheck>,
    pub restart: RestartPolicy,
    pub networks: Vec<String>,
    pub aliases: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NetworkPlan {
    pub name: String,
    pub driver: String,
    pub aliases: Vec<String>,
    pub internal: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VolumePlan {
    pub name: String,
    pub source: Option<String>,
    pub driver: String,
    pub containers: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct InstancePlan {
    pub project: String,
    pub services: Vec<ServicePlan>,
    pub networks: Vec<NetworkPlan>,
    pub volumes: Vec<VolumePlan>,
    pub order: Vec<String>,
}

fn dependencies(v: &serde_json::Value) -> Vec<Dependency> {
    let mut out = Vec::new();
    match v {
        serde_json::Value::Array(items) => {
            for item in items {
                if let serde_json::Value::String(s) = item {
                    out.push(Dependency {
                        service: s.clone(),
                        condition: "service_started".to_string(),
                        required: true,
                    });
                }
            }
        }
        serde_json::Value::Object(map) => {
            for (name, spec) in map {
                let condition = spec
                    .as_object()
                    .and_then(|o| o.get("condition"))
                    .and_then(|c| c.as_str())
                    .unwrap_or("service_started")
                    .to_string();
                let required = spec
                    .as_object()
                    .and_then(|o| o.get("required"))
                    .and_then(|r| r.as_bool())
                    .unwrap_or(true);
                out.push(Dependency {
                    service: name.clone(),
                    condition,
                    required,
                });
            }
        }
        _ => {}
    }
    out
}

fn network_names(v: &serde_json::Value) -> Vec<String> {
    match v {
        serde_json::Value::Array(items) => items
            .iter()
            .filter_map(|x| x.as_str().map(|s| s.to_string()))
            .collect(),
        serde_json::Value::Object(map) => map.keys().cloned().collect(),
        serde_json::Value::String(s) => vec![s.clone()],
        _ => Vec::new(),
    }
}

fn healthcheck(v: &serde_json::Value) -> Option<Healthcheck> {
    let map = v.as_object()?;
    let test = as_string_vec(map.get("test")?);
    if test.is_empty() {
        return None;
    }
    let get = |key: &str| {
        map.get(key)
            .and_then(|x| x.as_str())
            .map(duration_ns)
            .or_else(|| map.get(key).and_then(|x| x.as_u64()))
            .unwrap_or(0)
    };
    Some(Healthcheck {
        test,
        interval_ns: get("interval"),
        timeout_ns: get("timeout"),
        retries: map.get("retries").and_then(|x| x.as_u64()).unwrap_or(3) as u32,
        start_period_ns: get("start_period"),
    })
}

fn service_config(name: &str, s: &Service) -> Result<Config> {
    let mut cfg = Config {
        image: s.image.clone(),
        container_name: name.to_string(),
        ..Config::default()
    };
    cfg.entrypoint = as_string_vec(&s.entrypoint);
    cfg.cmd = as_string_vec(&s.command);
    cfg.env = as_map(&s.environment)
        .iter()
        .map(|(k, v)| format!("{k}={v}"))
        .collect();
    if cfg.env.is_empty() {
        cfg.env = Config::default_env();
    }
    cfg.labels = as_map(&s.labels);
    cfg.user_spec = s.user.clone();
    cfg.working_dir = s.working_dir.clone();
    cfg.dir = s.working_dir.clone();
    cfg.hostname = s.hostname.clone();
    cfg.domainname = s.domainname.clone();
    cfg.readonly_rootfs = s.read_only;
    cfg.privileged = s.privileged;
    cfg.stdin_open = s.stdin_open;
    cfg.tty = s.tty;
    cfg.capabilities = s.cap_add.clone();
    cfg.restart_policy = if s.restart.is_empty() {
        RestartPolicy::No
    } else {
        RestartPolicy::parse(&s.restart)?
    };
    cfg.healthcheck = healthcheck(&s.healthcheck);
    if !s.stop_signal.is_empty() {
        cfg.stop_signal = s.stop_signal.clone();
    }
    if !s.stop_grace_period.is_empty() {
        cfg.stop_timeout = (duration_ns(&s.stop_grace_period) / 1_000_000_000) as u32;
    }
    cfg.dns = as_string_vec(&s.dns);
    cfg.dns_search = s.dns_search.clone();
    cfg.extra_hosts = as_map(&s.extra_hosts);
    for p in &s.expose {
        if let Some(lit) = port_literal(p) {
            if let Ok(spec) = crate::net::ports::parse_port_spec(&lit) {
                cfg.exposed_ports.push(spec);
            }
        }
    }
    for p in &s.ports {
        if let Some(lit) = port_literal(p) {
            cfg.published_ports
                .push(crate::net::ports::parse_published_port(&lit)?);
        }
    }
    for v in &s.volumes {
        if let Some((source, target, read_only, flags)) = volume_literal(v) {
            let propagation = if flags.contains("rshared") {
                MountPropagation::RShared
            } else if flags.contains("shared") {
                MountPropagation::Shared
            } else if flags.contains("rslave") {
                MountPropagation::RSlave
            } else if flags.contains("slave") {
                MountPropagation::Slave
            } else if flags.contains("rprivate") {
                MountPropagation::RPrivate
            } else {
                MountPropagation::Private
            };
            cfg.volumes.push(VolumeSpec {
                name: source.clone(),
                container_path: target,
                driver: "local".to_string(),
                read_only,
                propagation,
                source: if source.starts_with('/') || source.starts_with('.') {
                    Some(source)
                } else {
                    None
                },
            });
        }
    }
    if s.network_mode == "none" {
        cfg.network_mode = NetworkMode::None;
    } else if s.network_mode == "host" {
        cfg.network_mode = NetworkMode::Host;
    } else if s.network_mode.starts_with("container:") {
        cfg.network_mode = NetworkMode::Container;
    }
    cfg.no_net = s.network_mode == "none";
    Ok(cfg)
}

pub fn validate(f: &ComposeFile) -> Result<()> {
    if f.services.is_empty() {
        return Err(Error::Config("compose file has no services".into()));
    }
    for (name, s) in &f.services {
        if s.image.is_empty() && s.build.is_null() {
            return Err(Error::Config(format!(
                "service {name} has neither image nor build"
            )));
        }
        for d in dependencies(&s.depends_on) {
            if !f.services.contains_key(&d.service) && d.required {
                return Err(Error::Config(format!(
                    "service {name} depends on unknown service {}",
                    d.service
                )));
            }
        }
        for p in &s.ports {
            if let Some(lit) = port_literal(p) {
                crate::net::ports::parse_published_port(&lit)
                    .map_err(|e| Error::Config(format!("service {name}: {e}")))?;
            }
        }
        for v in &s.volumes {
            if volume_literal(v).is_none() {
                return Err(Error::Config(format!(
                    "service {name} has an invalid volume"
                )));
            }
        }
    }
    Ok(())
}

fn order_services(f: &ComposeFile) -> Result<Vec<String>> {
    let mut remaining: Vec<String> = f.services.keys().cloned().collect();
    let mut done: Vec<String> = Vec::new();
    let mut guard = 0;
    while !remaining.is_empty() && guard <= f.services.len() + 1 {
        guard += 1;
        let mut progressed = false;
        let mut next = Vec::new();
        for name in &remaining {
            let deps: Vec<String> = dependencies(&f.services[name].depends_on)
                .into_iter()
                .filter(|d| d.required)
                .map(|d| d.service)
                .collect();
            if deps.iter().all(|d| done.contains(d)) {
                done.push(name.clone());
                progressed = true;
            } else {
                next.push(name.clone());
            }
        }
        remaining = next;
        if !progressed {
            return Err(Error::Config(format!(
                "compose dependency cycle among {remaining:?}"
            )));
        }
    }
    Ok(done)
}

pub fn plan(f: &ComposeFile, project: &str) -> Result<InstancePlan> {
    validate(f)?;
    let order = order_services(f)?;
    let project = f.name.clone().unwrap_or_else(|| project.to_string());
    let mut services = Vec::new();
    let mut networks = Vec::new();
    let mut volumes: BTreeMap<String, VolumePlan> = BTreeMap::new();
    for name in &order {
        let s = &f.services[name];
        let cfg = service_config(name, s)?;
        let nets = network_names(&s.networks);
        for n in &nets {
            if !networks.iter().any(|x: &NetworkPlan| x.name == *n) {
                let spec = f.networks.get(n);
                let driver = spec
                    .and_then(|v| v.as_object())
                    .and_then(|o| o.get("driver"))
                    .and_then(|d| d.as_str())
                    .unwrap_or("bridge")
                    .to_string();
                let internal = spec
                    .and_then(|v| v.as_object())
                    .and_then(|o| o.get("internal"))
                    .and_then(|d| d.as_bool())
                    .unwrap_or(false);
                networks.push(NetworkPlan {
                    name: format!("{project}_{n}"),
                    driver,
                    aliases: vec![name.clone()],
                    internal,
                });
            }
        }
        for v in &cfg.volumes {
            let key = if v.source.is_none() && !v.name.is_empty() {
                format!("{project}_{}", v.name)
            } else {
                v.name.clone()
            };
            volumes
                .entry(key.clone())
                .and_modify(|p| p.containers.push(name.clone()))
                .or_insert(VolumePlan {
                    name: key,
                    source: v.source.clone(),
                    driver: v.driver.clone(),
                    containers: vec![name.clone()],
                });
        }
        services.push(ServicePlan {
            name: name.clone(),
            image: s.image.clone(),
            config: cfg,
            depends_on: dependencies(&s.depends_on),
            healthcheck: healthcheck(&s.healthcheck),
            restart: if s.restart.is_empty() {
                RestartPolicy::No
            } else {
                RestartPolicy::parse(&s.restart)?
            },
            networks: nets,
            aliases: vec![name.clone()],
        });
    }
    Ok(InstancePlan {
        project,
        services,
        networks,
        volumes: volumes.into_values().collect(),
        order,
    })
}

pub fn to_json(p: &InstancePlan) -> Result<String> {
    Ok(serde_json::to_string_pretty(p)?)
}

pub fn service_label_count(labels: &serde_json::Value) -> usize {
    as_map(labels).len()
}
