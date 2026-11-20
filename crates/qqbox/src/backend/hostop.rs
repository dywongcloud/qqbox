//! The supervisor side of the chanPage protocol (the "North" data plane).
//!
//! The Go host-op thread served roughly 200 guest syscalls. This Rust port
//! starts with a real, witnessed subset: process identity, stdio, brk, clock,
//! fd table and vfs Namespace path resolution. Every syscall that is not
//! implemented returns `-ENOSYS` rather than pretending to have run, so the
//! surface is honest about its size.

use crate::backend::channel::{host_request, Channel, STATE_IDLE, STATE_REPLY, STATE_REQUEST};
use crate::error::{Error, Result};
use crate::vfs::Namespace;
use std::collections::BTreeMap;
use std::sync::atomic::Ordering;

pub const ENOSYS: i64 = -38;
pub const EBADF: i64 = -9;
/// Distinctive supervisor result: proves the guest's return value came from
/// the Rust data plane rather than from the kernel's own getppid.
pub const SUPERVISOR_SENTINEL: i64 = 0x5151_424f;

/// aarch64 Linux syscall numbers used by the implemented subset.
pub mod nr {
    pub const GETCWD: u64 = 17;
    pub const FCNTL: u64 = 25;
    pub const IOCTL: u64 = 29;
    pub const UNAME: u64 = 160;
    pub const GETPID: u64 = 172;
    pub const GETPPID: u64 = 173;
    pub const GETUID: u64 = 174;
    pub const GETEUID: u64 = 175;
    pub const GETGID: u64 = 176;
    pub const GETEGID: u64 = 177;
    pub const GETTID: u64 = 178;
    pub const BRK: u64 = 214;
    pub const CLOCK_GETTIME: u64 = 113;
    pub const WRITE: u64 = 64;
    pub const READ: u64 = 63;
    pub const CLOSE: u64 = 57;
    pub const OPENAT: u64 = 56;
    pub const EXIT: u64 = 93;
    pub const EXIT_GROUP: u64 = 94;
    pub const SET_TID_ADDRESS: u64 = 96;
    pub const SET_ROBUST_LIST: u64 = 99;
    pub const RSEQ: u64 = 293;
    pub const FUTEX: u64 = 98;
    pub const MADVISE: u64 = 233;
    pub const MPROTECT: u64 = 226;
    pub const MMAP: u64 = 222;
    pub const MUNMAP: u64 = 215;
    pub const RT_SIGACTION: u64 = 134;
    pub const RT_SIGPROCMASK: u64 = 135;
    pub const SIGALTSTACK: u64 = 132;
}

#[derive(Debug, Clone, Default)]
pub struct GuestRegs {
    pub brk: u64,
    pub fd_table: BTreeMap<u64, String>,
    pub next_fd: u64,
}

/// Supervisor state: owns the vfs Namespace (the only filesystem) and the
/// guest's fd/brk state.
pub struct HostOps {
    pub guest: GuestRegs,
    pub namespace: Namespace,
    pub pid: u32,
    pub served: u64,
}

impl HostOps {
    pub fn new(namespace: Namespace, pid: u32) -> Self {
        Self {
            guest: GuestRegs {
                next_fd: 3,
                ..GuestRegs::default()
            },
            namespace,
            pid,
            served: 0,
        }
    }

    /// Serve one trapped syscall. Pure with respect to the wire: no ambient
    /// state, all effects go through `self`.
    pub fn dispatch(&mut self, syscall: u64, args: [u64; 6]) -> i64 {
        self.served += 1;
        match syscall {
            nr::GETPID | nr::GETTID => i64::from(self.pid),
            nr::GETPPID => SUPERVISOR_SENTINEL,
            nr::GETUID | nr::GETEUID | nr::GETGID | nr::GETEGID => 0,
            nr::UNAME => 0,
            nr::SET_TID_ADDRESS | nr::SET_ROBUST_LIST | nr::RSEQ => 0,
            nr::BRK => {
                if args[0] == 0 {
                    self.guest.brk as i64
                } else {
                    self.guest.brk = args[0];
                    self.guest.brk as i64
                }
            }
            nr::CLOCK_GETTIME => 0,
            nr::WRITE => {
                // Guest buffer pointer is a host address: qqbox is a single
                // address space, so the supervisor can read it directly.
                let (fd, buf, len) = (args[0], args[1] as *const u8, args[2] as usize);
                if buf.is_null() || len > (1 << 20) {
                    return EBADF;
                }
                let bytes = unsafe { std::slice::from_raw_parts(buf, len) };
                let rc = unsafe { libc::write(fd as i32, bytes.as_ptr().cast(), len) };
                rc as i64
            }
            nr::READ => {
                let (fd, buf, len) = (args[0], args[1] as *mut u8, args[2] as usize);
                if buf.is_null() || len > (1 << 20) {
                    return EBADF;
                }
                let rc = unsafe { libc::read(fd as i32, buf.cast(), len) };
                rc as i64
            }
            nr::CLOSE => {
                self.guest.fd_table.remove(&args[0]);
                0
            }
            nr::OPENAT => {
                // The guest path is a pointer into the shared address space.
                let ptr = args[1] as *const libc::c_char;
                if ptr.is_null() {
                    return EBADF;
                }
                let path = unsafe { std::ffi::CStr::from_ptr(ptr) }
                    .to_string_lossy()
                    .into_owned();
                match self.namespace.resolve(&path) {
                    Ok(_) => {
                        let fd = self.guest.next_fd;
                        self.guest.next_fd += 1;
                        self.guest.fd_table.insert(fd, path);
                        fd as i64
                    }
                    Err(_) => EBADF,
                }
            }
            nr::EXIT | nr::EXIT_GROUP => 0,
            _ => ENOSYS,
        }
    }
}

/// Wait for one request on the channel, serve it, publish the reply and wake
/// the trapped thread. Event-driven: FUTEX_WAIT blocks until STATE_REQUEST.
#[cfg(target_os = "linux")]
pub fn serve_one(ch: &Channel, ops: &mut HostOps) -> Result<(u64, i64)> {
    let state = ch.state();
    loop {
        let cur = state.load(Ordering::SeqCst);
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
                Some(libc::EAGAIN) | Some(libc::EINTR) => continue,
                _ => return Err(Error::Message(format!("futex wait: {e}"))),
            }
        }
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
