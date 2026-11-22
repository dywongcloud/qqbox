use std::net::SocketAddr;
use std::path::Component;

use crate::config::Config;
use crate::error::{Error, Result};
use crate::net::EgressPolicy;
use crate::vfs::Namespace;

const PROT_WRITE: u32 = 0x2;
const PROT_EXEC: u32 = 0x4;
const EACCES: i32 = 13;
const ENOMEM: i32 = 12;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Invariant {
    WriteXorExecute,
    MemoryCap,
    MountConfinement,
    RootfsIsolation,
    EgressConfinement,
    DnsIsolation,
    SyscallBackstop,
    DeviceAllowlist,
    SingleAddressSpace,
    ZeroTrustControl,
}

static ALL: &[Invariant] = &[
    Invariant::WriteXorExecute,
    Invariant::MemoryCap,
    Invariant::MountConfinement,
    Invariant::RootfsIsolation,
    Invariant::EgressConfinement,
    Invariant::DnsIsolation,
    Invariant::SyscallBackstop,
    Invariant::DeviceAllowlist,
    Invariant::SingleAddressSpace,
    Invariant::ZeroTrustControl,
];

impl Invariant {
    pub fn name(&self) -> &'static str {
        match self {
            Invariant::WriteXorExecute => "WriteXorExecute",
            Invariant::MemoryCap => "MemoryCap",
            Invariant::MountConfinement => "MountConfinement",
            Invariant::RootfsIsolation => "RootfsIsolation",
            Invariant::EgressConfinement => "EgressConfinement",
            Invariant::DnsIsolation => "DnsIsolation",
            Invariant::SyscallBackstop => "SyscallBackstop",
            Invariant::DeviceAllowlist => "DeviceAllowlist",
            Invariant::SingleAddressSpace => "SingleAddressSpace",
            Invariant::ZeroTrustControl => "ZeroTrustControl",
        }
    }

    pub fn check(&self) -> &'static str {
        match self {
            Invariant::WriteXorExecute => "security::enforce_wx",
            Invariant::MemoryCap => "security::enforce_memory_cap",
            Invariant::MountConfinement => "security::enforce_mount",
            Invariant::RootfsIsolation => "vfs::path::confined",
            Invariant::EgressConfinement => "security::enforce_egress",
            Invariant::DnsIsolation => "net::EgressPolicy::resolve",
            Invariant::SyscallBackstop => "security::backstop_verdict",
            Invariant::DeviceAllowlist => "vfs::Namespace::allowed_devices",
            Invariant::SingleAddressSpace => "sandbox::run::run",
            Invariant::ZeroTrustControl => "daemon::Daemon::authorized",
        }
    }

    pub fn statement(&self) -> &'static str {
        match self {
            Invariant::WriteXorExecute => {
                "WriteXorExecute: security::enforce_wx rejects PROT_WRITE|PROT_EXEC with EACCES"
            }
            Invariant::MemoryCap => {
                "MemoryCap: security::enforce_memory_cap rejects allocations over the cap with ENOMEM"
            }
            Invariant::MountConfinement => {
                "MountConfinement: security::enforce_mount resolves guest paths inside the namespace"
            }
            Invariant::RootfsIsolation => {
                "RootfsIsolation: vfs::path::confined keeps every resolved host path under the root"
            }
            Invariant::EgressConfinement => {
                "EgressConfinement: security::enforce_egress delegates to net::EgressPolicy::allows_connect"
            }
            Invariant::DnsIsolation => {
                "DnsIsolation: net::EgressPolicy::resolve answers DNS in the supervisor"
            }
            Invariant::SyscallBackstop => {
                "SyscallBackstop: Linux TRAPs served syscalls and KILLs the direct-escape deny-list; Seatbelt/Windows kill"
            }
            Invariant::DeviceAllowlist => {
                "DeviceAllowlist: vfs::Namespace::resolve rejects /dev nodes outside allowed_devices"
            }
            Invariant::SingleAddressSpace => {
                "SingleAddressSpace: one address space per instance; the supervisor control plane is shared"
            }
            Invariant::ZeroTrustControl => {
                "ZeroTrustControl: daemon::Daemon::authorized checks the bearer token on every route"
            }
        }
    }
}

pub fn all() -> &'static [Invariant] {
    ALL
}

pub fn audit() -> Vec<&'static str> {
    ALL.iter().map(Invariant::statement).collect()
}

/// Constant-time equality for secret material: SHA-256 both operands and
/// compare fixed-size digests, so neither a wrong prefix nor a length
/// difference leaks through an early-exit branch.
pub fn secure_eq(a: &[u8], b: &[u8]) -> bool {
    use sha2::{Digest, Sha256};
    let da = Sha256::digest(a);
    let db = Sha256::digest(b);
    let mut diff = 0u8;
    for (x, y) in da.iter().zip(db.iter()) {
        diff |= x ^ y;
    }
    diff == 0
}

pub fn enforce_wx(prot: u32, cfg: &Config) -> Result<()> {
    let writable = prot & PROT_WRITE != 0;
    let executable = prot & PROT_EXEC != 0;
    if cfg.reject_rwx && writable && executable {
        return Err(Error::Io(std::io::Error::from_raw_os_error(EACCES)));
    }
    Ok(())
}

pub fn enforce_memory_cap(bytes: u64, cfg: &Config) -> Result<()> {
    let cap = match (cfg.max_bytes, cfg.resource_limits.memory_bytes) {
        (0, 0) => 0,
        (requested, 0) => requested,
        (0, limit) => limit,
        (requested, limit) => requested.min(limit),
    };
    if cap != 0 && bytes > cap {
        return Err(Error::Io(std::io::Error::from_raw_os_error(ENOMEM)));
    }
    Ok(())
}

pub fn enforce_mount(n: &Namespace, guest: &str) -> Result<()> {
    if guest.is_empty() || guest.chars().any(char::is_control) {
        return Err(Error::Config(format!(
            "invalid mount guest path: {}",
            guest
        )));
    }
    let cleaned =
        crate::vfs::path::clean_guest(guest).ok_or_else(|| Error::PathEscape(guest.to_owned()))?;
    let resolved = n.resolve(&format!("/{cleaned}"))?;
    if !resolved.guest.starts_with('/') || resolved.guest.split('/').any(|part| part == "..") {
        return Err(Error::PathEscape(resolved.guest));
    }
    if resolved
        .host
        .components()
        .any(|part| matches!(part, Component::ParentDir))
    {
        return Err(Error::PathEscape(
            resolved.host.to_string_lossy().into_owned(),
        ));
    }
    Ok(())
}

pub fn enforce_egress(p: &EgressPolicy, addr: &SocketAddr, resolved: bool) -> Result<()> {
    if addr.port() == 0 {
        return Err(Error::NetworkDenied("destination port 0".to_owned()));
    }
    p.allows_connect(addr, resolved)
}

// On Linux the installed filter TRAPs the syscalls the Rust supervisor serves
// and KILLs a fixed deny-list of direct host escapes; the Go-era default-deny
// text-range backstop is NOT installed in Rust. Seatbelt (macOS) and the
// instrumentation callback (Windows) kill instead. Error::Killed is reserved
// for a call the filter genuinely rejects, not for an unrewritten site.
pub fn backstop_verdict(missed: bool) -> Result<()> {
    if !missed {
        return Ok(());
    }
    match crate::types::HostOs::current() {
        crate::types::HostOs::Linux => Ok(()),
        _ => Err(Error::Killed),
    }
}
