//! seccomp-BPF text-range backstop.
//!
//! The committed Zig stub installs a process-wide filter from its own text and
//! a stricter per-thread guest filter. A syscall issued from outside the stub's
//! text is TRAPped to the supervisor rather than killed, so a rewrite miss is
//! correct but slower. This module builds the same BPF shape in Rust and
//! installs it with real `prctl`/`seccomp` syscalls on Linux.

use crate::error::{Error, Result};
use crate::sandbox::SockFilter;
use std::sync::atomic::{AtomicU32, Ordering};

pub const SECCOMP_RET_KILL_PROCESS: u32 = 0x8000_0000;
pub const SECCOMP_RET_ALLOW: u32 = 0x7fff_0000;
pub const SECCOMP_RET_TRAP: u32 = 0x0003_0000;
pub const SECCOMP_TRAP_TAG: u32 = 0x5ca1;
pub const SI_CODE_SECCOMP: i32 = 1;
pub const PR_SET_NO_NEW_PRIVS: i32 = 38;
pub const SECCOMP_SET_MODE_FILTER: i32 = 1;

const BPF_LD_W_ABS: u16 = 0x20;
const BPF_JEQ_K: u16 = 0x15;
const BPF_JGE_K: u16 = 0x35;
const BPF_RET_K: u16 = 0x06;

const SECCOMP_DATA_NR: u32 = 0;
const SECCOMP_DATA_ARCH: u32 = 4;
const SECCOMP_DATA_IP: u32 = 8;

fn ld(off: u32) -> SockFilter {
    SockFilter {
        code: BPF_LD_W_ABS,
        jt: 0,
        jf: 0,
        k: off,
    }
}
fn jeq(k: u32, jt: u8, jf: u8) -> SockFilter {
    SockFilter {
        code: BPF_JEQ_K,
        jt,
        jf,
        k,
    }
}
fn jge(k: u32, jt: u8, jf: u8) -> SockFilter {
    SockFilter {
        code: BPF_JGE_K,
        jt,
        jf,
        k,
    }
}
fn ret(k: u32) -> SockFilter {
    SockFilter {
        code: BPF_RET_K,
        jt: 0,
        jf: 0,
        k,
    }
}

/// The Rust equivalent of `stub/zig/seccomp.zig:load()`'s six-instruction
/// prefix. A syscall whose instruction pointer is outside `[lo, hi)` gets
/// `SECCOMP_RET_TRAP | SECCOMP_TRAP_TAG`; one inside falls through to the
/// caller's allow filter. Jump offsets are relative to the next instruction,
/// matching the Zig builder exactly (jt/jf of 0 falls through).
pub fn text_range_prefix(lo: u64, hi: u64) -> Vec<SockFilter> {
    vec![
        ld(SECCOMP_DATA_IP + 4),
        jeq((lo >> 32) as u32, 0, 3), // high word mismatch -> f[5] TRAP
        ld(SECCOMP_DATA_IP),
        jge(lo as u32, 0, 1), // ip < lo                 -> f[5] TRAP
        jge(hi as u32, 0, 1), // ip >= hi                -> f[5] TRAP; else allow filter
        ret(SECCOMP_RET_TRAP | SECCOMP_TRAP_TAG),
    ]
}

/// An allow filter that checks the audit arch and then admits the listed
/// syscall numbers, killing everything else. Append after `text_range_prefix`.
pub fn allow_list(arch: u32, allowed: &[u32]) -> Vec<SockFilter> {
    let n = allowed.len();
    let mut out = vec![
        ld(SECCOMP_DATA_ARCH),
        // index 1: wrong arch jumps past the allow chain and the ALLOW ret.
        jeq(arch, 0, (n + 2) as u8),
        ld(SECCOMP_DATA_NR),
    ];
    for (i, nr) in allowed.iter().enumerate() {
        // index 3+i: a match jumps forward to the ALLOW ret at index 3+n.
        out.push(jeq(*nr, (n - i - 1) as u8, 0));
    }
    out.push(ret(SECCOMP_RET_ALLOW));
    out.push(ret(SECCOMP_RET_KILL_PROCESS));
    out
}

/// A filter that TRAPs one syscall number and allows the rest. Used by the
/// live backend witness: a real `syscall` from a real guest thread is caught,
/// served over the chanPage, and resumed with the supervisor's result.
pub fn trap_one(nr: u32) -> Vec<SockFilter> {
    vec![
        ld(SECCOMP_DATA_NR),
        jeq(nr, 0, 1),
        ret(SECCOMP_RET_TRAP | SECCOMP_TRAP_TAG),
        ret(SECCOMP_RET_ALLOW),
    ]
}

/// Convert our portable `SockFilter` values into a Linux `sock_fprog` and
/// install them on the calling thread. Real syscalls, no mocks.
#[cfg(target_os = "linux")]
pub fn install(filters: &[SockFilter]) -> Result<()> {
    use std::mem::size_of;
    if filters.is_empty() || filters.len() > u16::MAX as usize {
        return Err(Error::Message("invalid seccomp filter length".into()));
    }
    let mut prog: Vec<libc::sock_filter> = filters
        .iter()
        .map(|f| libc::sock_filter {
            code: f.code,
            jt: f.jt,
            jf: f.jf,
            k: f.k,
        })
        .collect();
    let fprog = libc::sock_fprog {
        len: prog.len() as u16,
        filter: prog.as_mut_ptr(),
    };
    if size_of::<libc::sock_filter>() != size_of::<SockFilter>() {
        return Err(Error::Message("sock_filter ABI mismatch".into()));
    }
    let rc = unsafe { libc::prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) };
    if rc != 0 {
        return Err(Error::Message(format!(
            "PR_SET_NO_NEW_PRIVS: {}",
            std::io::Error::last_os_error()
        )));
    }
    let rc = unsafe {
        libc::syscall(
            libc::SYS_seccomp,
            SECCOMP_SET_MODE_FILTER,
            0,
            &fprog as *const libc::sock_fprog,
        )
    };
    if rc != 0 {
        return Err(Error::Message(format!(
            "seccomp(SET_MODE_FILTER): {}",
            std::io::Error::last_os_error()
        )));
    }
    Ok(())
}

#[cfg(not(target_os = "linux"))]
pub fn install(_filters: &[SockFilter]) -> Result<()> {
    Err(Error::UnsupportedPlatform(
        "seccomp-bpf is a Linux facility; use the Mach/Seatbelt or LPAC backstop".into(),
    ))
}

/// Atomic handshake counters shared with the signal handler. The handler must
/// not allocate or lock, so these are plain atomics plus volatile writes.
pub static TRAP_STATE: AtomicU32 = AtomicU32::new(0);
pub static TRAP_HITS: AtomicU32 = AtomicU32::new(0);

pub fn reset_counters() {
    TRAP_STATE.store(0, Ordering::SeqCst);
    TRAP_HITS.store(0, Ordering::SeqCst);
}
