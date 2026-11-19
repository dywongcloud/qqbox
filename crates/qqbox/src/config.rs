use serde::{Deserialize, Serialize};

use crate::types::{
    Device, Healthcheck, LabelMap, LogConfig, Mount, NetworkMode, PortSpec, PublishedPort,
    ResourceLimits, RestartPolicy, SeccompProfile, Stdio, UserId, VolumeSpec,
};

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Config {
    pub args: Vec<String>,
    pub env: Vec<String>,
    pub stdin: Stdio,
    pub stdout: Stdio,
    pub stderr: Stdio,
    pub dir: String,
    pub root: Option<String>,
    pub upper: Option<String>,
    pub user: Option<UserId>,
    pub mounts: Vec<Mount>,
    pub devices: Vec<Device>,
    pub no_rewrite: bool,
    pub reject_rwx: bool,
    pub net: bool,
    pub no_net: bool,
    pub allow_domains: Vec<String>,
    pub allow_cidrs: Vec<String>,
    pub allow_ports: Vec<u16>,
    pub max_bytes: u64,
    pub labels: LabelMap,
    pub annotations: LabelMap,
    pub hostname: String,
    pub domainname: String,
    pub stop_signal: String,
    pub stop_timeout: u32,
    pub exposed_ports: Vec<PortSpec>,
    pub published_ports: Vec<PublishedPort>,
    pub volumes: Vec<VolumeSpec>,
    pub capabilities: Vec<String>,
    pub seccomp_profile: SeccompProfile,
    pub apparmor_profile: Option<String>,
    pub resource_limits: ResourceLimits,
    pub healthcheck: Option<Healthcheck>,
    pub network_mode: NetworkMode,
    pub dns: Vec<String>,
    pub dns_search: Vec<String>,
    pub extra_hosts: LabelMap,
    pub log_config: LogConfig,
    pub restart_policy: RestartPolicy,
    pub tty: bool,
    pub stdin_open: bool,
    pub privileged: bool,
    pub readonly_rootfs: bool,
    pub entrypoint: Vec<String>,
    pub cmd: Vec<String>,
    pub working_dir: String,
    pub user_spec: String,
    pub image: String,
    pub container_name: String,
}

fn env_key(entry: &str) -> &str {
    match entry.split_once('=') {
        Some((key, _)) => key,
        None => entry,
    }
}

impl Config {
    pub fn default_env() -> Vec<String> {
        vec![
            String::from("PATH=/usr/local/bin:/usr/bin:/bin"),
            String::from("HOME=/"),
        ]
    }

    pub fn set_env(&mut self, kv: &str) {
        let key = env_key(kv);
        for entry in self.env.iter_mut() {
            if env_key(entry) == key {
                *entry = kv.to_owned();
                return;
            }
        }
        self.env.push(kv.to_owned());
    }

    pub fn get_env(&self, key: &str) -> Option<&str> {
        self.env
            .iter()
            .find(|entry| env_key(entry) == key)
            .map(|entry| match entry.split_once('=') {
                Some((_, value)) => value,
                None => "",
            })
    }
}
