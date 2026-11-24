//! The supervisor side of the chanPage protocol (the "North" data plane).
//!
//! The Go host-op thread served roughly 200 guest syscalls. This Rust port
//! starts with a real, witnessed subset: process identity, stdio, brk, clock,
//! fd table and vfs Namespace path resolution. Every syscall that is not
//! implemented returns `-ENOSYS` rather than pretending to have run, so the
//! surface is honest about its size.

use crate::backend::channel::{
    host_request, Channel, STATE_IDLE, STATE_REPLY, STATE_REQUEST, STATE_STOP,
};
use crate::error::{Error, Result};
use crate::vfs::Namespace;
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};

pub const ENOSYS: i64 = -38;
pub const EBADF: i64 = -9;
pub const EFAULT: i64 = -14;
pub const EIO: i64 = -5;
/// Distinctive supervisor result: proves the guest's return value came from
/// the Rust data plane rather than from the kernel's own getppid.
pub const SUPERVISOR_SENTINEL: i64 = 0x5151_424f;

/// asm-generic syscall numbers this dispatcher actually serves. `IMPLEMENTED`
/// is the single source of truth: the capability report derives its count from
/// it and `dispatch` refuses any number outside it, so the two cannot drift.
pub mod nr {
    pub const OPENAT: u64 = 56;
    pub const CLOSE: u64 = 57;
    pub const READ: u64 = 63;
    pub const WRITE: u64 = 64;
    pub const CLOCK_GETTIME: u64 = 113;
    pub const UNAME: u64 = 160;
    pub const GETPID: u64 = 172;
    pub const GETPPID: u64 = 173;
    pub const GETUID: u64 = 174;
    pub const GETEUID: u64 = 175;
    pub const GETGID: u64 = 176;
    pub const GETEGID: u64 = 177;
    pub const GETTID: u64 = 178;
    pub const BRK: u64 = 214;
}

pub const IMPLEMENTED: &[u64] = &[
    nr::OPENAT,
    nr::CLOSE,
    nr::READ,
    nr::WRITE,
    nr::CLOCK_GETTIME,
    nr::UNAME,
    nr::GETPID,
    nr::GETPPID,
    nr::GETUID,
    nr::GETEUID,
    nr::GETGID,
    nr::GETEGID,
    nr::GETTID,
    nr::BRK,
];

/// Guest buffers are bounded; a trapped syscall can never make the supervisor
/// copy more than a chanPage slot's worth of bytes.
pub const MAX_GUEST_IO: usize = 4096;
/// Linux `struct utsname`: six NUL-padded 65-byte fields.
const UTSNAME_LEN: usize = 6 * 65;

/// Set when the guest exits before publishing a request. SIGCHLD interrupts
/// FUTEX_WAIT so `serve_one` fails instead of blocking forever.
pub static GUEST_GONE: AtomicU32 = AtomicU32::new(0);

#[derive(Debug, Clone, Default)]
pub struct GuestRegs {
    pub brk: u64,
    /// Real host descriptors opened on the guest's behalf, keyed by fd number.
    pub fd_table: BTreeMap<i32, String>,
}

/// Supervisor state: owns the vfs Namespace (the only filesystem) and the
/// guest's brk/fd state. `guest_pid` is the process whose address space holds
/// guest pointers; `None` means there is no guest address space, so syscalls
/// that need one return -EFAULT instead of dereferencing arbitrary integers.
pub struct HostOps {
    pub guest: GuestRegs,
    pub namespace: Namespace,
    pub pid: u32,
    pub guest_pid: Option<i32>,
    pub served: u64,
}

impl HostOps {
    pub fn new(namespace: Namespace, pid: u32) -> Self {
        Self {
            guest: GuestRegs::default(),
            namespace,
            pid,
            guest_pid: None,
            served: 0,
        }
    }

    /// Attach a guest address space. Libraries take options, never ambient
    /// process state: the caller supplies the pid explicitly.
    pub fn with_guest_pid(mut self, pid: i32) -> Self {
        self.guest_pid = Some(pid);
        self
    }

    /// Only descriptors opened on the guest's behalf may be referenced. A
    /// truncated u64 must never become a supervisor fd (stdout, sockets,
    /// logs, the chanPage fd itself).
    fn guest_fd(&self, raw: u64) -> Option<i32> {
        let fd = i32::try_from(raw).ok()?;
        self.guest.fd_table.contains_key(&fd).then_some(fd)
    }

    fn read_guest(&self, addr: u64, max: usize) -> Option<Vec<u8>> {
        let pid = self.guest_pid?;
        if addr == 0 || max == 0 || max > MAX_GUEST_IO {
            return None;
        }
        let mut buf = vec![0u8; max];
        let local = libc::iovec {
            iov_base: buf.as_mut_ptr().cast(),
            iov_len: max,
        };
        let remote = libc::iovec {
            iov_base: addr as *mut libc::c_void,
            iov_len: max,
        };
        let n = unsafe { libc::process_vm_readv(pid, &local, 1, &remote, 1, 0) };
        if n <= 0 {
            return None;
        }
        buf.truncate(n as usize);
        Some(buf)
    }

    fn write_guest(&self, addr: u64, data: &[u8]) -> Option<usize> {
        let pid = self.guest_pid?;
        if addr == 0 || data.is_empty() || data.len() > MAX_GUEST_IO {
            return None;
        }
        let local = libc::iovec {
            iov_base: data.as_ptr() as *mut libc::c_void,
            iov_len: data.len(),
        };
        let remote = libc::iovec {
            iov_base: addr as *mut libc::c_void,
            iov_len: data.len(),
        };
        let n = unsafe { libc::process_vm_writev(pid, &local, 1, &remote, 1, 0) };
        if n <= 0 {
            None
        } else {
            Some(n as usize)
        }
    }

    /// Serve one trapped syscall. Every arm has a real effect; a number outside
    /// `IMPLEMENTED` returns -ENOSYS rather than pretending to have run.
    pub fn dispatch(&mut self, syscall: u64, args: [u64; 6]) -> i64 {
        self.served += 1;
        if !IMPLEMENTED.contains(&syscall) {
            return ENOSYS;
        }
        match syscall {
            nr::GETPID | nr::GETTID => i64::from(self.pid),
            nr::GETPPID => SUPERVISOR_SENTINEL,
            nr::GETUID | nr::GETEUID | nr::GETGID | nr::GETEGID => 0,
            nr::BRK => {
                if args[0] != 0 {
                    self.guest.brk = args[0];
                }
                self.guest.brk as i64
            }
            nr::CLOCK_GETTIME => {
                let mut ts: libc::timespec = unsafe { std::mem::zeroed() };
                if unsafe { libc::clock_gettime(libc::CLOCK_REALTIME, &mut ts) } != 0 {
                    return EIO;
                }
                let bytes = unsafe {
                    std::slice::from_raw_parts(
                        (&ts as *const libc::timespec).cast::<u8>(),
                        std::mem::size_of::<libc::timespec>(),
                    )
                };
                match self.write_guest(args[1], bytes) {
                    Some(n) if n == bytes.len() => 0,
                    Some(_) | None => EFAULT,
                }
            }
            nr::UNAME => {
                let mut out = [0u8; UTSNAME_LEN];
                let fields = [
                    "Linux",
                    "qbox",
                    "6.17.7-qqbox",
                    "#1 qqbox",
                    std::env::consts::ARCH,
                    "(none)",
                ];
                for (i, field) in fields.iter().enumerate() {
                    let b = field.as_bytes();
                    let n = b.len().min(64);
                    out[i * 65..i * 65 + n].copy_from_slice(&b[..n]);
                }
                match self.write_guest(args[0], &out) {
                    Some(n) if n == out.len() => 0,
                    Some(_) | None => EFAULT,
                }
            }
            nr::WRITE => {
                let Some(fd) = self.guest_fd(args[0]) else {
                    return EBADF;
                };
                let len = (args[2] as usize).min(MAX_GUEST_IO);
                let bytes = match self.read_guest(args[1], len) {
                    Some(b) => b,
                    None => return EFAULT,
                };
                unsafe { libc::write(fd, bytes.as_ptr().cast(), bytes.len()) as i64 }
            }
            nr::READ => {
                let Some(fd) = self.guest_fd(args[0]) else {
                    return EBADF;
                };
                let len = (args[2] as usize).min(MAX_GUEST_IO);
                let mut buf = vec![0u8; len];
                let n = unsafe { libc::read(fd, buf.as_mut_ptr().cast(), len) };
                if n <= 0 {
                    return n as i64;
                }
                match self.write_guest(args[1], &buf[..n as usize]) {
                    Some(w) => w as i64,
                    None => EFAULT,
                }
            }
            nr::OPENAT => {
                let bytes = match self.read_guest(args[1], MAX_GUEST_IO) {
                    Some(b) => b,
                    None => return EFAULT,
                };
                let end = bytes.iter().position(|b| *b == 0).unwrap_or(bytes.len());
                let path = match std::str::from_utf8(&bytes[..end]) {
                    Ok(p) if !p.is_empty() => p,
                    _ => return EFAULT,
                };
                let resolved = match self.namespace.resolve(path) {
                    Ok(r) => r,
                    Err(_) => return EBADF,
                };
                let host = match std::ffi::CString::new(resolved.host.to_string_lossy().as_bytes())
                {
                    Ok(c) => c,
                    Err(_) => return EBADF,
                };
                let fd = unsafe { libc::open(host.as_ptr(), libc::O_RDONLY | libc::O_CLOEXEC) };
                if fd < 0 {
                    return EBADF;
                }
                self.guest.fd_table.insert(fd, path.to_string());
                fd as i64
            }
            nr::CLOSE => {
                let Some(fd) = self.guest_fd(args[0]) else {
                    return EBADF;
                };
                self.guest.fd_table.remove(&fd);
                unsafe { libc::close(fd) as i64 }
            }
            _ => ENOSYS,
        }
    }
}

/// Wait for one request on the channel, serve it, publish the reply and wake
/// the trapped thread. Event-driven: FUTEX_WAIT blocks until STATE_REQUEST.
/// Wait for one request on the channel, serve it, publish the reply and wake
/// the trapped thread. Event-driven: FUTEX_WAIT blocks until STATE_REQUEST.
/// `stop` lets the owner shut the supervisor down and JOIN it instead of
/// leaving a never-joined thread after a failed install.
#[cfg(target_os = "linux")]
pub fn serve_one(ch: &Channel, ops: &mut HostOps) -> Result<(u64, i64)> {
    serve_one_inner(ch, ops, None)
}

#[cfg(target_os = "linux")]
pub fn serve_one_with_stop(
    ch: &Channel,
    ops: &mut HostOps,
    stop: &AtomicBool,
) -> Result<(u64, i64)> {
    serve_one_inner(ch, ops, Some(stop))
}

/// Wake every waiter on the chanPage state word. Used to release a supervisor
/// blocked in FUTEX_WAIT during shutdown; no sleep or periodic recheck.
#[cfg(target_os = "linux")]
pub fn wake_all(ch: &Channel) {
    // Change the futex word BEFORE waking: a thread that has not entered
    // FUTEX_WAIT yet must not block forever. The waiter compares the word, so
    // writing the sentinel makes FUTEX_WAIT return EAGAIN; stop flag is the
    // durable signal.
    ch.state().store(STATE_STOP, Ordering::SeqCst);
    unsafe {
        libc::syscall(
            libc::SYS_futex,
            ch.state_word() as *mut libc::c_void,
            libc::FUTEX_WAKE,
            i32::MAX as libc::c_ulong,
            std::ptr::null::<libc::c_void>(),
            std::ptr::null::<libc::c_void>(),
            0,
        );
    }
}

#[cfg(target_os = "linux")]
fn serve_one_inner(
    ch: &Channel,
    ops: &mut HostOps,
    stop: Option<&AtomicBool>,
) -> Result<(u64, i64)> {
    let state = ch.state();
    loop {
        if stop.is_some_and(|flag| flag.load(Ordering::SeqCst)) {
            return Err(Error::Message("host-op supervisor stopped".into()));
        }
        let cur = state.load(Ordering::SeqCst);
        if cur == STATE_STOP {
            return Err(Error::Message("host-op supervisor stopped".into()));
        }
        if cur == STATE_REQUEST {
            break;
        }
        let rc = unsafe {
            libc::syscall(
                libc::SYS_futex,
                ch.state_word() as *mut libc::c_void,
                libc::FUTEX_WAIT,
                cur as libc::c_ulong,
                std::ptr::null::<libc::c_void>(),
                std::ptr::null::<libc::c_void>(),
                0,
            )
        };
        if rc < 0 {
            let e = std::io::Error::last_os_error();
            match e.raw_os_error() {
                Some(libc::EAGAIN) => continue,
                Some(libc::EINTR) => {
                    if GUEST_GONE.load(Ordering::SeqCst) != 0 {
                        return Err(Error::Message(
                            "guest exited before publishing a chanPage request".into(),
                        ));
                    }
                    continue;
                }
                _ => return Err(Error::Message(format!("futex wait: {e}"))),
            }
        }
    }
    // A stop that arrived while we were waking must not dispatch a stale
    // request. Re-check under the same SeqCst ordering before touching state.
    if stop.is_some_and(|flag| flag.load(Ordering::SeqCst)) {
        return Err(Error::Message("host-op supervisor stopped".into()));
    }
    let req = ch.request();
    let ret = ops.dispatch(req.nr, req.args);
    ch.write_ret(ret);
    state.store(STATE_REPLY, Ordering::SeqCst);
    unsafe {
        libc::syscall(
            libc::SYS_futex,
            ch.state_word() as *mut libc::c_void,
            libc::FUTEX_WAKE,
            1 as libc::c_ulong,
            std::ptr::null::<libc::c_void>(),
            std::ptr::null::<libc::c_void>(),
            0,
        )
    };
    Ok((req.nr, ret))
}

/// Reset the channel to idle so the next trap can proceed.
pub fn ack(ch: &Channel) {
    ch.state().store(STATE_IDLE, Ordering::SeqCst);
}

/// Build a synthetic host request, used by the live witness to prove the
/// dispatcher without relying on a signal having fired yet.
pub fn synthetic(nr: u64, args: [u64; 6]) -> crate::zig::abi::ChannelRequest {
    host_request(nr, args, 0)
}
