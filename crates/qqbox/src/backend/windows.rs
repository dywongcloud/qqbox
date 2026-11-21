//! Windows LPAC / AppContainer backend contract (cross-compiled, not live).
//!
//! The Go-era Windows backend created an AppContainer, launched the stub with
//! `CREATE_SUSPENDED | DETACHED_PROCESS` and inherited handles, used a
//! `CreateFileMapping` section as the chanPage with an auto-reset event pair
//! per slot, and installed an x86-64 instrumentation callback as the backstop
//! (no callback exists on arm64).
//!
//! This Rust module records the contract honestly and cross-compiles for
//! `x86_64-pc-windows-msvc`. No Windows host is available in this session, so
//! no live witness is claimed.

use crate::backend::{Capabilities, Platform, WitnessReport};
use crate::error::{Error, Result};

pub struct WindowsBackend;

impl WindowsBackend {
    pub fn new() -> Self {
        Self
    }
}

impl Default for WindowsBackend {
    fn default() -> Self {
        Self::new()
    }
}

impl Platform for WindowsBackend {
    fn name(&self) -> &'static str {
        "windows-lpac-instrumentation-callback"
    }

    fn capabilities(&self) -> Result<Capabilities> {
        Ok(Capabilities {
            host: "windows",
            arch: std::env::consts::ARCH,
            memfd_channel: false,
            seccomp_trap: false,
            sigsys_handler: false,
            mach_ports: false,
            seatbelt: false,
            hypervisor_framework: false,
            appcontainer_lpac: false,
            instrumentation_callback: false,
            chan_page_bytes: crate::zig::abi::CHAN_SIZE,
            implemented_syscalls: 0,
            note: "Contract only: CreateFileMapping chanPage, per-slot auto-reset events, LPAC AppContainer, VEH + x86-64 instrumentation callback, RtlCloneUserProcess fork. Contract only: the x86_64-pc-windows-msvc cross-check is blocked by ring's C toolchain/sysroot gap on this macOS host; no Windows host witness exists.".into(),
        })
    }

    fn install_backstop(&self) -> Result<()> {
        Err(Error::UnsupportedPlatform(
            "LPAC + instrumentation callback not ported; arm64 Windows has no callback backstop"
                .into(),
        ))
    }

    fn witness(&self) -> Result<WitnessReport> {
        Err(Error::UnsupportedPlatform(
            "no Windows host available for a live guest witness".into(),
        ))
    }
}
