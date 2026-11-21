//! Native guest-execution backends (the "South" side of the port).
//!
//! qqbox runs unmodified Linux ELF code on the host CPU by rewriting syscall
//! instructions; the supervisor owns the data plane through a 128-byte
//! chanPage. The Go engine had three per-host backends: Linux
//! (memfd/seccomp/hop), macOS (Mach/Seatbelt/HVF) and Windows (LPAC).
//!
//! This Rust port implements the Linux trap-to-supervisor path in full for a
//! witnessed syscall subset: a real `memfd_create` channel, a real seccomp-BPF
//! text-range/`RET_TRAP` filter, a real `SIGSYS` handler that saves guest
//! registers into the chanPage, and a real Rust host-op dispatcher that serves
//! the request and resumes the guest with its result. macOS and Windows provide
//! real capability probes and honest boundaries (see their modules).
//!
//! Not yet ported: PIE/ET_DYN, trampoline islands, the full ~205-syscall
//! table, the PROT_R hop alias, ptrace/Mach exception register save-restore,
//! and the Seatbelt/HVF/LPAC launch contracts. Static aarch64 ELF segment
//! mapping IS ported in `guest.rs`. `go-removal` remains pending.

use crate::error::{Error, Result};
use serde::Serialize;

pub mod channel;
pub mod filter;

#[cfg(target_os = "linux")]
pub mod guest;
#[cfg(target_os = "linux")]
pub mod hostop;
#[cfg(target_os = "linux")]
pub mod linux;
#[cfg(target_os = "macos")]
pub mod macos;
#[cfg(target_os = "linux")]
pub mod trap;
#[cfg(target_os = "windows")]
pub mod windows;

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Capabilities {
    pub host: &'static str,
    pub arch: &'static str,
    pub memfd_channel: bool,
    pub seccomp_trap: bool,
    pub sigsys_handler: bool,
    pub mach_ports: bool,
    pub seatbelt: bool,
    pub hypervisor_framework: bool,
    pub appcontainer_lpac: bool,
    pub instrumentation_callback: bool,
    pub chan_page_bytes: usize,
    pub implemented_syscalls: usize,
    pub note: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct WitnessReport {
    pub host: &'static str,
    pub arch: &'static str,
    pub channel_fd: i32,
    pub channel_bytes: usize,
    pub filter_instructions: usize,
    pub trapped_syscall: u64,
    pub supervisor_result: i64,
    pub guest_observed_result: i64,
    pub untrapped_control_result: i64,
    pub control_differs: bool,
    pub trap_hits: u32,
    pub served: u64,
    pub match_ok: bool,
}

/// Per-host backend contract. Implementations take options through methods,
/// never read the environment, and never claim a capability they lack.
pub trait Platform: Send + Sync {
    fn name(&self) -> &'static str;
    fn capabilities(&self) -> Result<Capabilities>;
    /// Install the host's syscall backstop on the calling thread.
    fn install_backstop(&self) -> Result<()>;
    /// Run the live end-to-end trap witness.
    fn witness(&self) -> Result<WitnessReport>;
}

pub fn current() -> Box<dyn Platform> {
    // The SIGSYS register save/restore is aarch64-only. Installing a TRAP
    // filter on another Linux arch would kill the guest with an empty handler.
    #[cfg(all(target_os = "linux", target_arch = "aarch64"))]
    {
        return Box::new(linux::LinuxBackend::new());
    }
    #[cfg(target_os = "macos")]
    {
        return Box::new(macos::MacBackend::new());
    }
    #[cfg(target_os = "windows")]
    {
        return Box::new(windows::WindowsBackend::new());
    }
    #[allow(unreachable_code)]
    Box::new(UnsupportedBackend)
}

struct UnsupportedBackend;

impl Platform for UnsupportedBackend {
    fn name(&self) -> &'static str {
        "unsupported"
    }
    fn capabilities(&self) -> Result<Capabilities> {
        Err(Error::UnsupportedPlatform(
            "no native backend for this host".into(),
        ))
    }
    fn install_backstop(&self) -> Result<()> {
        Err(Error::UnsupportedPlatform(
            "no native backend for this host".into(),
        ))
    }
    fn witness(&self) -> Result<WitnessReport> {
        Err(Error::UnsupportedPlatform(
            "no native backend for this host".into(),
        ))
    }
}
