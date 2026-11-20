//! Linux memfd/seccomp/trap backend.

use crate::backend::channel::{Channel, STATE_IDLE};
use crate::backend::filter::{self, TRAP_HITS};
use crate::backend::hostop::{self, nr, HostOps};
use crate::backend::trap;
use crate::backend::{Capabilities, Platform, WitnessReport};
use crate::error::{Error, Result};
use crate::vfs::Namespace;
use std::sync::atomic::Ordering;

pub struct LinuxBackend;

impl LinuxBackend {
    pub fn new() -> Self {
        Self
    }
}

impl Default for LinuxBackend {
    fn default() -> Self {
        Self::new()
    }
}

impl Platform for LinuxBackend {
    fn name(&self) -> &'static str {
        "linux-memfd-seccomp-trap"
    }

    fn capabilities(&self) -> Result<Capabilities> {
        let seccomp = std::fs::read_to_string("/proc/sys/kernel/seccomp/actions_avail")
            .unwrap_or_default()
            .contains("trap");
        Ok(Capabilities {
            host: "linux",
            arch: std::env::consts::ARCH,
            memfd_channel: true,
            seccomp_trap: seccomp,
            sigsys_handler: true,
            mach_ports: false,
            seatbelt: false,
            hypervisor_framework: false,
            appcontainer_lpac: false,
            instrumentation_callback: false,
            chan_page_bytes: crate::zig::abi::CHAN_SIZE,
            implemented_syscalls: 16,
            note: "Real memfd chanPage, seccomp-BPF RET_TRAP filter, SIGSYS register save/restore and Rust host-op dispatcher. ELF mapping, trampoline islands, PROT_R hop and the ~200-syscall table are not ported.".into(),
        })
    }

    fn install_backstop(&self) -> Result<()> {
        // The text-range prefix needs the real stub text bounds. With no Zig
        // stub mapped, use a filter that TRAPs a single syscall; the launcher
        // replaces it with the full text-range program once a stub is mapped.
        filter::install(&filter::trap_one(nr::GETPPID as u32))
    }

    /// Live end-to-end witness: fork a guest thread, install a real seccomp
    /// filter and SIGSYS handler, execute a real `getppid` syscall, serve it
    /// through the chanPage from the Rust supervisor, and resume the guest
    /// with the supervisor's result.
    fn witness(&self) -> Result<WitnessReport> {
        let ch = Channel::create()?;
        ch.reset();
        trap::set_channel(&ch);
        trap::install_handler()?;
        filter::reset_counters();

        let ns = Namespace::new(std::path::Path::new("/"))?;
        let mut ops = HostOps::new(ns, std::process::id());

        // The witness uses a forked child so the seccomp filter is confined to
        // the guest thread and the supervisor keeps full syscall access.
        let pid = unsafe { libc::fork() };
        if pid < 0 {
            return Err(Error::Message(format!(
                "fork: {}",
                std::io::Error::last_os_error()
            )));
        }
        if pid == 0 {
            // ---- guest thread ----
            // Control: the same syscall BEFORE the filter exists. The kernel
            // returns the real parent pid, not the supervisor sentinel.
            let control = unsafe { libc::syscall(libc::SYS_getppid) };
            unsafe {
                std::ptr::write_volatile(
                    ch.base()
                        .add(crate::zig::abi::SCRATCH_OFF + 24)
                        .cast::<i64>(),
                    control,
                );
            }
            let rc = filter::install(&filter::trap_one(nr::GETPPID as u32));
            if let Err(e) = rc {
                eprintln!("guest: install filter failed: {e}");
                unsafe { libc::_exit(70) };
            }
            let observed = unsafe { libc::syscall(libc::SYS_getppid) };
            // Publish the value the guest actually saw back through the
            // channel scratch area so the parent can verify the resume path.
            unsafe {
                std::ptr::write_volatile(
                    ch.base()
                        .add(crate::zig::abi::SCRATCH_OFF + 8)
                        .cast::<i64>(),
                    observed,
                );
            }
            let hits = TRAP_HITS.load(Ordering::SeqCst);
            unsafe {
                std::ptr::write_volatile(
                    ch.base()
                        .add(crate::zig::abi::SCRATCH_OFF + 16)
                        .cast::<u32>(),
                    hits,
                );
            }
            unsafe { libc::_exit(if hits == 1 { 0 } else { 71 }) };
        }

        // ---- supervisor thread ----
        let (served_nr, served_ret) = hostop::serve_one(&ch, &mut ops)?;
        // Do not ack here: the guest's SIGSYS handler is still waiting for
        // STATE_REPLY. Resetting to IDLE now would race it into a spin.

        let mut status: i32 = 0;
        let waited = unsafe { libc::waitpid(pid, &mut status, 0) };
        if waited < 0 {
            return Err(Error::Message(format!(
                "waitpid: {}",
                std::io::Error::last_os_error()
            )));
        }
        let exit_ok = libc::WIFEXITED(status) && libc::WEXITSTATUS(status) == 0;
        let observed = unsafe {
            std::ptr::read_volatile(
                ch.base()
                    .add(crate::zig::abi::SCRATCH_OFF + 8)
                    .cast::<i64>(),
            )
        };
        // TRAP_HITS lives in the guest's address space after fork, so read the
        // value the child published rather than the parent's copy.
        let hits = unsafe {
            std::ptr::read_volatile(
                ch.base()
                    .add(crate::zig::abi::SCRATCH_OFF + 16)
                    .cast::<u32>(),
            )
        };
        let control = unsafe {
            std::ptr::read_volatile(
                ch.base()
                    .add(crate::zig::abi::SCRATCH_OFF + 24)
                    .cast::<i64>(),
            )
        };
        let control_differs = control != served_ret && control != observed;

        Ok(WitnessReport {
            host: "linux",
            arch: std::env::consts::ARCH,
            channel_fd: ch.fd(),
            channel_bytes: crate::zig::abi::SLOT_SIZE,
            filter_instructions: filter::trap_one(nr::GETPPID as u32).len(),
            trapped_syscall: served_nr,
            supervisor_result: served_ret,
            guest_observed_result: observed,
            untrapped_control_result: control,
            control_differs,
            trap_hits: hits,
            served: ops.served,
            match_ok: exit_ok && observed == served_ret && hits == 1 && control_differs,
        })
    }
}

/// Serve one synthetic request through the channel without a signal, for the
/// dispatcher witness used where seccomp cannot be installed.
pub fn dispatch_witness(nr: u64, args: [u64; 6]) -> Result<(u64, i64)> {
    let ch = Channel::create()?;
    ch.reset();
    let req = hostop::synthetic(nr, args);
    ch.publish(&req);
    ch.state()
        .store(crate::backend::channel::STATE_REQUEST, Ordering::SeqCst);
    let ns = Namespace::new(std::path::Path::new("/"))?;
    let mut ops = HostOps::new(ns, std::process::id());
    let out = hostop::serve_one(&ch, &mut ops)?;
    ch.state().store(STATE_IDLE, Ordering::SeqCst);
    Ok(out)
}
