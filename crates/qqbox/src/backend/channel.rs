//! The chanPage: a 128-byte shared request/response page plus a futex state
//! word. This is the ABI the committed freestanding Zig stubs expect (see
//! `crate::zig::abi`); the offsets are asserted there, not redefined here.
//!
//! On Linux the page lives in a `memfd_create` file mapped `MAP_SHARED`, so the
//! guest stub and the Rust supervisor see the same bytes. The Go backend used
//! inherited fds 3/4/5 (channel / lifeline / socket); fd 3 is the channel.

use crate::error::{Error, Result};
use crate::zig::abi::{
    ChannelRequest, ChannelResponse, CHAN_SIZE, MSG_SYSCALL, OFF_RET, OFF_STATE,
};
use std::sync::atomic::{AtomicU32, Ordering};

/// The Zig stub uses the chanPage's own `state` word (offset 8) as the futex
/// word: ST_REQ=1, ST_REP=2, ST_DEAD=3. Keep that ABI exactly.
pub const STATE_OFF: usize = OFF_STATE;
pub const SLOT_BYTES: usize = crate::zig::abi::SLOT_SIZE;

pub const STATE_IDLE: u32 = 0;
pub const STATE_REQUEST: u32 = 1;
pub const STATE_REPLY: u32 = 2;
pub const STATE_DONE: u32 = 3;

/// A shared-memory channel. `base` is page-aligned and `SLOT_BYTES` long.
pub struct Channel {
    base: *mut u8,
    fd: i32,
    owns_fd: bool,
}

unsafe impl Send for Channel {}
unsafe impl Sync for Channel {}

impl Channel {
    /// Create a real memfd-backed shared channel (Linux).
    #[cfg(target_os = "linux")]
    pub fn create() -> Result<Self> {
        let name = b"qqbox-chan\0";
        let fd = unsafe { libc::memfd_create(name.as_ptr().cast(), libc::MFD_CLOEXEC) };
        if fd < 0 {
            return Err(Error::Message(format!(
                "memfd_create: {}",
                std::io::Error::last_os_error()
            )));
        }
        if unsafe { libc::ftruncate(fd, SLOT_BYTES as libc::off_t) } != 0 {
            let e = std::io::Error::last_os_error();
            unsafe { libc::close(fd) };
            return Err(Error::Message(format!("ftruncate channel: {e}")));
        }
        let base = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                SLOT_BYTES,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_SHARED,
                fd,
                0,
            )
        };
        if base == libc::MAP_FAILED {
            let e = std::io::Error::last_os_error();
            unsafe { libc::close(fd) };
            return Err(Error::Message(format!("mmap channel: {e}")));
        }
        Ok(Self {
            base: base.cast(),
            fd,
            owns_fd: true,
        })
    }

    #[cfg(not(target_os = "linux"))]
    pub fn create() -> Result<Self> {
        Err(Error::UnsupportedPlatform(
            "memfd channel is Linux-only; macOS uses Mach VM and Windows a section".into(),
        ))
    }

    /// Map an existing inherited channel fd (the fd 3 contract).
    #[cfg(target_os = "linux")]
    pub fn from_fd(fd: i32) -> Result<Self> {
        let base = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                SLOT_BYTES,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_SHARED,
                fd,
                0,
            )
        };
        if base == libc::MAP_FAILED {
            return Err(Error::Message(format!(
                "mmap inherited channel: {}",
                std::io::Error::last_os_error()
            )));
        }
        Ok(Self {
            base: base.cast(),
            fd,
            owns_fd: false,
        })
    }

    pub fn fd(&self) -> i32 {
        self.fd
    }

    pub fn base(&self) -> *mut u8 {
        self.base
    }

    pub fn state(&self) -> &AtomicU32 {
        // The state word is 4-byte aligned: SCRATCH_OFF is 128.
        unsafe { &*self.base.add(STATE_OFF).cast::<AtomicU32>() }
    }

    /// Raw futex word, for `SYS_futex`.
    pub fn state_word(&self) -> *mut u32 {
        unsafe { self.base.add(STATE_OFF).cast::<u32>() }
    }

    /// Write a request into the shared page. Only called by the trap handler or
    /// the launcher, never concurrently with a reply.
    pub fn publish(&self, req: &ChannelRequest) {
        unsafe {
            std::ptr::write_volatile(self.base.cast::<ChannelRequest>(), *req);
        }
    }

    /// Read the request currently in the shared page.
    pub fn request(&self) -> ChannelRequest {
        unsafe { std::ptr::read_volatile(self.base.cast::<ChannelRequest>()) }
    }

    /// Write the supervisor's return value at OFF_RET, which is the `ret`
    /// field of both ChannelRequest and ChannelResponse.
    pub fn write_ret(&self, ret: i64) {
        unsafe {
            std::ptr::write_volatile(self.base.add(OFF_RET).cast::<i64>(), ret);
        }
    }

    pub fn read_ret(&self) -> i64 {
        unsafe { std::ptr::read_volatile(self.base.add(OFF_RET).cast::<i64>()) }
    }

    /// The Zig stub's response shape, for callers that need it.
    pub fn response(&self) -> ChannelResponse {
        unsafe { std::ptr::read_volatile(self.base.cast::<ChannelResponse>()) }
    }

    pub fn reset(&self) {
        self.state().store(STATE_IDLE, Ordering::SeqCst);
        unsafe { std::ptr::write_bytes(self.base, 0, CHAN_SIZE) };
    }
}

/// A non-owning view over an existing chanPage base. Used by the signal
/// handler, which cannot allocate or call into owning types.
pub struct ChannelView {
    base: *mut u8,
}

impl ChannelView {
    pub fn new(base: *mut u8) -> Self {
        Self { base }
    }

    pub fn publish(&self, req: &ChannelRequest) {
        unsafe {
            std::ptr::write_volatile(self.base.cast::<ChannelRequest>(), *req);
        }
    }

    pub fn state_ptr(&self) -> *mut AtomicU32 {
        unsafe { self.base.add(STATE_OFF).cast::<AtomicU32>() }
    }

    pub fn read_ret(&self) -> i64 {
        unsafe { std::ptr::read_volatile(self.base.add(OFF_RET).cast::<i64>()) }
    }
}

impl Channel {
    /// A non-owning view of this channel, for the trap handler.
    pub fn view(&self) -> ChannelView {
        ChannelView::new(self.base)
    }

    /// Reset the state word after a completed request/reply cycle.
    pub fn complete(&self) {
        self.state()
            .store(crate::backend::channel::STATE_IDLE, Ordering::SeqCst);
    }
}

impl Drop for Channel {
    fn drop(&mut self) {
        unsafe {
            libc::munmap(self.base.cast(), SLOT_BYTES);
            if self.owns_fd {
                libc::close(self.fd);
            }
        }
    }
}

/// A request built from a trapped syscall.
pub fn host_request(nr: u64, args: [u64; 6], rip: u64) -> ChannelRequest {
    let mut req = ChannelRequest::new(0);
    req.kind = MSG_SYSCALL;
    req.nr = nr;
    req.args = args;
    req.rip = rip;
    req.guest_rip = rip;
    req.alive = 1;
    req
}
