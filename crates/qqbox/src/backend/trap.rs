//! SIGSYS trap handler for the Rust Linux backend's TRAP verdict.
//!
//! A syscall number listed in `filter::trap_and_deny` raises SIGSYS with
//! `si_code == SYS_SECCOMP` and `si_errno == SECCOMP_TRAP_TAG`. This handler
//! is not a default-deny text-range backstop: a rewrite miss outside the TRAP
//! set is ALLOWed and runs natively on the host, so static rewriting is
//! required for correctness. The handler
//! reads the saved registers from `ucontext_t`, publishes a `ChannelRequest`
//! into the shared chanPage, blocks on a futex until the Rust supervisor has
//! served it, and writes the supervisor's result into the saved `x0` so the
//! guest resumes as if the kernel had returned it.
//!
//! Signal-handler constraints: no allocation, no locks, no Rust std I/O. Only
//! volatile shared-memory access, atomics and raw futex syscalls.

use crate::backend::channel::{Channel, STATE_IDLE, STATE_REPLY, STATE_REQUEST};
use crate::backend::filter::{SECCOMP_TRAP_TAG, SI_CODE_SECCOMP, TRAP_HITS};
use crate::error::{Error, Result};
use std::sync::atomic::{AtomicUsize, Ordering};

/// Base address of the shared chanPage. Set once before the handler is active.
pub static CHAN_BASE: AtomicUsize = AtomicUsize::new(0);

pub fn set_channel(ch: &Channel) {
    CHAN_BASE.store(ch.base() as usize, Ordering::SeqCst);
}

fn futex(word: *mut u32, op: i32, val: u32) -> i64 {
    unsafe {
        libc::syscall(
            libc::SYS_futex,
            word as *mut libc::c_void,
            op,
            val as libc::c_ulong,
            std::ptr::null::<libc::c_void>(),
            std::ptr::null::<libc::c_void>(),
            0,
        )
    }
}

/// The SIGSYS handler. `info` is `siginfo_t`, `uc` is `ucontext_t`.
#[cfg(all(target_os = "linux", target_arch = "aarch64"))]
unsafe extern "C" fn on_sigsys(_sig: i32, info: *mut libc::siginfo_t, uc: *mut libc::c_void) {
    let si_code = (*info).si_code;
    let si_errno = (*info).si_errno;
    if si_code != SI_CODE_SECCOMP || si_errno as u32 != SECCOMP_TRAP_TAG {
        return;
    }
    let base = CHAN_BASE.load(Ordering::SeqCst) as *mut u8;
    if base.is_null() {
        return;
    }
    let ucx = uc.cast::<libc::ucontext_t>();
    let m = &mut (*ucx).uc_mcontext;
    // aarch64 Linux: x0..x30 live in regs[0..30]; x8 carries the syscall nr.
    let nr = m.regs[8];
    let mut args = [0u64; 6];
    for (i, slot) in args.iter_mut().enumerate() {
        *slot = m.regs[i];
    }
    let rip = m.pc;

    let mut req = crate::backend::channel::host_request(nr, args, rip);
    req.self_addr = base as u64;
    let chan = crate::backend::channel::ChannelView::new(base);
    chan.publish(&req);
    let state = chan.state_ptr();

    (*state).store(STATE_REQUEST, Ordering::SeqCst);
    futex(state as *mut u32, libc::FUTEX_WAKE, 1);

    // Event-driven wait: no polling interval, no sleep. FUTEX_WAIT blocks
    // until the supervisor stores STATE_REPLY and wakes us.
    loop {
        if (*state).load(Ordering::SeqCst) == STATE_REPLY {
            break;
        }
        futex(state as *mut u32, libc::FUTEX_WAIT, STATE_REQUEST);
    }

    m.regs[0] = chan.read_ret() as u64;
    // Release the handshake word so the next trap can publish ST_REQ. Without
    // this reset the supervisor's FUTEX_WAIT sees a stale ST_REP forever.
    (*state).store(STATE_IDLE, Ordering::SeqCst);
    TRAP_HITS.fetch_add(1, Ordering::SeqCst);
}

#[cfg(not(all(target_os = "linux", target_arch = "aarch64")))]
unsafe extern "C" fn on_sigsys(_sig: i32, _info: *mut libc::siginfo_t, _uc: *mut libc::c_void) {}

/// Install the SIGSYS handler for the calling process. Only aarch64 has a
/// real register save/restore; other Linux arches get no handler at all and
/// must not be reported as `sigsys_handler=true`.
#[cfg(all(target_os = "linux", target_arch = "aarch64"))]
pub fn install_handler() -> Result<()> {
    let mut sa: libc::sigaction = unsafe { std::mem::zeroed() };
    sa.sa_sigaction = on_sigsys as *const () as usize;
    sa.sa_flags = libc::SA_SIGINFO | libc::SA_NODEFER;
    unsafe {
        libc::sigemptyset(&mut sa.sa_mask);
    }
    let rc = unsafe { libc::sigaction(libc::SIGSYS, &sa, std::ptr::null_mut()) };
    if rc != 0 {
        return Err(Error::Message(format!(
            "sigaction(SIGSYS): {}",
            std::io::Error::last_os_error()
        )));
    }
    Ok(())
}

#[cfg(not(all(target_os = "linux", target_arch = "aarch64")))]
pub fn install_handler() -> Result<()> {
    Err(Error::UnsupportedPlatform(
        "SIGSYS register save/restore is Linux aarch64-only".into(),
    ))
}
