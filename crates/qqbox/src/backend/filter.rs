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
///
/// The Zig builder requires `lo >> 32 == hi >> 32`: the prefix checks only the
/// high word once. A range crossing a 4 GiB boundary would admit in-range IPs
/// as TRAPs, so refuse it instead of emitting an unsound program.
pub fn text_range_prefix(lo: u64, hi: u64) -> Result<Vec<SockFilter>> {
    if lo >= hi {
        return Err(Error::Message("invalid seccomp text range".into()));
    }
    if lo >> 32 != hi >> 32 {
        return Err(Error::Message(
            "seccomp text range crosses a 4 GiB boundary".into(),
        ));
    }
    Ok(vec![
        ld(SECCOMP_DATA_IP + 4),
        jeq((lo >> 32) as u32, 0, 3), // high word mismatch -> f[5] TRAP
        ld(SECCOMP_DATA_IP),
        jge(lo as u32, 0, 1), // ip < lo               -> f[5] TRAP
        jge(hi as u32, 0, 1), // ip >= hi              -> f[5] TRAP; else allow filter
        ret(SECCOMP_RET_TRAP | SECCOMP_TRAP_TAG),
    ])
}

/// An allow filter that checks the audit arch, admits the listed syscall
/// numbers and KILLs everything else. Append after `text_range_prefix`.
///
/// Layout (n = allowed.len()):
///   0        LD arch
///   1        JEQ arch -> next (2), else -> 3+n (KILL)
///   2        LD nr
///   3+i      JEQ nr_i -> 4+n (ALLOW), else -> next
///   3+n      RET KILL_PROCESS
///   4+n      RET ALLOW
/// A mismatch falls through the whole chain to KILL; only a match reaches
/// ALLOW. BPF `jt`/`jf` are u8, so bound the list length before casting.
pub fn allow_list(arch: u32, allowed: &[u32]) -> Result<Vec<SockFilter>> {
    let n = allowed.len();
    // Max jf is n+1 (index 1 -> index 3+n) and max jt is n (index 3 -> index
    // 4+n); both must fit in u8 without truncation.
    if n == 0 || n + 1 > u8::MAX as usize {
        return Err(Error::Message(format!(
            "allow_list length {n} exceeds BPF jump offset range"
        )));
    }
    let mut out = Vec::with_capacity(n + 5);
    out.push(ld(SECCOMP_DATA_ARCH));
    out.push(jeq(arch, 0, (n + 1) as u8));
    out.push(ld(SECCOMP_DATA_NR));
    for (i, nr) in allowed.iter().enumerate() {
        out.push(jeq(*nr, (n - i) as u8, 0));
    }
    out.push(ret(SECCOMP_RET_KILL_PROCESS));
    out.push(ret(SECCOMP_RET_ALLOW));
    Ok(out)
}

/// AUDIT_ARCH values (audit.h).
pub const AUDIT_ARCH_AARCH64: u32 = 0xc000_00b7;
pub const AUDIT_ARCH_X86_64: u32 = 0xc000_003e;

/// The audit arch of the compiled target. An unsupported architecture yields
/// 0, which no real kernel reports, so a filter built from it KILLs rather
/// than silently allowing a foreign-ABI syscall.
pub const fn host_audit_arch() -> u32 {
    if cfg!(target_arch = "aarch64") {
        AUDIT_ARCH_AARCH64
    } else if cfg!(target_arch = "x86_64") {
        AUDIT_ARCH_X86_64
    } else {
        0
    }
}

/// Build a filter that TRAPs the given syscall numbers on the host's audit
/// arch, ALLOWs other syscalls on that arch, and KILLs foreign-ABI syscalls.
///
/// Layout (n = trapped.len()):
///   0        LD arch
///   1        JEQ arch -> 2, else -> 5+n (KILL)
///   2        LD nr
///   3+i      JEQ nr_i -> 4+n (TRAP), else -> next
///   3+n      RET ALLOW
///   4+n      RET TRAP | SECCOMP_TRAP_TAG
///   5+n      RET KILL_PROCESS
/// `jt`/`jf` are u8, so bound n before casting. `futex` and `rt_sigreturn`
/// are refused: the SIGSYS handler needs them for the chanPage handshake.
pub fn trap_syscalls(trapped: &[u32]) -> Result<Vec<SockFilter>> {
    let n = trapped.len();
    if n == 0 || n + 3 > u8::MAX as usize {
        return Err(Error::Message(format!(
            "trapped syscall count {n} exceeds BPF jump offset range"
        )));
    }
    // asm-generic syscall numbers the SIGSYS handler itself issues.
    const NR_FUTEX: u32 = 98;
    const NR_RT_SIGRETURN: u32 = 139;
    if trapped.contains(&NR_FUTEX) {
        return Err(Error::Message(
            "cannot trap futex: the chanPage handshake needs it".into(),
        ));
    }
    if trapped.contains(&NR_RT_SIGRETURN) {
        return Err(Error::Message(
            "cannot trap rt_sigreturn: the SIGSYS handler needs it".into(),
        ));
    }
    let mut out = Vec::with_capacity(n + 6);
    out.push(ld(SECCOMP_DATA_ARCH));
    out.push(jeq(host_audit_arch(), 0, (n + 3) as u8));
    out.push(ld(SECCOMP_DATA_NR));
    for (i, k) in trapped.iter().enumerate() {
        out.push(jeq(*k, (n - i) as u8, 0));
    }
    out.push(ret(SECCOMP_RET_ALLOW));
    out.push(ret(SECCOMP_RET_TRAP | SECCOMP_TRAP_TAG));
    out.push(ret(SECCOMP_RET_KILL_PROCESS));
    validate(&out)?;
    Ok(out)
}

/// A filter that TRAPs one syscall number and allows the rest. Used by the
/// live backend witness: a real `syscall` from a real guest thread is caught,
/// served over the chanPage, and resumed with the supervisor's result.
pub fn trap_one(nr: u32) -> Result<Vec<SockFilter>> {
    trap_syscalls(&[nr])
}

/// Static BPF verifier: prove every jump target is inside the program and
/// that execution cannot fall off the end. This is a total, allocation-free
/// check applied before any program reaches the kernel. No unreachable-code
/// or out-of-range jump can be installed.
pub fn validate(filters: &[SockFilter]) -> Result<()> {
    if filters.is_empty() || filters.len() > u16::MAX as usize {
        return Err(Error::Message("invalid seccomp filter length".into()));
    }
    let len = filters.len();
    for (pc, f) in filters.iter().enumerate() {
        let class = f.code & 0x07;
        let is_jump =
            class == 0x05 && matches!(f.code & 0xf8, 0x10 | 0x20 | 0x30 | 0x40 | 0x50 | 0x80);
        if is_jump {
            for (which, off) in [("jt", f.jt), ("jf", f.jf)] {
                let target = pc + 1 + off as usize;
                if target >= len {
                    return Err(Error::Message(format!(
                        "BPF {which} jump at pc={pc} targets {target}, program length {len}"
                    )));
                }
            }
        } else if pc + 1 == len && class != 0x06 {
            return Err(Error::Message(format!(
                "BPF program falls through past final instruction at pc={pc}"
            )));
        }
    }
    Ok(())
}

/// Convert our portable `SockFilter` values into a Linux `sock_fprog` and
/// install them on the calling thread. Real syscalls, no mocks.
#[cfg(target_os = "linux")]
pub fn install(filters: &[SockFilter]) -> Result<()> {
    use std::mem::size_of;
    validate(filters)?;
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
