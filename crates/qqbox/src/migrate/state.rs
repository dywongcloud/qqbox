//! Live-migration state algebra.
//!
//! `MigrationState` is an ADT of four **disjoint** partitions of the state the
//! Rust supervisor actually owns. `capture` and `apply` are pure morphisms.
//! `capture` starts from a live `Config`/`GuestState`/`Namespace`, so it cannot
//! know a state's session identity (generation, chanPage geometry, boundary);
//! `recapture` restores exactly those fields from the source state, and the
//! witnessed law is `recapture(apply(s), s) == normalize(s)`. Neither reads
//! ambient state. There is no guest memory, register file or vCPU state here:
//! qqbox has no VMM (see `migration-guest-memory` and docs/LIVE_MIGRATION.md).
//!
//! The `Config` destructure in `capture` is intentionally exhaustive (no `..`).
//! Adding a field to `Config` is a compile error until it is assigned to
//! exactly one partition, which enforces `forall t: Gamma_t superset Gamma_t-1`.

use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

use crate::config::Config;
use crate::sandbox::GuestState;
use crate::types::{
    Device, Healthcheck, LabelMap, LogConfig, Mount, NetworkMode, PortSpec, PublishedPort,
    ResourceLimits, RestartPolicy, SeccompProfile, Stdio, UserId, VolumeSpec,
};
use crate::vfs::{Namespace, NamespaceSnapshot};

pub const STATE_VERSION: u8 = 1;

/// Declarative workload spec: identity, process, security and resources.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct Spec {
    pub args: Vec<String>,
    pub env: Vec<String>,
    pub stdin: Stdio,
    pub stdout: Stdio,
    pub stderr: Stdio,
    pub dir: String,
    pub user: Option<UserId>,
    pub no_rewrite: bool,
    pub reject_rwx: bool,
    pub max_bytes: u64,
    pub labels: LabelMap,
    pub annotations: LabelMap,
    pub hostname: String,
    pub domainname: String,
    pub stop_signal: String,
    pub stop_timeout: u32,
    pub capabilities: Vec<String>,
    pub seccomp_profile: SeccompProfile,
    pub apparmor_profile: Option<String>,
    pub resource_limits: ResourceLimits,
    pub healthcheck: Option<Healthcheck>,
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

/// The vfs Namespace: declarative mounts plus the resolved host path table.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct FsState {
    pub root: Option<String>,
    pub upper: Option<String>,
    pub mounts: Vec<Mount>,
    pub devices: Vec<Device>,
    pub volumes: Vec<VolumeSpec>,
    pub resolved: NamespaceSnapshot,
}

/// The chanPage channel and the guest fd table owned by the supervisor.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ChanState {
    pub pid: u32,
    pub brk: u64,
    pub mapped_bytes: u64,
    pub fd_table: BTreeMap<u64, String>,
    pub chan_size: u64,
    pub chan_align: u64,
    pub slot_size: u64,
    pub max_slots: u64,
    pub stub_window_base: u64,
}

impl Default for ChanState {
    fn default() -> Self {
        ChanState {
            pid: 0,
            brk: 0,
            mapped_bytes: 0,
            fd_table: BTreeMap::new(),
            chan_size: crate::zig::CHAN_SIZE as u64,
            chan_align: crate::zig::CHAN_ALIGN as u64,
            slot_size: crate::zig::SLOT_SIZE as u64,
            max_slots: crate::zig::MAX_SLOTS as u64,
            stub_window_base: crate::zig::STUB_BASE,
        }
    }
}

/// A single expanded host/container port pair derived from `published_ports`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
pub struct ResolvedBinding {
    pub host_port: u16,
    pub container_port: u16,
}

/// Egress policy and port bindings. `resolved_bindings` is derived runtime
/// state; `binding_rejections` records specs that failed expansion so no
/// input is silently dropped.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct NetState {
    pub net: bool,
    pub no_net: bool,
    pub allow_domains: Vec<String>,
    pub allow_cidrs: Vec<String>,
    pub allow_ports: Vec<u16>,
    pub exposed_ports: Vec<PortSpec>,
    pub published_ports: Vec<PublishedPort>,
    pub network_mode: NetworkMode,
    pub dns: Vec<String>,
    pub dns_search: Vec<String>,
    pub extra_hosts: LabelMap,
    pub resolved_bindings: Vec<ResolvedBinding>,
    pub binding_rejections: Vec<String>,
}

/// Explicit, versioned boundary. `guest_execution` records what this runtime
/// cannot capture rather than pretending the state exists.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Boundary {
    pub guest_execution: GuestExecution,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum GuestExecution {
    /// Guest physical memory, vCPU registers and dirty-page bitmaps require a
    /// VMM. qqbox rewrites syscalls in a host process; there is no VMM to
    /// suspend and no guest-RAM API, even though the Linux trap-to-supervisor
    /// backend is ported. This is a structural boundary, not a TODO.
    NotCapturable,
}

impl Default for Boundary {
    fn default() -> Self {
        Boundary {
            guest_execution: GuestExecution::NotCapturable,
        }
    }
}

/// The complete migration state. Partitions are disjoint by construction:
/// `capture` destructures `Config` exhaustively and moves each field into
/// exactly one of `spec`, `fs`, `net`; `chan` holds state absent from `Config`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct MigrationState {
    pub version: u8,
    pub generation: u64,
    pub spec: Spec,
    pub fs: FsState,
    pub chan: ChanState,
    pub net: NetState,
    pub boundary: Boundary,
}

/// Total derivation of resolved bindings. Never panics, never drops an invalid
/// spec silently: it records a diagnostic instead.
fn derive_bindings(ports: &[PublishedPort]) -> (Vec<ResolvedBinding>, Vec<String>) {
    let mut bindings = Vec::new();
    let mut rejections = Vec::new();
    for port in ports {
        match crate::net::ports::expand(port) {
            Ok(pairs) => {
                for (host, container) in pairs {
                    bindings.push(ResolvedBinding {
                        host_port: host,
                        container_port: container,
                    });
                }
            }
            Err(e) => rejections.push(format!(
                "{}: {e}",
                crate::net::ports::render_published_port(port)
            )),
        }
    }
    bindings.sort_unstable();
    bindings.dedup();
    (bindings, rejections)
}

impl MigrationState {
    /// Pure capture. No IO, no ambient state, no hidden mutation.
    pub fn capture(cfg: &Config, guest: &GuestState, ns: &Namespace) -> MigrationState {
        let Config {
            args,
            env,
            stdin,
            stdout,
            stderr,
            dir,
            root,
            upper,
            user,
            mounts,
            devices,
            no_rewrite,
            reject_rwx,
            net,
            no_net,
            allow_domains,
            allow_cidrs,
            allow_ports,
            max_bytes,
            labels,
            annotations,
            hostname,
            domainname,
            stop_signal,
            stop_timeout,
            exposed_ports,
            published_ports,
            volumes,
            capabilities,
            seccomp_profile,
            apparmor_profile,
            resource_limits,
            healthcheck,
            network_mode,
            dns,
            dns_search,
            extra_hosts,
            log_config,
            restart_policy,
            tty,
            stdin_open,
            privileged,
            readonly_rootfs,
            entrypoint,
            cmd,
            working_dir,
            user_spec,
            image,
            container_name,
        } = cfg.clone();
        let (resolved_bindings, binding_rejections) = derive_bindings(&published_ports);

        MigrationState {
            version: STATE_VERSION,
            generation: 0,
            spec: Spec {
                args,
                env,
                stdin,
                stdout,
                stderr,
                dir,
                user,
                no_rewrite,
                reject_rwx,
                max_bytes,
                labels,
                annotations,
                hostname,
                domainname,
                stop_signal,
                stop_timeout,
                capabilities,
                seccomp_profile,
                apparmor_profile,
                resource_limits,
                healthcheck,
                log_config,
                restart_policy,
                tty,
                stdin_open,
                privileged,
                readonly_rootfs,
                entrypoint,
                cmd,
                working_dir,
                user_spec,
                image,
                container_name,
            },
            fs: FsState {
                root,
                upper,
                mounts,
                devices,
                volumes,
                resolved: ns.snapshot(),
            },
            chan: ChanState {
                pid: guest.pid,
                brk: guest.brk,
                mapped_bytes: guest.mapped_bytes,
                fd_table: guest.fd_table.clone(),
                ..ChanState::default()
            },
            net: NetState {
                net,
                no_net,
                allow_domains,
                allow_cidrs,
                allow_ports,
                exposed_ports,
                published_ports,
                network_mode,
                dns,
                dns_search,
                extra_hosts,
                resolved_bindings,
                binding_rejections,
            },
            boundary: Boundary::default(),
        }
    }

    /// Pure inverse of `capture`. Returns a typed error for an unsupported
    /// version or an unsafe namespace instead of panicking.
    pub fn apply(&self) -> Result<Applied, MigrationError> {
        if self.version != STATE_VERSION {
            return Err(MigrationError::Version {
                found: self.version,
                supported: STATE_VERSION,
            });
        }
        let Spec {
            args,
            env,
            stdin,
            stdout,
            stderr,
            dir,
            user,
            no_rewrite,
            reject_rwx,
            max_bytes,
            labels,
            annotations,
            hostname,
            domainname,
            stop_signal,
            stop_timeout,
            capabilities,
            seccomp_profile,
            apparmor_profile,
            resource_limits,
            healthcheck,
            log_config,
            restart_policy,
            tty,
            stdin_open,
            privileged,
            readonly_rootfs,
            entrypoint,
            cmd,
            working_dir,
            user_spec,
            image,
            container_name,
        } = self.spec.clone();
        let cfg = Config {
            args,
            env,
            stdin,
            stdout,
            stderr,
            dir,
            root: self.fs.root.clone(),
            upper: self.fs.upper.clone(),
            user,
            mounts: self.fs.mounts.clone(),
            devices: self.fs.devices.clone(),
            no_rewrite,
            reject_rwx,
            net: self.net.net,
            no_net: self.net.no_net,
            allow_domains: self.net.allow_domains.clone(),
            allow_cidrs: self.net.allow_cidrs.clone(),
            allow_ports: self.net.allow_ports.clone(),
            max_bytes,
            labels,
            annotations,
            hostname,
            domainname,
            stop_signal,
            stop_timeout,
            exposed_ports: self.net.exposed_ports.clone(),
            published_ports: self.net.published_ports.clone(),
            volumes: self.fs.volumes.clone(),
            capabilities,
            seccomp_profile,
            apparmor_profile,
            resource_limits,
            healthcheck,
            network_mode: self.net.network_mode,
            dns: self.net.dns.clone(),
            dns_search: self.net.dns_search.clone(),
            extra_hosts: self.net.extra_hosts.clone(),
            log_config,
            restart_policy,
            tty,
            stdin_open,
            privileged,
            readonly_rootfs,
            entrypoint,
            cmd,
            working_dir,
            user_spec,
            image,
            container_name,
        };
        let ns = Namespace::from_snapshot(&self.fs.resolved)
            .map_err(|e| MigrationError::Namespace(e.to_string()))?;
        let guest = GuestState {
            pid: self.chan.pid,
            brk: self.chan.brk,
            mapped_bytes: self.chan.mapped_bytes,
            fd_table: self.chan.fd_table.clone(),
        };
        Ok(Applied {
            config: cfg,
            guest,
            namespace: ns,
        })
    }

    /// Rebuild a state from `apply`'s output, restoring the session identity
    /// that a live `capture` cannot know. This is the right-hand side of the
    /// round-trip law; callers must not hand-patch individual fields.
    pub fn recapture(applied: &Applied, source: &MigrationState) -> MigrationState {
        let mut out = Self::capture(&applied.config, &applied.guest, &applied.namespace);
        out.generation = source.generation;
        out.chan = source.chan.clone();
        out.boundary = source.boundary.clone();
        out
    }

    /// Canonical form. Applying this twice is the same as applying it once:
    /// `normalize(normalize(s)) == normalize(s)` (f o f = f).
    pub fn normalize(&self) -> MigrationState {
        let mut out = self.clone();
        out.version = STATE_VERSION;
        let mut domains = out.net.allow_domains.clone();
        domains.sort();
        domains.dedup();
        out.net.allow_domains = domains;
        let mut cidrs = out.net.allow_cidrs.clone();
        cidrs.sort();
        cidrs.dedup();
        out.net.allow_cidrs = cidrs;
        let mut ports = out.net.allow_ports.clone();
        ports.sort_unstable();
        ports.dedup();
        out.net.allow_ports = ports;
        out.net.resolved_bindings.sort_unstable();
        out.net.resolved_bindings.dedup();
        out.net.binding_rejections.sort();
        out.fs.resolved.mounts.sort_by(|a, b| a.guest.cmp(&b.guest));
        out.boundary = Boundary::default();
        out
    }

    /// SHA-256 over the canonical encoding. Pure and deterministic.
    pub fn digest(&self) -> Result<[u8; 32], MigrationError> {
        use sha2::{Digest, Sha256};
        let bytes = serde_json::to_vec(&self.normalize())
            .map_err(|e| MigrationError::Encoding(e.to_string()))?;
        let out = Sha256::digest(&bytes);
        let mut d = [0u8; 32];
        d.copy_from_slice(&out);
        Ok(d)
    }

    /// Pre-copy delta: only the partitions that changed. Assignment-based, so
    /// `apply_delta(apply_delta(s, d), d) == apply_delta(s, d)`.
    pub fn delta(&self, newer: &MigrationState) -> Delta {
        Delta {
            generation: newer.generation,
            spec: (self.spec != newer.spec).then(|| newer.spec.clone()),
            fs: (self.fs != newer.fs).then(|| newer.fs.clone()),
            chan: (self.chan != newer.chan).then(|| newer.chan.clone()),
            net: (self.net != newer.net).then(|| newer.net.clone()),
        }
    }

    pub fn apply_delta(&self, d: &Delta) -> Result<MigrationState, MigrationError> {
        if d.generation < self.generation {
            return Err(MigrationError::Regression {
                current: self.generation,
                incoming: d.generation,
            });
        }
        let mut out = self.clone();
        if let Some(spec) = &d.spec {
            out.spec = spec.clone();
        }
        if let Some(fs) = &d.fs {
            out.fs = fs.clone();
        }
        if let Some(chan) = &d.chan {
            out.chan = chan.clone();
        }
        if let Some(net) = &d.net {
            out.net = net.clone();
        }
        out.generation = d.generation;
        Ok(out.normalize())
    }

    pub fn is_empty_delta(d: &Delta) -> bool {
        d.spec.is_none() && d.fs.is_none() && d.chan.is_none() && d.net.is_none()
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Delta {
    pub generation: u64,
    pub spec: Option<Spec>,
    pub fs: Option<FsState>,
    pub chan: Option<ChanState>,
    pub net: Option<NetState>,
}

/// The runtime tuple produced by `apply`. Kept as a value, not global state.
pub struct Applied {
    pub config: Config,
    pub guest: GuestState,
    pub namespace: Namespace,
}

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum MigrationError {
    #[error("unsupported migration state version {found}, this build supports {supported}")]
    Version { found: u8, supported: u8 },
    #[error("generation regression: current {current}, incoming {incoming}")]
    Regression { current: u64, incoming: u64 },
    #[error("invalid namespace snapshot: {0}")]
    Namespace(String),
    #[error("state encoding failed: {0}")]
    Encoding(String),
    #[error("migration capability rejected")]
    Capability,
    #[error("wrong migration phase: expected {expected}, found {found}")]
    Phase {
        expected: &'static str,
        found: String,
    },
    #[error("migration wire: {0}")]
    Wire(String),
}

impl From<MigrationError> for crate::error::Error {
    fn from(e: MigrationError) -> Self {
        crate::error::Error::Message(e.to_string())
    }
}
