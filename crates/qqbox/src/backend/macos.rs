//! macOS Mach / Seatbelt / Hypervisor.framework backend.
//!
//! The Go-era macOS backend used a named `MAP_SHARED` temp file (path in
//! argv[1]) as the chanPage, `os_sync_wait_on_address` for the handshake, a
//! Mach bootstrap broker port for fork/service, a compiled Seatbelt profile,
//! MAP_JIT W^X flipping, and — for low-linked non-PIE ET_EXEC guests — a
//! Hypervisor.framework VM with a second address space.
//!
//! This Rust module implements the honest, live-verifiable part: real Mach
//! capability probing (`mach_task_self`, host page size, task info) via mach2.
//! It does NOT claim guest execution: that needs the committed `stub.bin`
//! byte-identical, ad-hoc codesigning, `posix_spawnattr_setspecialport_np`,
//! the compiled Seatbelt profile, `legacyX18` Mach-O patching and the full
//! 205-syscall data plane. See docs/PORT_STATUS.md.

use crate::backend::{Capabilities, Platform, WitnessReport};
use crate::error::{Error, Result};

pub struct MacBackend;

impl MacBackend {
    pub fn new() -> Self {
        Self
    }
}

impl Default for MacBackend {
    fn default() -> Self {
        Self::new()
    }
}

/// Real Mach probe: returns the task port and host page size.
pub fn mach_probe() -> Result<(u32, u32)> {
    let task = unsafe { mach2::traps::mach_task_self() };
    if task == 0 {
        return Err(Error::Message(
            "mach_task_self returned MACH_PORT_NULL".into(),
        ));
    }
    let page_size = unsafe { mach2::vm_page_size::vm_page_size } as u32;
    if page_size == 0 {
        return Err(Error::Message("vm_page_size is zero".into()));
    }
    Ok((task, page_size))
}

impl Platform for MacBackend {
    fn name(&self) -> &'static str {
        "macos-mach-seatbelt-hvf"
    }

    fn capabilities(&self) -> Result<Capabilities> {
        let (task, page_size) = mach_probe()?;
        Ok(Capabilities {
            host: "macos",
            arch: std::env::consts::ARCH,
            memfd_channel: false,
            seccomp_trap: false,
            sigsys_handler: false,
            mach_ports: task != 0,
            seatbelt: false,
            hypervisor_framework: false,
            appcontainer_lpac: false,
            instrumentation_callback: false,
            chan_page_bytes: crate::zig::abi::CHAN_SIZE,
            implemented_syscalls: 0,
            note: format!(
                "Real Mach probe: mach_task_self={task}, vm_page_size={page_size}. \
                 Seatbelt profile compilation, MAP_JIT, the named MAP_SHARED chanPage, \
                 os_sync handshake, broker port, ad-hoc codesign and HVF guest launch \
                 are NOT ported; HVF additionally needs the com.apple.security.hypervisor \
                 entitlement. No guest executes on this host."
            ),
        })
    }

    fn install_backstop(&self) -> Result<()> {
        Err(Error::UnsupportedPlatform(
            "macOS backstop needs the compiled Seatbelt profile and ad-hoc codesigning; not ported"
                .into(),
        ))
    }

    fn witness(&self) -> Result<WitnessReport> {
        Err(Error::UnsupportedPlatform(
            "no macOS guest execution: a live trap witness runs on the Linux backend".into(),
        ))
    }
}
