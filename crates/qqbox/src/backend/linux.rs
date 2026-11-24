//! Linux memfd/seccomp/trap backend.

use crate::backend::channel::{Channel, STATE_IDLE};
use crate::backend::filter::{self, TRAP_HITS};
use crate::backend::hostop::{self, nr, HostOps};
use crate::backend::trap;
use crate::backend::{Capabilities, Platform, WitnessReport};
use crate::error::{Error, Result};
use crate::vfs::Namespace;
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;

/// Holds the chanPage alive for as long as the backstop filter is installed.
/// A filter that TRAPs without a channel and a serving supervisor would hang
/// the caller, so `install_backstop` owns both.
pub struct LinuxBackend {
    backstop: Mutex<Option<Arc<Channel>>>,
    supervisor_started: AtomicBool,
    supervisor_stop: Arc<AtomicBool>,
    supervisor_thread: Mutex<Option<JoinHandle<()>>>,
}

/// SIGCHLD interrupts FUTEX_WAIT: a child that exited before publishing a
/// request must not leave the supervisor blocked forever.
static CHILD_GONE: AtomicU32 = AtomicU32::new(0);

unsafe extern "C" fn on_child(_sig: i32) {
    CHILD_GONE.store(1, Ordering::SeqCst);
    hostop::GUEST_GONE.store(1, Ordering::SeqCst);
}

impl LinuxBackend {
    pub fn new() -> Self {
        Self {
            backstop: Mutex::new(None),
            supervisor_started: AtomicBool::new(false),
            supervisor_stop: Arc::new(AtomicBool::new(false)),
            supervisor_thread: Mutex::new(None),
        }
    }

    fn install_sigchld() -> Result<()> {
        let mut sa: libc::sigaction = unsafe { std::mem::zeroed() };
        sa.sa_sigaction = on_child as *const () as usize;
        sa.sa_flags = 0; // no SA_RESTART: SIGCHLD must interrupt FUTEX_WAIT.
        unsafe {
            libc::sigemptyset(&mut sa.sa_mask);
            if libc::sigaction(libc::SIGCHLD, &sa, std::ptr::null_mut()) != 0 {
                return Err(Error::Message(format!(
                    "sigaction(SIGCHLD): {}",
                    std::io::Error::last_os_error()
                )));
            }
        }
        Ok(())
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
            // Register save/restore is aarch64-only; do not claim a handler
            // exists on x86_64 Linux where install_handler fails closed.
            sigsys_handler: cfg!(target_arch = "aarch64"),
            mach_ports: false,
            seatbelt: false,
            hypervisor_framework: false,
            appcontainer_lpac: false,
            instrumentation_callback: false,
            chan_page_bytes: crate::zig::abi::CHAN_SIZE,
            implemented_syscalls: hostop::IMPLEMENTED.len(),
            note: format!(
                "Real memfd chanPage, seccomp-BPF RET_TRAP filter, SIGSYS register save/restore and Rust host-op dispatcher; {} syscalls have real effects, the rest return -ENOSYS. PIE/ET_DYN, trampoline islands, PROT_R hop and the ~205-syscall table are not ported.",
                hostop::IMPLEMENTED.len()
            ),
        })
    }

    fn install_backstop(&self) -> Result<()> {
        // A TRAP filter without a chanPage, a SIGSYS handler AND a supervisor
        // that actually serves requests would hang or kill the caller. Install
        // all three together. The text-range prefix needs the real stub text
        // bounds, so with no Zig stub mapped we TRAP a single syscall.
        let mut slot = match self.backstop.lock() {
            Ok(g) => g,
            Err(p) => p.into_inner(),
        };
        // A second install would spawn a second, never-joined thread racing
        // the same futex word. Refuse it instead of dispatching one request
        // twice.
        if self.supervisor_started.swap(true, Ordering::SeqCst) {
            return Err(Error::Message(
                "backstop supervisor is already installed on this backend".into(),
            ));
        }
        // Claim the flag atomically, but do NOT leave it set if any fallible
        // step below fails: otherwise a failed install permanently wedges
        // every retry into "already installed". Roll back on every error path.
        let result = (|| -> Result<()> {
            if slot.is_none() {
                *slot = Some(Arc::new(Channel::create()?));
            }
            let Some(ch) = slot.as_ref() else {
                return Err(Error::Message("backstop chanPage unavailable".into()));
            };
            ch.reset();
            trap::set_channel(ch);
            trap::install_handler()?;
            Self::install_sigchld()?;

            // Build the filter before spawning the serving thread: fewer
            // fallible steps remain after a thread exists.
            let program = filter::trap_and_deny(&[nr::GETPPID as u32], filter::GUEST_DENY)?;
            let ns = Namespace::new(std::path::Path::new("/"))?;
            let serving = Arc::clone(ch);
            self.supervisor_stop.store(false, Ordering::SeqCst);
            let stop = Arc::clone(&self.supervisor_stop);
            let handle = std::thread::Builder::new()
                .name("qqbox-hostop".to_owned())
                .spawn(move || {
                    let mut ops = HostOps::new(ns, std::process::id())
                        .with_guest_pid(std::process::id() as i32);
                    while !stop.load(Ordering::SeqCst) {
                        // The SIGSYS handler alone owns REPLY -> IDLE. An
                        // unconditional ack() here would clobber a REQUEST the
                        // handler just published, losing the next trap.
                        let _ = hostop::serve_one_with_stop(&serving, &mut ops, &stop);
                        if CHILD_GONE.load(Ordering::SeqCst) != 0 {
                            hostop::GUEST_GONE.store(0, Ordering::SeqCst);
                        }
                    }
                })
                .map_err(|e| Error::Message(format!("spawn host-op thread: {e}")))?;
            *self
                .supervisor_thread
                .lock()
                .unwrap_or_else(|p| p.into_inner()) = Some(handle);
            crate::sandbox::install(&program)
        })();
        if result.is_err() {
            // Release the claim AND stop/join the servant, so a failed install
            // leaves no never-joined thread racing the chanPage futex word.
            self.supervisor_stop.store(true, Ordering::SeqCst);
            if let Some(ch) = slot.as_ref() {
                hostop::wake_all(ch);
            }
            if let Some(handle) = self
                .supervisor_thread
                .lock()
                .unwrap_or_else(|p| p.into_inner())
                .take()
            {
                let _ = handle.join();
            }
            self.supervisor_started.store(false, Ordering::SeqCst);
        }
        result
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
        let ops = HostOps::new(ns, std::process::id());
        CHILD_GONE.store(0, Ordering::SeqCst);
        hostop::GUEST_GONE.store(0, Ordering::SeqCst);
        Self::install_sigchld()?;
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
            let rc = match filter::trap_one(nr::GETPPID as u32) {
                Ok(f) => filter::install(&f),
                Err(e) => Err(e),
            };
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
        let mut ops = ops.with_guest_pid(pid);
        let (served_nr, served_ret) = hostop::serve_one(&ch, &mut ops)?;
        // Do not ack here: the guest's SIGSYS handler is still waiting for
        // STATE_REPLY. Resetting to IDLE now would race it into a spin.

        let mut status: i32 = 0;
        // SIGCHLD has no SA_RESTART (it must interrupt FUTEX_WAIT), so reap
        // through EINTR instead of treating an interruption as a failure.
        loop {
            let rc = unsafe { libc::waitpid(pid, &mut status, 0) };
            if rc >= 0 {
                break;
            }
            let err = std::io::Error::last_os_error();
            if err.raw_os_error() != Some(libc::EINTR) {
                return Err(Error::Message(format!("waitpid: {err}")));
            }
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
            filter_instructions: filter::trap_one(nr::GETPPID as u32)?.len(),
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

/// Live witness for the bounded guest-memory data plane: allocate a real
/// buffer in this process, attach it as the guest address space, and prove the
/// supervisor writes a non-zero `timespec` and a 390-byte `utsname` into it.
/// Returns `(clock_gettime_result, uname_result, tv_sec, sysname)`.
pub fn memory_witness() -> Result<(i64, i64, i64, String)> {
    let ns = Namespace::new(std::path::Path::new("/"))?;
    let mut ops = HostOps::new(ns, std::process::id()).with_guest_pid(std::process::id() as i32);
    let mut clock_buf = [0u8; 32];
    let clock_ptr = clock_buf.as_mut_ptr() as u64;
    let clock_rc = ops.dispatch(nr::CLOCK_GETTIME, [0, clock_ptr, 0, 0, 0, 0]);
    let tv_sec = i64::from_ne_bytes(clock_buf[..8].try_into().unwrap_or([0; 8]));
    let mut name_buf = [0u8; 390];
    let name_ptr = name_buf.as_mut_ptr() as u64;
    let uname_rc = ops.dispatch(nr::UNAME, [name_ptr, 0, 0, 0, 0, 0]);
    let end = name_buf.iter().position(|b| *b == 0).unwrap_or(390);
    let sysname = String::from_utf8_lossy(&name_buf[..end]).into_owned();
    Ok((clock_rc, uname_rc, tv_sec, sysname))
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
    let mut ops = HostOps::new(ns, std::process::id()).with_guest_pid(std::process::id() as i32);
    let out = hostop::serve_one(&ch, &mut ops)?;
    ch.state().store(STATE_IDLE, Ordering::SeqCst);
    Ok(out)
}
