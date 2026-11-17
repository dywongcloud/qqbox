// stdio.zig — the guest's stdio fast path. While a guest fd 0-2 still refers
// to the stub's own inherited stdio (in blocking mode), read/write/readv/writev
// on it are one native syscall here, with no supervisor round trip: the buffer
// is already in this address space. The supervisor publishes which fds qualify
// in the slot's stdio word (sandbox/stdio_darwin.go): bit i for write on fd i,
// bit 8+i for read.

const std = @import("std");
const sig = @import("sig.zig");
const chan = @import("chan.zig");

const NR_READ: u64 = 63;
const NR_WRITE: u64 = 64;
const NR_READV: u64 = 65;
const NR_WRITEV: u64 = 66;

const SYS_read: u64 = 3;
const SYS_write: u64 = 4;
const SYS_readv: u64 = 120;
const SYS_writev: u64 = 121;

const SIGTTIN: c_int = 21;
const SIGTTOU: c_int = 22;
const SIG_IGN: usize = 1;

extern "c" fn signal(sig: c_int, handler: usize) usize;

// init runs before Seatbelt. The stub is in a background process group of the
// terminal: with TOSTOP set a write would stop it, and a stray read too, where
// ignoring the signals makes the write go through and the read fail with EIO.
pub fn init() void {
    _ = signal(SIGTTOU, SIG_IGN);
    _ = signal(SIGTTIN, SIG_IGN);
}

pub fn handle(ch: *chan.Chan, nr: u64, a: [*]const u64) ?u64 {
    const fd = a[0];
    if (fd > 2) return null;
    const mask = @atomicLoad(u64, &ch.stdio, .monotonic);
    const sys: u64 = switch (nr) {
        NR_WRITE => if (mask >> @intCast(fd) & 1 != 0) SYS_write else return null,
        NR_WRITEV => if (mask >> @intCast(fd) & 1 != 0) SYS_writev else return null,
        NR_READ => if (mask >> @intCast(8 + fd) & 1 != 0) SYS_read else return null,
        NR_READV => if (mask >> @intCast(8 + fd) & 1 != 0) SYS_readv else return null,
        else => return null,
    };
    // It may block (a pipe), so a signal must be able to cut it short.
    return linuxRet(sig.cpSys(sys, .{ fd, a[1], a[2], 0, 0, 0 }));
}

// linuxRet maps the Darwin errnos these calls can return to Linux's; the ones
// below 35 are the same on both. A host EINTR (a guest signal kicked this
// thread) becomes ERESTARTSYS, which sig.afterSyscall restarts or fails with
// EINTR as the handler's SA_RESTART says.
fn linuxRet(r: i64) u64 {
    if (r == -4) return @bitCast(@as(i64, -512));
    if (r >= 0 or r > -35) return @bitCast(r);
    const e: i64 = switch (-r) {
        35 => 11, // EAGAIN
        54 => 104, // ECONNRESET
        57 => 107, // ENOTCONN
        60 => 110, // ETIMEDOUT
        69 => 122, // EDQUOT
        else => 5, // EIO
    };
    return @bitCast(-e);
}
