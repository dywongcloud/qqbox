use std::collections::BTreeMap;
use std::net::IpAddr;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use serde::{Deserialize, Serialize};

use crate::error::{Error, Result};

pub type Id = String;
pub type LabelMap = BTreeMap<String, String>;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct UserId {
    pub uid: u32,
    pub gid: u32,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct Mount {
    pub host: String,
    pub guest: String,
    pub read_only: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum DeviceKind {
    Serial,
    Null,
    Zero,
    Random,
    Tty,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Device {
    pub path: String,
    pub kind: DeviceKind,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum Stdio {
    #[default]
    Null,
    Inherit,
    Pipe,
    Capture,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct WinSize {
    pub rows: u16,
    pub cols: u16,
    pub xpixel: u16,
    pub ypixel: u16,
}

pub trait Terminal: Send {
    fn read(&mut self, b: &mut [u8]) -> std::io::Result<usize>;
    fn write(&mut self, b: &[u8]) -> std::io::Result<usize>;
    fn winsize(&self) -> WinSize {
        WinSize::default()
    }
}

#[derive(Debug, Clone, Default)]
pub struct Context {
    pub cancelled: Arc<AtomicBool>,
    pub wake: Arc<(std::sync::Mutex<bool>, std::sync::Condvar)>,
}

impl Context {
    pub fn cancel(&self) {
        self.cancelled.store(true, Ordering::SeqCst);
        let (lock, changed) = &*self.wake;
        let mut guard = match lock.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        };
        *guard = true;
        changed.notify_all();
    }

    pub fn is_cancelled(&self) -> bool {
        self.cancelled.load(Ordering::SeqCst)
    }

    pub fn wait_cancelled(&self) {
        let (lock, changed) = &*self.wake;
        let mut guard = match lock.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        };
        while !*guard {
            guard = match changed.wait(guard) {
                Ok(next) => next,
                Err(poisoned) => poisoned.into_inner(),
            };
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Arch {
    Amd64,
    Arm64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum HostOs {
    Linux,
    Darwin,
    Windows,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum PortProtocol {
    Tcp,
    Udp,
    Sctp,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PortSpec {
    pub port: u16,
    pub range_end: Option<u16>,
    pub protocol: PortProtocol,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PublishedPort {
    pub host_ip: IpAddr,
    pub host_port: u16,
    pub host_port_end: Option<u16>,
    pub container_port: u16,
    pub container_port_end: Option<u16>,
    pub protocol: PortProtocol,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum NetworkMode {
    #[default]
    Bridge,
    Host,
    None,
    Container,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum MountPropagation {
    Private,
    RPrivate,
    Shared,
    RShared,
    Slave,
    RSlave,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct VolumeSpec {
    pub name: String,
    pub container_path: String,
    pub driver: String,
    pub read_only: bool,
    pub propagation: MountPropagation,
    pub source: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct Healthcheck {
    pub test: Vec<String>,
    pub interval_ns: u64,
    pub timeout_ns: u64,
    pub retries: u32,
    pub start_period_ns: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum SeccompProfile {
    #[default]
    Default,
    Unconfined,
    Path(String),
    Json(String),
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct ResourceLimits {
    pub memory_bytes: u64,
    pub cpu_shares: u64,
    pub cpu_quota: i64,
    pub cpu_period: u64,
    pub pids_limit: i64,
    pub blkio_weight: u16,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum RestartPolicy {
    #[default]
    No,
    Always,
    OnFailure,
    UnlessStopped,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct LogConfig {
    pub driver: String,
    pub options: LabelMap,
}

pub fn validate_label_key(k: &str) -> Result<()> {
    if k.is_empty() {
        return Err(Error::Config("label key is empty".to_owned()));
    }
    if k.starts_with('.') {
        return Err(Error::Config(format!("label key starts with a dot: {}", k)));
    }
    if k.chars().any(|c| c.is_control() || c.is_whitespace()) {
        return Err(Error::Config(format!(
            "label key has control characters or whitespace: {}",
            k
        )));
    }
    Ok(())
}

pub fn validate_label_value(v: &str) -> Result<()> {
    if v.chars().any(|c| c.is_control()) {
        return Err(Error::Config(format!(
            "label value has control characters: {}",
            v
        )));
    }
    Ok(())
}

pub fn merge_labels(image: &LabelMap, container: &LabelMap) -> LabelMap {
    let mut merged = image.clone();
    for (key, value) in container {
        merged.insert(key.clone(), value.clone());
    }
    merged
}

pub fn labels_match(labels: &LabelMap, filter: &LabelMap) -> bool {
    filter.iter().all(|(key, expected)| match labels.get(key) {
        None => false,
        Some(actual) => expected.is_empty() || actual == expected,
    })
}

impl Arch {
    pub fn as_str(&self) -> &'static str {
        match self {
            Arch::Amd64 => "amd64",
            Arch::Arm64 => "arm64",
        }
    }

    pub fn host() -> Arch {
        if cfg!(target_arch = "aarch64") {
            Arch::Arm64
        } else {
            Arch::Amd64
        }
    }

    pub fn parse(s: &str) -> Result<Arch> {
        match s.trim().to_ascii_lowercase().as_str() {
            "amd64" | "x86_64" | "x86-64" => Ok(Arch::Amd64),
            "arm64" | "aarch64" => Ok(Arch::Arm64),
            other => Err(Error::UnsupportedPlatform(other.to_string())),
        }
    }
}

impl HostOs {
    pub fn current() -> HostOs {
        if cfg!(target_os = "linux") {
            HostOs::Linux
        } else if cfg!(target_os = "macos") {
            HostOs::Darwin
        } else {
            HostOs::Windows
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            HostOs::Linux => "linux",
            HostOs::Darwin => "darwin",
            HostOs::Windows => "windows",
        }
    }
}

impl PortProtocol {
    pub fn as_str(&self) -> &'static str {
        match self {
            PortProtocol::Tcp => "tcp",
            PortProtocol::Udp => "udp",
            PortProtocol::Sctp => "sctp",
        }
    }

    pub fn parse(s: &str) -> Result<PortProtocol> {
        match s.trim().to_ascii_lowercase().as_str() {
            "" | "tcp" => Ok(PortProtocol::Tcp),
            "udp" => Ok(PortProtocol::Udp),
            "sctp" => Ok(PortProtocol::Sctp),
            other => Err(Error::Config(format!("unknown port protocol {other}"))),
        }
    }
}

impl Default for MountPropagation {
    fn default() -> Self {
        MountPropagation::Private
    }
}

impl RestartPolicy {
    pub fn parse(s: &str) -> Result<RestartPolicy> {
        match s.trim().to_ascii_lowercase().as_str() {
            "" | "no" | "none" => Ok(RestartPolicy::No),
            "always" => Ok(RestartPolicy::Always),
            "on-failure" | "on_failure" => Ok(RestartPolicy::OnFailure),
            "unless-stopped" | "unless_stopped" => Ok(RestartPolicy::UnlessStopped),
            other => Err(Error::Config(format!("unknown restart policy {other}"))),
        }
    }
}
