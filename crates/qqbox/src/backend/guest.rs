//! Real ELF guest launcher for the Linux trap-to-supervisor backend.
//!
//! Unlike the stub-based Go engine, this maps an **unmodified** static aarch64
//! ELF into the supervisor's address space and installs a parameterized
//! syscall-number seccomp filter that TRAPs the served syscalls (no text-range
//! prefix); the Rust supervisor serves them over the shared chanPage and the
//! SIGSYS handler writes the result back into the saved `x0`.
//! No rewriting, no trampoline islands, no second kernel.
//!
//! Scope is deliberately narrow: ET_EXEC, static, aarch64 only; the trapped
//! syscall set is a parameter. This is real execution, not a plan or a mock.
//! The ~205-syscall data plane, PIE/ET_DYN, signal frames, Landlock and the
//! PROT_R hop alias remain unported (see docs/NATIVE_BACKEND.md).

use crate::backend::channel::{Channel, ChannelView, STATE_REPLY, STATE_REQUEST};
use crate::backend::filter::{self, SECCOMP_TRAP_TAG, SI_CODE_SECCOMP};
use crate::error::{Error, Result};
use std::path::Path;
use std::sync::atomic::{AtomicU32, AtomicUsize, Ordering};

const ST_REQ: u32 = STATE_REQUEST;
const ST_REP: u32 = STATE_REPLY;
const TAG: u32 = SECCOMP_TRAP_TAG;
const SCRATCH: usize = crate::zig::abi::SCRATCH_OFF;
const SENTINEL: u64 = 0x5151_424f;

static CHAN_BASE: AtomicUsize = AtomicUsize::new(0);
static TRAP_HITS: AtomicU32 = AtomicU32::new(0);
static CHILD_GONE: AtomicU32 = AtomicU32::new(0);

/// Syscall numbers (asm-generic) that the launcher traps by default.
pub const DEFAULT_TRAPPED: &[u32] = &[64, 172]; // write, getpid

#[derive(Debug, Clone, serde::Serialize)]
pub struct GuestOptions {
    /// Syscall numbers to intercept and serve from Rust.
    pub trapped: Vec<u32>,
    /// Value returned for `getpid` so the guest can prove the supervisor ran.
    pub pid_sentinel: u64,
    /// Bytes of guest stack to allocate.
    pub stack_bytes: usize,
    /// Exit code the guest produces only when the supervisor's sentinel was
    /// returned for getpid. The kernel-only baseline returns a real pid and
    /// exits 1. This is the falsification anchor.
    pub expected_exit: i32,
}

impl Default for GuestOptions {
    fn default() -> Self {
        Self {
            trapped: DEFAULT_TRAPPED.to_vec(),
            pid_sentinel: SENTINEL,
            stack_bytes: 64 * 1024,
            expected_exit: 42,
        }
    }
}

#[derive(Debug, Clone, serde::Serialize)]
pub struct GuestReport {
    pub host: &'static str,
    pub arch: &'static str,
    pub entry: u64,
    pub trapped: Vec<u32>,
    pub served: Vec<u64>,
    pub trap_hits: u32,
    pub guest_exit_code: i32,
    pub pid_sentinel: u64,
    pub match_ok: bool,
}

#[repr(C)]
struct Ehdr {
    ident: [u8; 16],
    etype: u16,
    machine: u16,
    ver: u32,
    entry: u64,
    phoff: u64,
    shoff: u64,
    flags: u32,
    ehsize: u16,
    phentsize: u16,
    phnum: u16,
    shentsize: u16,
    shnum: u16,
    shstrndx: u16,
}

#[repr(C)]
struct Phdr {
    ptype: u32,
    flags: u32,
    off: u64,
    vaddr: u64,
    paddr: u64,
    filesz: u64,
    memsz: u64,
    align: u64,
}

struct MappedGuest {
    entry: u64,
    stack_top: usize,
}

fn futex(word: *mut u32, op: i32, val: u32) -> i64 {
    unsafe {
        libc::syscall(
            libc::SYS_futex,
            word.cast::<libc::c_void>(),
            op,
            val as libc::c_ulong,
            std::ptr::null::<libc::c_void>(),
            std::ptr::null::<libc::c_void>(),
            0,
        )
    }
}

unsafe extern "C" fn on_child(_sig: i32) {
    CHILD_GONE.store(1, Ordering::SeqCst);
}

/// SIGSYS handler. Async-signal-safe: volatile shared-memory writes, atomics
/// and raw futex only. No allocation, locks or Rust std I/O.
#[cfg(all(target_os = "linux", target_arch = "aarch64"))]
unsafe extern "C" fn on_sigsys(_sig: i32, info: *mut libc::siginfo_t, uc: *mut libc::c_void) {
    if (*info).si_code != SI_CODE_SECCOMP || (*info).si_errno as u32 != TAG {
        return;
    }
    let base = CHAN_BASE.load(Ordering::SeqCst) as *mut u8;
    if base.is_null() {
        return;
    }
    let m = &mut (*uc.cast::<libc::ucontext_t>()).uc_mcontext;
    let nr = m.regs[8];
    let view = ChannelView::new(base);
    let mut req = crate::backend::channel::host_request(nr, [0u64; 6], m.pc);
    for (i, slot) in req.args.iter_mut().enumerate() {
        *slot = m.regs[i];
    }
    req.self_addr = base as u64;
    view.publish(&req);
    let state = view.state_ptr();
    (*state).store(ST_REQ, Ordering::SeqCst);
    futex(state as *mut u32, libc::FUTEX_WAKE, 1);
    loop {
        if (*state).load(Ordering::SeqCst) == ST_REP {
            break;
        }
        futex(state as *mut u32, libc::FUTEX_WAIT, ST_REQ);
    }
    let ret = view.read_ret() as u64;
    std::ptr::write_volatile(
        base.add(SCRATCH).cast::<u32>(),
        TRAP_HITS.fetch_add(1, Ordering::SeqCst) + 1,
    );
    (*state).store(0, Ordering::SeqCst);
    m.regs[0] = ret;
}

#[cfg(not(all(target_os = "linux", target_arch = "aarch64")))]
unsafe extern "C" fn on_sigsys(_sig: i32, _info: *mut libc::siginfo_t, _uc: *mut libc::c_void) {}

/// Confirm the guest's PT_LOAD ranges do not overlap the supervisor's own
/// mappings. `MAP_FIXED` would silently discard existing mappings, so refuse
/// the collision instead of corrupting the supervisor.
fn disjoint_from_self(bytes: &[u8]) -> Result<()> {
    if bytes.len() < std::mem::size_of::<Ehdr>() {
        return Err(Error::Message("guest ELF is truncated".into()));
    }
    let eh = unsafe { std::ptr::read_unaligned(bytes.as_ptr().cast::<Ehdr>()) };
    let phentsize = eh.phentsize as usize;
    if phentsize < std::mem::size_of::<Phdr>() {
        return Err(Error::Message(
            "guest program header entry is too small".into(),
        ));
    }
    let phdr_end = (eh.phnum as usize)
        .checked_mul(phentsize)
        .and_then(|n| (eh.phoff as usize).checked_add(n))
        .ok_or_else(|| Error::Message("guest program header table overflows".into()))?;
    if phdr_end > bytes.len() {
        return Err(Error::Message("guest program headers out of bounds".into()));
    }
    let maps = std::fs::read_to_string("/proc/self/maps")
        .map_err(|e| Error::Message(format!("read /proc/self/maps: {e}")))?;
    for line in maps.lines() {
        let range = match line.split_whitespace().next() {
            Some(v) => v,
            None => continue,
        };
        let (lo, hi) = match range.split_once('-') {
            Some((a, b)) => match (u64::from_str_radix(a, 16), u64::from_str_radix(b, 16)) {
                (Ok(x), Ok(y)) => (x, y),
                _ => continue,
            },
            None => continue,
        };
        for i in 0..eh.phnum as usize {
            let off = (eh.phoff as usize)
                .checked_add(i * phentsize)
                .ok_or_else(|| Error::Message("guest program header offset overflows".into()))?;
            if off + std::mem::size_of::<Phdr>() > bytes.len() {
                return Err(Error::Message("guest program header out of bounds".into()));
            }
            let ph = unsafe { std::ptr::read_unaligned(bytes.as_ptr().add(off).cast::<Phdr>()) };
            if ph.ptype != 1 {
                continue;
            }
            let start = ph.vaddr & !4095u64;
            let end = ph
                .vaddr
                .checked_add(ph.memsz)
                .and_then(|v| v.checked_add(4095))
                .map(|v| v & !4095u64)
                .ok_or_else(|| Error::Message("guest segment range overflows".into()))?;
            if end <= start {
                return Err(Error::Message("guest segment range is empty".into()));
            }
            if start < hi && lo < end {
                return Err(Error::Message(format!(
                    "guest PT_LOAD {start:#x}..{end:#x} overlaps supervisor mapping {lo:#x}..{hi:#x}"
                )));
            }
        }
    }
    Ok(())
}

/// Map a static ET_EXEC aarch64 ELF with W^X protections and a fresh stack.
#[cfg(all(target_os = "linux", target_arch = "aarch64"))]
fn map_guest(bytes: &[u8], stack_bytes: usize) -> Result<MappedGuest> {
    if bytes.len() < std::mem::size_of::<Ehdr>() {
        return Err(Error::Message("guest ELF is truncated".into()));
    }
    let eh = unsafe { std::ptr::read_unaligned(bytes.as_ptr().cast::<Ehdr>()) };
    if (eh.phentsize as usize) < std::mem::size_of::<Phdr>() {
        return Err(Error::Message(
            "guest program header entry is too small".into(),
        ));
    }
    if &eh.ident[..4] != b"\x7fELF" || eh.ident[4] != 2 {
        return Err(Error::Message("guest is not a 64-bit ELF".into()));
    }
    if eh.machine != 183 {
        return Err(Error::Message("guest is not aarch64".into()));
    }
    if eh.etype != 2 {
        return Err(Error::Message("guest must be static ET_EXEC".into()));
    }
    disjoint_from_self(bytes)?;

    let mut mapped_any = false;
    for i in 0..eh.phnum as usize {
        let stride = i
            .checked_mul(eh.phentsize as usize)
            .ok_or_else(|| Error::Message("guest program header offset overflows".into()))?;
        let off = (eh.phoff as usize)
            .checked_add(stride)
            .ok_or_else(|| Error::Message("guest program header offset overflows".into()))?;
        let hdr_end = off
            .checked_add(std::mem::size_of::<Phdr>())
            .ok_or_else(|| Error::Message("guest program header offset overflows".into()))?;
        if hdr_end > bytes.len() {
            return Err(Error::Message("guest program header out of bounds".into()));
        }
        let ph = unsafe { std::ptr::read_unaligned(bytes.as_ptr().add(off).cast::<Phdr>()) };
        if ph.ptype != 1 {
            continue;
        }
        let src_off = usize::try_from(ph.off)
            .map_err(|_| Error::Message("guest PT_LOAD offset out of range".into()))?;
        let src_end = ph
            .off
            .checked_add(ph.filesz)
            .and_then(|v| usize::try_from(v).ok())
            .ok_or_else(|| Error::Message("guest PT_LOAD file size invalid".into()))?;
        if src_end > bytes.len() || src_off > src_end || ph.filesz > ph.memsz {
            return Err(Error::Message("guest PT_LOAD file size invalid".into()));
        }
        let start = ph.vaddr & !4095u64;
        let end = ph
            .vaddr
            .checked_add(ph.memsz)
            .and_then(|v| v.checked_add(4095))
            .map(|v| v & !4095u64)
            .ok_or_else(|| Error::Message("guest PT_LOAD range overflows".into()))?;
        if end <= start {
            return Err(Error::Message("guest PT_LOAD range is empty".into()));
        }
        let len = (end - start) as usize;
        let map = unsafe {
            libc::mmap(
                start as *mut libc::c_void,
                len,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_PRIVATE | libc::MAP_ANONYMOUS | libc::MAP_FIXED,
                -1,
                0,
            )
        };
        if map == libc::MAP_FAILED {
            return Err(Error::Message(format!(
                "mmap guest segment: {}",
                std::io::Error::last_os_error()
            )));
        }
        let src = &bytes[src_off..src_end];
        unsafe {
            std::ptr::copy_nonoverlapping(src.as_ptr(), ph.vaddr as *mut u8, src.len());
            let mut a = ph.vaddr & !63u64;
            let b = ph
                .vaddr
                .checked_add(ph.filesz)
                .ok_or_else(|| Error::Message("guest PT_LOAD cache range overflows".into()))?;
            while a < b {
                core::arch::asm!("dc civac, {x}", x = in(reg) a, options(nostack));
                a += 64;
            }
            core::arch::asm!("dsb ish", options(nostack));
            let mut a = ph.vaddr & !63u64;
            while a < b {
                core::arch::asm!("ic ivau, {x}", x = in(reg) a, options(nostack));
                a += 64;
            }
            core::arch::asm!("dsb ish", "isb", options(nostack));
            let prot = (if ph.flags & 4 != 0 {
                libc::PROT_READ
            } else {
                0
            }) | (if ph.flags & 2 != 0 {
                libc::PROT_WRITE
            } else {
                0
            }) | (if ph.flags & 1 != 0 {
                libc::PROT_EXEC
            } else {
                0
            });
            if libc::mprotect(start as *mut libc::c_void, len, prot) != 0 {
                return Err(Error::Message(format!(
                    "mprotect guest W^X: {}",
                    std::io::Error::last_os_error()
                )));
            }
        }
        mapped_any = true;
    }
    if !mapped_any {
        return Err(Error::Message("guest has no PT_LOAD segments".into()));
    }

    let stack = unsafe {
        libc::mmap(
            std::ptr::null_mut(),
            stack_bytes,
            libc::PROT_READ | libc::PROT_WRITE,
            libc::MAP_PRIVATE | libc::MAP_ANONYMOUS,
            -1,
            0,
        )
    };
    if stack == libc::MAP_FAILED {
        return Err(Error::Message(format!(
            "mmap guest stack: {}",
            std::io::Error::last_os_error()
        )));
    }
    Ok(MappedGuest {
        entry: eh.entry,
        stack_top: (stack as usize + stack_bytes) & !15usize,
    })
}

#[cfg(not(all(target_os = "linux", target_arch = "aarch64")))]
fn map_guest(_bytes: &[u8], _stack_bytes: usize) -> Result<MappedGuest> {
    Err(Error::UnsupportedPlatform(
        "real ELF guest launch is Linux aarch64 static ET_EXEC only".into(),
    ))
}

/// Build the broker filter from the single shared, statically verified
/// builder. No duplicate BPF arithmetic: the same program shape and the same
/// bounds checks as `filter::trap_one`.
fn broker_filter(trapped: &[u32]) -> Result<Vec<crate::sandbox::SockFilter>> {
    filter::trap_and_deny(trapped, filter::GUEST_DENY)
}

/// Execute a real unmodified guest ELF under the Rust supervisor.
#[cfg(all(target_os = "linux", target_arch = "aarch64"))]
pub fn run_elf(guest: &Path, opts: &GuestOptions) -> Result<GuestReport> {
    let bytes = std::fs::read(guest).map_err(|e| Error::Message(format!("read guest ELF: {e}")))?;
    let mapped = map_guest(&bytes, opts.stack_bytes)?;
    let ch = Channel::create()?;
    ch.reset();
    CHAN_BASE.store(ch.base() as usize, Ordering::SeqCst);
    TRAP_HITS.store(0, Ordering::SeqCst);
    CHILD_GONE.store(0, Ordering::SeqCst);
    let filter = broker_filter(&opts.trapped)?;
    let mut sa: libc::sigaction = unsafe { std::mem::zeroed() };
    sa.sa_sigaction = on_sigsys as *const () as usize;
    sa.sa_flags = libc::SA_SIGINFO | libc::SA_NODEFER;
    unsafe {
        libc::sigemptyset(&mut sa.sa_mask);
        if libc::sigaction(libc::SIGSYS, &sa, std::ptr::null_mut()) != 0 {
            return Err(Error::Message(format!(
                "sigaction(SIGSYS): {}",
                std::io::Error::last_os_error()
            )));
        }
        let mut child_sa: libc::sigaction = std::mem::zeroed();
        child_sa.sa_sigaction = on_child as *const () as usize;
        child_sa.sa_flags = 0; // no SA_RESTART: SIGCHLD interrupts FUTEX_WAIT.
        libc::sigemptyset(&mut child_sa.sa_mask);
        if libc::sigaction(libc::SIGCHLD, &child_sa, std::ptr::null_mut()) != 0 {
            return Err(Error::Message(format!(
                "sigaction(SIGCHLD): {}",
                std::io::Error::last_os_error()
            )));
        }
    }

    let pid = unsafe { libc::fork() };
    if pid < 0 {
        return Err(Error::Message(format!(
            "fork: {}",
            std::io::Error::last_os_error()
        )));
    }
    if pid == 0 {
        if let Err(e) = filter::install(&filter) {
            eprintln!("guest: filter install failed: {e}");
            unsafe { libc::_exit(70) };
        }
        unsafe {
            core::arch::asm!(
                "mov sp, {s}",
                "br {e}",
                s = in(reg) mapped.stack_top,
                e = in(reg) mapped.entry,
                options(noreturn)
            );
        }
    }

    let state = ch.state_word();
    let mut served = Vec::new();
    loop {
        loop {
            let cur = ch.state().load(Ordering::SeqCst);
            if cur == ST_REQ {
                break;
            }
            let rc = futex(state, libc::FUTEX_WAIT, cur);
            if rc < 0 {
                let e = std::io::Error::last_os_error();
                if e.raw_os_error() == Some(libc::EINTR) && CHILD_GONE.load(Ordering::SeqCst) != 0 {
                    break;
                }
            }
        }
        if CHILD_GONE.load(Ordering::SeqCst) != 0 && ch.state().load(Ordering::SeqCst) != ST_REQ {
            break;
        }
        let req = ch.request();
        served.push(req.nr);
        let ret: i64 = match req.nr {
            64 => {
                let len = (req.args[2] as usize).min(4096);
                let mut buf = [0u8; 4096];
                let local = libc::iovec {
                    iov_base: buf.as_mut_ptr().cast(),
                    iov_len: len,
                };
                let remote = libc::iovec {
                    iov_base: req.args[1] as *mut libc::c_void,
                    iov_len: len,
                };
                let got = unsafe { libc::process_vm_readv(pid, &local, 1, &remote, 1, 0) };
                if got <= 0 {
                    -38
                } else {
                    unsafe { libc::write(1, buf.as_ptr().cast(), got as usize) as i64 }
                }
            }
            172 => opts.pid_sentinel as i64,
            _ => -38,
        };
        ch.write_ret(ret);
        ch.state().store(STATE_REPLY, Ordering::SeqCst);
        futex(state, libc::FUTEX_WAKE, 1);
    }

    let mut status = 0i32;
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
    let hits = unsafe { std::ptr::read_volatile(ch.base().add(SCRATCH).cast::<u32>()) };
    let guest_exit_code = if libc::WIFEXITED(status) {
        libc::WEXITSTATUS(status)
    } else {
        -1
    };
    Ok(GuestReport {
        host: "linux",
        arch: "aarch64",
        entry: mapped.entry,
        trapped: opts.trapped.clone(),
        served,
        trap_hits: hits,
        guest_exit_code,
        pid_sentinel: opts.pid_sentinel,
        match_ok: guest_exit_code == opts.expected_exit && hits >= 2,
    })
}

#[cfg(not(all(target_os = "linux", target_arch = "aarch64")))]
pub fn run_elf(_guest: &Path, _opts: &GuestOptions) -> Result<GuestReport> {
    Err(Error::UnsupportedPlatform(
        "real ELF guest launch requires Linux aarch64".into(),
    ))
}
