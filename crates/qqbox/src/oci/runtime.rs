use crate::config::Config;
use crate::error::{Error, Result};
use crate::types::*;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

pub const SUPPORTED_OCI_VERSION: &str = "1.0.2";
pub const OCI_VERSION_MAX: &str = "1.2.0";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct RuntimeSpec {
    #[serde(rename = "ociVersion", default)]
    pub oci_version: String,
    #[serde(default)]
    pub process: Option<Process>,
    #[serde(default)]
    pub root: Root,
    #[serde(default)]
    pub hostname: String,
    #[serde(default)]
    pub domainname: String,
    #[serde(default)]
    pub mounts: Vec<MountSpec>,
    #[serde(default)]
    pub hooks: Hooks,
    #[serde(default)]
    pub annotations: LabelMap,
    #[serde(default)]
    pub linux: Option<Linux>,
    #[serde(default)]
    pub vm: Option<serde_json::Value>,
    #[serde(default)]
    pub solaris: Option<serde_json::Value>,
    #[serde(default)]
    pub windows: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Process {
    #[serde(default)]
    pub terminal: bool,
    #[serde(default)]
    pub console_size: Option<ConsoleSize>,
    #[serde(default)]
    pub user: User,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env: Vec<String>,
    #[serde(default)]
    pub cwd: String,
    #[serde(default)]
    pub capabilities: Option<Capabilities>,
    #[serde(default)]
    pub rlimits: Vec<Rlimit>,
    #[serde(rename = "noNewPrivileges", default)]
    pub no_new_privileges: bool,
    #[serde(rename = "apparmorProfile", default)]
    pub apparmor_profile: String,
    #[serde(rename = "selinuxLabel", default)]
    pub selinux_label: String,
    #[serde(rename = "oomScoreAdj", default)]
    pub oom_score_adj: Option<i32>,
    #[serde(default)]
    pub scheduler: Option<serde_json::Value>,
    #[serde(default)]
    pub io_priority: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct ConsoleSize {
    pub height: u16,
    pub width: u16,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct User {
    pub uid: u32,
    pub gid: u32,
    #[serde(default)]
    pub umask: Option<u32>,
    #[serde(rename = "additionalGids", default)]
    pub additional_gids: Vec<u32>,
    #[serde(default)]
    pub username: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Capabilities {
    #[serde(default)]
    pub bounding: Vec<String>,
    #[serde(default)]
    pub effective: Vec<String>,
    #[serde(default)]
    pub inheritable: Vec<String>,
    #[serde(default)]
    pub permitted: Vec<String>,
    #[serde(default)]
    pub ambient: Vec<String>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Rlimit {
    #[serde(rename = "type")]
    pub kind: String,
    pub soft: u64,
    pub hard: u64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Root {
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub readonly: bool,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct MountSpec {
    pub destination: String,
    #[serde(default, rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub source: String,
    #[serde(default)]
    pub options: Vec<String>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Hooks {
    #[serde(default)]
    pub prestart: Vec<Hook>,
    #[serde(default, rename = "createRuntime")]
    pub create_runtime: Vec<Hook>,
    #[serde(default, rename = "createContainer")]
    pub create_container: Vec<Hook>,
    #[serde(default, rename = "startContainer")]
    pub start_container: Vec<Hook>,
    #[serde(default)]
    pub poststart: Vec<Hook>,
    #[serde(default)]
    pub poststop: Vec<Hook>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Hook {
    pub path: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env: Vec<String>,
    #[serde(default)]
    pub timeout: Option<u64>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Linux {
    #[serde(default)]
    pub namespaces: Vec<Namespace>,
    #[serde(rename = "uidMappings", default)]
    pub uid_mappings: Vec<IdMapping>,
    #[serde(rename = "gidMappings", default)]
    pub gid_mappings: Vec<IdMapping>,
    #[serde(default)]
    pub devices: Vec<LinuxDevice>,
    #[serde(default, rename = "cgroupsPath")]
    pub cgroups_path: String,
    #[serde(default)]
    pub resources: Option<LinuxResources>,
    #[serde(default)]
    pub seccomp: Option<serde_json::Value>,
    #[serde(default)]
    pub sysctl: BTreeMap<String, String>,
    #[serde(default, rename = "rootfsPropagation")]
    pub rootfs_propagation: String,
    #[serde(default, rename = "maskedPaths")]
    pub masked_paths: Vec<String>,
    #[serde(default, rename = "readonlyPaths")]
    pub readonly_paths: Vec<String>,
    #[serde(default, rename = "mountLabel")]
    pub mount_label: String,
    #[serde(default)]
    pub personality: Option<serde_json::Value>,
    #[serde(default)]
    pub time_offsets: Option<serde_json::Value>,
    #[serde(default)]
    pub intel_rdt: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Namespace {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub path: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct IdMapping {
    #[serde(rename = "containerID")]
    pub container_id: u32,
    #[serde(rename = "hostID")]
    pub host_id: u32,
    pub size: u32,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct LinuxDevice {
    pub path: String,
    #[serde(rename = "type")]
    pub kind: String,
    pub major: i64,
    pub minor: i64,
    #[serde(default)]
    pub file_mode: Option<u32>,
    #[serde(default)]
    pub uid: Option<u32>,
    #[serde(default)]
    pub gid: Option<u32>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct LinuxResources {
    #[serde(default)]
    pub memory: Option<Memory>,
    #[serde(default)]
    pub cpu: Option<serde_json::Value>,
    #[serde(default)]
    pub pids: Option<Pids>,
    #[serde(default)]
    pub block_io: Option<serde_json::Value>,
    #[serde(default)]
    pub hugepage_limits: Vec<serde_json::Value>,
    #[serde(default)]
    pub network: Option<serde_json::Value>,
    #[serde(default)]
    pub rdma: BTreeMap<String, serde_json::Value>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Memory {
    #[serde(default)]
    pub limit: Option<i64>,
    #[serde(default)]
    pub reservation: Option<i64>,
    #[serde(default)]
    pub swap: Option<i64>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Pids {
    pub limit: i64,
}

pub fn parse(bytes: &[u8]) -> Result<RuntimeSpec> {
    if let Ok(spec) = serde_json::from_slice::<RuntimeSpec>(bytes) {
        return Ok(spec);
    }
    let spec: RuntimeSpec = serde_yaml::from_slice(bytes)
        .map_err(|e| Error::Config(format!("runtime spec parse: {e}")))?;
    Ok(spec)
}

pub fn validate(spec: &RuntimeSpec) -> Result<()> {
    if spec.oci_version.is_empty() {
        return Err(Error::Config("ociVersion is required".into()));
    }
    let supported = ["1.0.0", "1.0.1", "1.0.2", "1.1.0", "1.2.0"];
    if !supported.contains(&spec.oci_version.as_str()) {
        return Err(Error::UnsupportedPlatform(format!(
            "ociVersion {} is outside the supported range 1.0.0..{}",
            spec.oci_version, OCI_VERSION_MAX
        )));
    }
    if spec.root.path.is_empty() {
        return Err(Error::Config("root.path is required".into()));
    }
    if spec.vm.is_some() || spec.solaris.is_some() || spec.windows.is_some() {
        return Err(Error::Config(
            "vm/solaris/windows sections are not supported by this host".into(),
        ));
    }
    Ok(())
}

pub fn features() -> serde_json::Value {
    serde_json::json!({
        "ociVersionMin":"1.0.0","ociVersionMax":OCI_VERSION_MAX,
        "hooks":["prestart","createRuntime","createContainer","startContainer","poststart","poststop"],
        "mountOptions":["ro","rw","rbind","bind","nosuid","nodev","noexec","size","mode","private","rprivate","shared","rshared","slave","rslave","tmpcopyup"],
        "annotations":{},"linux":{"namespaces":["pid","network","mount","ipc","uts","user","cgroup"]},
        "supported":SUPPORTED_OCI_VERSION
    })
}

pub fn state(
    id: &str,
    status: &str,
    pid: u32,
    bundle: &str,
    annotations: &LabelMap,
) -> serde_json::Value {
    serde_json::json!({
        "ociVersion":SUPPORTED_OCI_VERSION,"id":id,"status":status,"pid":pid,
        "bundle":bundle,"annotations":annotations
    })
}

fn mount_options(options: &[String]) -> Result<(bool, MountPropagation)> {
    let mut read_only = false;
    let mut propagation = MountPropagation::Private;
    for option in options {
        match option.as_str() {
            "ro" => read_only = true,
            "rw" => read_only = false,
            "rbind" | "bind" | "nosuid" | "nodev" | "noexec" | "tmpcopyup" => {}
            value if value.starts_with("size=") || value.starts_with("mode=") => {}
            "private" => propagation = MountPropagation::Private,
            "rprivate" => propagation = MountPropagation::RPrivate,
            "shared" => propagation = MountPropagation::Shared,
            "rshared" => propagation = MountPropagation::RShared,
            "slave" => propagation = MountPropagation::Slave,
            "rslave" => propagation = MountPropagation::RSlave,
            other => {
                return Err(Error::Config(format!(
                    "unsupported mount option {other}; refusing to silently ignore it"
                )))
            }
        }
    }
    Ok((read_only, propagation))
}

pub fn apply(spec: &RuntimeSpec) -> Result<(Config, Vec<String>)> {
    validate(spec)?;
    let mut cfg = Config::default();
    let mut rejected = Vec::new();
    cfg.root = Some(spec.root.path.clone());
    cfg.readonly_rootfs = spec.root.readonly;
    cfg.hostname = spec.hostname.clone();
    cfg.domainname = spec.domainname.clone();
    cfg.annotations = spec.annotations.clone();
    if let Some(process) = &spec.process {
        cfg.args = process.args.clone();
        cfg.cmd = process.args.clone();
        cfg.env = if process.env.is_empty() {
            Config::default_env()
        } else {
            process.env.clone()
        };
        cfg.dir = if process.cwd.is_empty() {
            "/".to_string()
        } else {
            process.cwd.clone()
        };
        cfg.working_dir = cfg.dir.clone();
        cfg.user = Some(UserId {
            uid: process.user.uid,
            gid: process.user.gid,
        });
        cfg.user_spec = format!("{}:{}", process.user.uid, process.user.gid);
        cfg.tty = process.terminal;
        if process.no_new_privileges {
            cfg.capabilities.push("no_new_privs".to_string());
        }
        if !process.apparmor_profile.is_empty() {
            cfg.apparmor_profile = Some(process.apparmor_profile.clone());
        }
        if !process.selinux_label.is_empty() {
            rejected.push(format!("selinuxLabel={}", process.selinux_label));
        }
        if process.user.umask.is_some() {
            rejected.push("user.umask".to_string());
        }
        if !process.user.additional_gids.is_empty() {
            rejected.push("user.additionalGids".to_string());
        }
        if let Some(caps) = &process.capabilities {
            for cap in caps
                .bounding
                .iter()
                .chain(caps.effective.iter())
                .chain(caps.permitted.iter())
            {
                if !cfg.capabilities.contains(cap) {
                    cfg.capabilities.push(cap.clone());
                }
            }
        }
        for rlimit in &process.rlimits {
            if rlimit.kind == "RLIMIT_NOFILE" {
                cfg.resource_limits.pids_limit = rlimit.hard as i64;
            } else {
                rejected.push(format!("rlimits.{}", rlimit.kind));
            }
        }
        if process.scheduler.is_some() {
            rejected.push("process.scheduler".to_string());
        }
        if process.io_priority.is_some() {
            rejected.push("process.ioPriority".to_string());
        }
        if process.oom_score_adj.is_some() {
            rejected.push("process.oomScoreAdj".to_string());
        }
    }
    for mount in &spec.mounts {
        let (read_only, propagation) = mount_options(&mount.options)?;
        match mount.kind.as_str() {
            "bind" => {
                if mount.source.is_empty() {
                    rejected.push(format!("bind mount {} has no source", mount.destination));
                } else {
                    cfg.mounts.push(Mount {
                        host: mount.source.clone(),
                        guest: mount.destination.clone(),
                        read_only,
                    });
                }
            }
            "tmpfs" | "tmpcopyup" => cfg.volumes.push(VolumeSpec {
                name: "tmpfs".to_string(),
                container_path: mount.destination.clone(),
                driver: "tmpfs".to_string(),
                read_only,
                propagation,
                source: None,
            }),
            "proc" | "sysfs" | "devpts" | "mqueue" | "cgroup" | "cgroup2" => {
                cfg.volumes.push(VolumeSpec {
                    name: mount.kind.clone(),
                    container_path: mount.destination.clone(),
                    driver: mount.kind.clone(),
                    read_only: true,
                    propagation,
                    source: None,
                });
            }
            "overlay" => rejected.push("mount type overlay".to_string()),
            other => rejected.push(format!("mount type {other}")),
        }
    }
    if spec.hooks.prestart.is_empty()
        && spec.hooks.create_runtime.is_empty()
        && spec.hooks.create_container.is_empty()
        && spec.hooks.start_container.is_empty()
        && spec.hooks.poststart.is_empty()
        && spec.hooks.poststop.is_empty()
    {
        // no hooks to execute
    } else {
        rejected.push("hooks execution".to_string());
    }
    if let Some(linux) = &spec.linux {
        for ns in &linux.namespaces {
            if ns.kind == "network" {
                cfg.network_mode = if ns.path.is_empty() {
                    NetworkMode::Bridge
                } else {
                    NetworkMode::Container
                };
            }
        }
        if !linux.uid_mappings.is_empty() || !linux.gid_mappings.is_empty() {
            rejected.push("linux uid/gid mappings".to_string());
        }
        if !linux.devices.is_empty() {
            rejected.push("linux.devices".to_string());
        }
        if !linux.cgroups_path.is_empty() {
            rejected.push("linux.cgroupsPath".to_string());
        }
        if linux.seccomp.is_some() {
            // The Rust control plane can carry a typed profile but cannot
            // install an arbitrary BPF program; report it as rejected rather
            // than claiming an OCI seccomp policy was enforced.
            rejected.push("linux.seccomp".to_string());
        }
        if !linux.sysctl.is_empty() {
            rejected.push("linux.sysctl".to_string());
        }
        if !linux.masked_paths.is_empty() {
            rejected.push("linux.maskedPaths".to_string());
        }
        if !linux.readonly_paths.is_empty() {
            rejected.push("linux.readonlyPaths".to_string());
        }
        if linux.personality.is_some() {
            rejected.push("linux.personality".to_string());
        }
        if linux.time_offsets.is_some() {
            rejected.push("linux.timeOffsets".to_string());
        }
        if linux.intel_rdt.is_some() {
            rejected.push("linux.intelRdt".to_string());
        }
        if let Some(resources) = &linux.resources {
            if let Some(memory) = &resources.memory {
                if let Some(limit) = memory.limit {
                    if limit > 0 {
                        cfg.max_bytes = limit as u64;
                        cfg.resource_limits.memory_bytes = limit as u64;
                    }
                }
            }
            if let Some(pids) = &resources.pids {
                cfg.resource_limits.pids_limit = pids.limit;
            }
            if resources.cpu.is_some()
                || resources.block_io.is_some()
                || resources.network.is_some()
            {
                rejected.push("linux.resources cpu/blockIO/network".to_string());
            }
            if !resources.hugepage_limits.is_empty() || !resources.rdma.is_empty() {
                rejected.push("linux.resources hugepageLimits/rdma".to_string());
            }
        }
    }
    Ok((cfg, rejected))
}
