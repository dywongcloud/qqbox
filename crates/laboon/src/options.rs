use serde::{Deserialize, Serialize};

use crate::config::Config;
use crate::error::{Error, Result};
use crate::types::{
    merge_labels, validate_label_key, validate_label_value, Device, Healthcheck, LabelMap, Mount,
    MountPropagation, NetworkMode, PortSpec, PublishedPort, RestartPolicy, Stdio, UserId,
    VolumeSpec,
};

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Options {
    pub args: Option<Vec<String>>,
    pub entrypoint: Option<Vec<String>>,
    pub env: Option<Vec<String>>,
    pub env_vars: Vec<String>,
    pub user: Option<String>,
    pub dir: Option<String>,
    pub root: Option<String>,
    pub mounts: Vec<Mount>,
    pub devices: Vec<Device>,
    pub stdin: Stdio,
    pub stdout: Stdio,
    pub stderr: Stdio,
    pub reject_rwx: bool,
    pub max_bytes: u64,
    pub net: bool,
    pub no_net: bool,
    pub allow_domains: Vec<String>,
    pub allow_cidrs: Vec<String>,
    pub allow_ports: Vec<u16>,
    pub platform: Option<String>,
    pub pull: bool,
    pub image_cache_dir: Option<String>,
    pub max_image_size: u64,
    pub no_rewrite: bool,
    pub labels: LabelMap,
    pub published_ports: Vec<PublishedPort>,
    pub volumes: Vec<VolumeSpec>,
    pub network_mode: NetworkMode,
    pub hostname: String,
    pub dns: Vec<String>,
    pub extra_hosts: LabelMap,
    pub capabilities: Vec<String>,
    pub privileged: bool,
    pub restart_policy: RestartPolicy,
    pub healthcheck: Option<Healthcheck>,
    pub publish_literals: Vec<String>,
    pub volume_literals: Vec<String>,
}

fn owned(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

fn has_parent_segment(path: &str) -> bool {
    path.split('/').any(|part| part == "..")
}

fn valid_volume_name(name: &str) -> bool {
    !name.is_empty()
        && !name.starts_with('.')
        && name
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-' || c == '.')
}

fn sanitize_volume_name(path: &str) -> String {
    let mut name = String::new();
    for c in path.chars() {
        if c.is_ascii_alphanumeric() {
            name.push(c);
        } else if !name.ends_with('_') {
            name.push('_');
        }
    }
    let trimmed = name.trim_matches('_').to_owned();
    if trimmed.is_empty() {
        String::from("volume")
    } else {
        trimmed
    }
}

fn validate_env_entry(kv: &str) -> Result<()> {
    if kv.is_empty() || kv.starts_with('=') || kv.chars().any(|c| c.is_control()) {
        return Err(Error::Config(format!("invalid environment entry: {}", kv)));
    }
    Ok(())
}

fn parse_id(text: &str) -> Result<Option<u32>> {
    if text.is_empty() || !text.bytes().all(|b| b.is_ascii_digit()) {
        return Ok(None);
    }
    match text.parse::<u32>() {
        Ok(id) => Ok(Some(id)),
        Err(_) => Err(Error::Config(format!(
            "user or group id out of range: {}",
            text
        ))),
    }
}

fn parse_user_spec(spec: &str) -> Result<Option<UserId>> {
    let trimmed = spec.trim();
    if trimmed.is_empty() {
        return Ok(None);
    }
    if trimmed.chars().any(|c| c.is_control() || c.is_whitespace()) {
        return Err(Error::Config(format!("invalid user spec: {}", trimmed)));
    }
    let (user_part, group_part) = match trimmed.split_once(':') {
        Some((user, group)) => (user, Some(group)),
        None => (trimmed, None),
    };
    if user_part.is_empty() {
        return Err(Error::Config(format!("invalid user spec: {}", trimmed)));
    }
    if let Some(group) = group_part {
        if group.is_empty() {
            return Err(Error::Config(format!("invalid user spec: {}", trimmed)));
        }
    }
    let uid = match parse_id(user_part)? {
        Some(id) => id,
        None => return Ok(None),
    };
    let gid = match group_part {
        None => uid,
        Some(group) => match parse_id(group)? {
            Some(id) => id,
            None => return Ok(None),
        },
    };
    if uid == 0 && gid == 0 {
        return Ok(None);
    }
    Ok(Some(UserId { uid, gid }))
}

fn parse_volume_literal(literal: &str) -> Result<VolumeSpec> {
    if literal.is_empty() || literal.chars().any(|c| c.is_control()) {
        return Err(Error::Config(format!("invalid volume: {}", literal)));
    }
    let parts: Vec<&str> = literal.split(':').collect();
    let (source, target, flags) = match parts.as_slice() {
        [only] => {
            if !only.starts_with('/') || *only == "/" || has_parent_segment(only) {
                return Err(Error::Config(format!(
                    "invalid anonymous volume: {}",
                    literal
                )));
            }
            return Ok(VolumeSpec {
                name: sanitize_volume_name(only),
                container_path: (*only).to_owned(),
                driver: String::from("local"),
                read_only: false,
                propagation: MountPropagation::Private,
                source: None,
            });
        }
        [src, dst] => (*src, *dst, ""),
        [src, dst, mode] => (*src, *dst, *mode),
        _ => return Err(Error::Config(format!("invalid volume: {}", literal))),
    };
    if target.is_empty() || !target.starts_with('/') || has_parent_segment(target) {
        return Err(Error::Config(format!(
            "invalid volume container path: {}",
            literal
        )));
    }
    let mut read_only = false;
    let mut propagation = MountPropagation::Private;
    if !flags.is_empty() {
        for flag in flags.split(',') {
            match flag {
                "" => return Err(Error::Config(format!("empty volume option in {}", literal))),
                "ro" | "readonly" => read_only = true,
                "rw" => read_only = false,
                "private" => propagation = MountPropagation::Private,
                "rprivate" => propagation = MountPropagation::RPrivate,
                "shared" => propagation = MountPropagation::Shared,
                "rshared" => propagation = MountPropagation::RShared,
                "slave" => propagation = MountPropagation::Slave,
                "rslave" => propagation = MountPropagation::RSlave,
                "z" | "Z" | "cached" | "delegated" | "nocopy" | "consistent" => {}
                other => {
                    return Err(Error::Config(format!(
                        "unknown volume option {} in {}",
                        other, literal
                    )))
                }
            }
        }
    }
    let (name, source_value) = if source.is_empty() {
        (sanitize_volume_name(target), None)
    } else if source.starts_with('/') || source.starts_with('.') || source.starts_with('~') {
        (sanitize_volume_name(source), Some(source.to_owned()))
    } else {
        (source.to_owned(), None)
    };
    if !valid_volume_name(&name) {
        return Err(Error::Config(format!("invalid volume name in {}", literal)));
    }
    Ok(VolumeSpec {
        name,
        container_path: target.to_owned(),
        driver: String::from("local"),
        read_only,
        propagation,
        source: source_value,
    })
}

fn validate_volume(volume: &VolumeSpec) -> Result<()> {
    if volume.name.is_empty()
        || volume
            .name
            .chars()
            .any(|c| c.is_control() || c.is_whitespace())
    {
        return Err(Error::Config(format!(
            "invalid volume name: {}",
            volume.name
        )));
    }
    if volume.container_path.is_empty()
        || !volume.container_path.starts_with('/')
        || has_parent_segment(&volume.container_path)
        || volume.container_path.chars().any(|c| c.is_control())
    {
        return Err(Error::Config(format!(
            "invalid volume container path: {}",
            volume.container_path
        )));
    }
    if let Some(source) = &volume.source {
        if source.is_empty() || source.chars().any(|c| c.is_control()) {
            return Err(Error::Config(format!("invalid volume source: {}", source)));
        }
    }
    Ok(())
}

fn validate_published_port(port: &PublishedPort) -> Result<()> {
    if port.container_port == 0 {
        return Err(Error::Config(
            "published port has no container port".to_owned(),
        ));
    }
    let container_last = match port.container_port_end {
        Some(end) => end,
        None => port.container_port,
    };
    let host_last = match port.host_port_end {
        Some(end) => end,
        None => port.host_port,
    };
    if container_last < port.container_port || host_last < port.host_port {
        return Err(Error::Config("published port range is reversed".to_owned()));
    }
    if port.host_port == 0 && port.host_port_end.is_some() {
        return Err(Error::Config(
            "ephemeral host port cannot have a range".to_owned(),
        ));
    }
    if let (Some(host_end), Some(container_end)) = (port.host_port_end, port.container_port_end) {
        let host_span = u32::from(host_end) - u32::from(port.host_port);
        let container_span = u32::from(container_end) - u32::from(port.container_port);
        if host_span != container_span {
            return Err(Error::Config(
                "published port ranges have different lengths".to_owned(),
            ));
        }
    }
    Ok(())
}

impl Options {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn with_args(mut self, a: &[&str]) -> Self {
        self.args = Some(owned(a));
        self
    }

    pub fn with_entrypoint(mut self, entry: &[&str]) -> Self {
        self.entrypoint = Some(owned(entry));
        self
    }

    pub fn with_env(mut self, env: &[&str]) -> Self {
        self.env = Some(owned(env));
        self
    }

    pub fn with_env_var(mut self, key: &str, value: &str) -> Self {
        self.env_vars.push(format!("{}={}", key, value));
        self
    }

    pub fn with_user(mut self, user: &str) -> Self {
        self.user = Some(user.to_owned());
        self
    }

    pub fn with_dir(mut self, dir: &str) -> Self {
        self.dir = Some(dir.to_owned());
        self
    }

    pub fn with_root(mut self, root: &str) -> Self {
        self.root = Some(root.to_owned());
        self
    }

    pub fn with_mount(mut self, host: &str, guest: &str) -> Self {
        if guest.trim_end_matches('/').is_empty() || guest == "/" {
            self.root = Some(host.to_owned());
        } else {
            self.mounts.push(Mount {
                host: host.to_owned(),
                guest: guest.to_owned(),
                read_only: false,
            });
        }
        self
    }

    pub fn with_mount_read_only(mut self, host: &str, guest: &str) -> Self {
        self.mounts.push(Mount {
            host: host.to_owned(),
            guest: guest.to_owned(),
            read_only: true,
        });
        self
    }

    pub fn with_stdio(mut self, stdin: Stdio, stdout: Stdio, stderr: Stdio) -> Self {
        self.stdin = stdin;
        self.stdout = stdout;
        self.stderr = stderr;
        self
    }

    pub fn with_stdin(mut self, stdin: Stdio) -> Self {
        self.stdin = stdin;
        self
    }

    pub fn with_stdout(mut self, stdout: Stdio) -> Self {
        self.stdout = stdout;
        self
    }

    pub fn with_stderr(mut self, stderr: Stdio) -> Self {
        self.stderr = stderr;
        self
    }

    pub fn with_reject_rwx(mut self, reject: bool) -> Self {
        self.reject_rwx = reject;
        self
    }

    pub fn with_max_memory(mut self, bytes: u64) -> Self {
        self.max_bytes = bytes;
        self
    }

    pub fn with_net(mut self, net: bool) -> Self {
        self.net = net;
        self
    }

    pub fn with_no_net(mut self, no_net: bool) -> Self {
        self.no_net = no_net;
        self
    }

    pub fn with_allow_domains(mut self, domains: &[&str]) -> Self {
        self.allow_domains.extend(owned(domains));
        self
    }

    pub fn with_allow_cidrs(mut self, cidrs: &[&str]) -> Self {
        self.allow_cidrs.extend(owned(cidrs));
        self
    }

    pub fn with_allow_ports(mut self, ports: &[u16]) -> Self {
        self.allow_ports.extend_from_slice(ports);
        self
    }

    pub fn with_platform(mut self, platform: &str) -> Self {
        self.platform = Some(platform.to_owned());
        self
    }

    pub fn with_pull(mut self, pull: bool) -> Self {
        self.pull = pull;
        self
    }

    pub fn with_image_cache_dir(mut self, dir: &str) -> Self {
        self.image_cache_dir = Some(dir.to_owned());
        self
    }

    pub fn with_max_image_size(mut self, bytes: u64) -> Self {
        self.max_image_size = bytes;
        self
    }

    pub fn with_no_rewrite(mut self, no_rewrite: bool) -> Self {
        self.no_rewrite = no_rewrite;
        self
    }

    pub fn with_labels(mut self, labels: LabelMap) -> Self {
        self.labels.extend(labels);
        self
    }

    pub fn with_publish(mut self, literal: &str) -> Self {
        match crate::net::ports::parse_published_port(literal) {
            Ok(port) => self.published_ports.push(port),
            Err(_) => self.publish_literals.push(literal.to_owned()),
        }
        self
    }

    pub fn with_volume(mut self, source: &str, target: &str) -> Self {
        let literal = format!("{}:{}", source, target);
        match parse_volume_literal(&literal) {
            Ok(volume) => self.volumes.push(volume),
            Err(_) => self.volume_literals.push(literal),
        }
        self
    }

    pub fn with_network_mode(mut self, mode: NetworkMode) -> Self {
        self.network_mode = mode;
        self
    }

    pub fn with_hostname(mut self, hostname: &str) -> Self {
        self.hostname = hostname.to_owned();
        self
    }

    pub fn with_dns(mut self, dns: &[&str]) -> Self {
        self.dns.extend(owned(dns));
        self
    }

    pub fn with_extra_host(mut self, host: &str, address: &str) -> Self {
        self.extra_hosts.insert(host.to_owned(), address.to_owned());
        self
    }

    pub fn with_capability(mut self, capability: &str) -> Self {
        self.capabilities.push(capability.to_owned());
        self
    }

    pub fn with_privileged(mut self, privileged: bool) -> Self {
        self.privileged = privileged;
        self
    }

    pub fn with_restart(mut self, policy: RestartPolicy) -> Self {
        self.restart_policy = policy;
        self
    }

    pub fn with_healthcheck(mut self, check: Healthcheck) -> Self {
        self.healthcheck = Some(check);
        self
    }

    pub fn apply(&self, base: &Config) -> Result<Config> {
        let mut config = base.clone();

        if let Some(env) = &self.env {
            for kv in env {
                validate_env_entry(kv)?;
            }
            config.env = env.clone();
        }
        for kv in &self.env_vars {
            validate_env_entry(kv)?;
            config.set_env(kv);
        }

        if let Some(entrypoint) = &self.entrypoint {
            config.entrypoint = entrypoint.clone();
            config.cmd.clear();
        }
        if let Some(args) = &self.args {
            config.cmd = args.clone();
        }
        let mut composed = config.entrypoint.clone();
        composed.extend(config.cmd.iter().cloned());
        if !composed.is_empty() {
            config.args = composed;
        } else if let Some(args) = &self.args {
            if config.args.is_empty() {
                config.args = args.clone();
            } else {
                config.args.extend(args.iter().cloned());
            }
        }

        if let Some(spec) = &self.user {
            config.user_spec = spec.clone();
            config.user = parse_user_spec(spec)?;
        }

        if let Some(dir) = &self.dir {
            config.dir = dir.clone();
            config.working_dir = dir.clone();
        }
        if let Some(root) = &self.root {
            config.root = Some(root.clone());
        }
        config.mounts.extend(self.mounts.iter().cloned());
        config.devices.extend(self.devices.iter().cloned());

        if self.stdin != Stdio::Null || self.stdout != Stdio::Null || self.stderr != Stdio::Null {
            config.stdin = self.stdin;
            config.stdout = self.stdout;
            config.stderr = self.stderr;
        }

        config.reject_rwx |= self.reject_rwx;
        config.no_rewrite |= self.no_rewrite;
        config.privileged |= self.privileged;
        if self.max_bytes != 0 {
            config.max_bytes = self.max_bytes;
        }

        config.network_mode = self.network_mode;
        let no_net = self.no_net || self.network_mode == NetworkMode::None;
        let net = (self.net || self.network_mode == NetworkMode::Host) && !no_net;
        config.net = net;
        config.no_net = no_net;

        config
            .allow_domains
            .extend(self.allow_domains.iter().cloned());
        config.allow_cidrs.extend(self.allow_cidrs.iter().cloned());
        config.allow_ports.extend(self.allow_ports.iter().copied());
        if !self.hostname.is_empty() {
            config.hostname = self.hostname.clone();
        }
        for server in &self.dns {
            if server.chars().any(|c| c.is_control() || c.is_whitespace()) {
                return Err(Error::Config(format!("invalid dns server: {}", server)));
            }
            if !config.dns.contains(server) {
                config.dns.push(server.clone());
            }
        }
        for (host, address) in &self.extra_hosts {
            validate_label_key(host)?;
            if address.is_empty() || address.chars().any(|c| c.is_control() || c.is_whitespace()) {
                return Err(Error::Config(format!(
                    "invalid extra host address for {}: {}",
                    host, address
                )));
            }
            config.extra_hosts.insert(host.clone(), address.clone());
        }
        config
            .capabilities
            .extend(self.capabilities.iter().cloned());

        for (key, value) in &self.labels {
            validate_label_key(key)?;
            validate_label_value(value)?;
        }
        config.labels = merge_labels(&config.labels, &self.labels);

        for port in &self.published_ports {
            validate_published_port(port)?;
            config.published_ports.push(port.clone());
        }
        for literal in &self.publish_literals {
            let port = crate::net::ports::parse_published_port(literal).map_err(|err| {
                Error::Config(format!("invalid published port {}: {}", literal, err))
            })?;
            validate_published_port(&port)?;
            config.published_ports.push(port);
        }
        for volume in &self.volumes {
            validate_volume(volume)?;
            config.volumes.push(volume.clone());
        }
        for literal in &self.volume_literals {
            let volume = parse_volume_literal(literal)?;
            validate_volume(&volume)?;
            config.volumes.push(volume);
        }
        for port in &config.published_ports {
            let spec = PortSpec {
                port: port.container_port,
                range_end: port.container_port_end,
                protocol: port.protocol,
            };
            if !config.exposed_ports.contains(&spec) {
                config.exposed_ports.push(spec);
            }
        }

        if self.restart_policy != RestartPolicy::No {
            config.restart_policy = self.restart_policy.clone();
        }
        if let Some(check) = &self.healthcheck {
            config.healthcheck = Some(check.clone());
        }
        Ok(config)
    }
}
