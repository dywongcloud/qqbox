// futex.zig — the Linux futex, served in the Windows stub. Every guest thread
// of one process shares this address space, so a futex word is an ordinary
// in-process address (M3 is single-process; shared cross-process futexes come
// with fork in M4). Each waiter parks in an alertable wait on its own auto-reset
// event (sig.park), so a signal APC can break it; a waker sets the events of the
// threads parked on the address (sig.futexWake). WaitOnAddress is not used: a
// cross-process APC cannot break it (probes §6).
//
// Requeue is folded into a wake: the uaddr2 waiters that would move are woken
// instead and re-check their condition (futex callers tolerate spurious wakeups).

const std = @import("std");
const sig = @import("sig.zig");
const stub = @import("stub.zig");

// LC_FUTEX hands a futex call on shared memory to the supervisor, which keys
// its waiters by the view's section and offset across processes: (uaddr, op,
// val, timeout, key, offset).
const LC_FUTEX: u64 = 0x4c430026;

const EAGAIN: i64 = 11;
const EFAULT: i64 = 14;
const EINVAL: i64 = 22;
const ENOSYS: i64 = 38;
const ETIMEDOUT: i64 = 110;
const ERESTARTSYS: i64 = 512;

const FUTEX_WAIT = 0;
const FUTEX_WAKE = 1;
const FUTEX_REQUEUE = 3;
const FUTEX_CMP_REQUEUE = 4;
const FUTEX_WAKE_OP = 5;
const FUTEX_WAIT_BITSET = 9;
const FUTEX_WAKE_BITSET = 10;
const FUTEX_PRIVATE_FLAG = 128;
const FUTEX_CLOCK_REALTIME = 256;

fn neg(e: i64) u64 {
    return @bitCast(-e);
}

fn word(addr: u64) *u32 {
    return @ptrFromInt(addr);
}

// wait blocks while *addr == val. timeout is a guest timespec: relative for
// FUTEX_WAIT, an absolute deadline on the op's clock for FUTEX_WAIT_BITSET. A
// signal with a handler ends the wait with ERESTARTSYS; one without is acted on
// and the wait goes on to the same deadline.
fn wait(addr: u64, val: u32, timeout: u64, absolute: bool) u64 {
    if (addr & 3 != 0) return neg(EINVAL);
    if ((stub.arch.guestLoad32(addr) orelse return neg(EFAULT)) != val) return neg(EAGAIN);
    var deadline: i64 = 0;
    if (timeout != 0) {
        const ts = sig.get([2]i64, timeout) orelse return neg(EFAULT);
        var ns = ts[0] *| std.time.ns_per_s +| ts[1];
        if (absolute) ns -|= sig.nowNs();
        if (ns <= 0) return neg(ETIMEDOUT);
        deadline = sig.nowNs() +| ns;
    }
    sig.beginFutexWait(addr);
    defer sig.endFutexWait();
    while (true) {
        if (!sig.futexQueued()) return 0; // a wake took this thread off the queue
        if (sig.interrupted() and sig.settle()) return neg(ERESTARTSYS);
        // A word unmapped meanwhile reads as unchanged: the waiter stays
        // queued, as Linux's does, until a wake or the deadline.
        if ((stub.arch.guestLoad32(addr) orelse val) != val) return 0;
        if (deadline != 0 and sig.nowNs() >= deadline) return neg(ETIMEDOUT);
        sig.park(deadline);
    }
}

// wakeOp implements FUTEX_WAKE_OP: apply the encoded op to *uaddr2, wake n on
// uaddr, and wake n2 on uaddr2 if the old *uaddr2 passes the compare.
fn wakeOp(uaddr: u64, n: u32, n2: u32, uaddr2: u64, enc: u32) u64 {
    const op = (enc >> 28) & 7;
    const cmp = (enc >> 24) & 15;
    var oparg: u32 = (enc >> 12) & 0xfff;
    const cmparg: i32 = @intCast(enc & 0xfff);
    if ((enc >> 31) != 0) oparg = @as(u32, 1) << @intCast(oparg & 31);
    if (uaddr2 & 3 != 0) return neg(EINVAL);
    if (stub.arch.guestLoad32(uaddr2) == null) return neg(EFAULT);
    const p = word(uaddr2);
    var old = @atomicLoad(u32, p, .seq_cst);
    while (true) {
        const new = switch (op) {
            0 => oparg,
            1 => old +% oparg,
            2 => old | oparg,
            3 => old & ~oparg,
            4 => old ^ oparg,
            else => return neg(ENOSYS),
        };
        old = @cmpxchgWeak(u32, p, old, new, .seq_cst, .seq_cst) orelse break;
    }
    var woke = sig.futexWake(uaddr, n);
    const o: i32 = @bitCast(old);
    const hit = switch (cmp) {
        0 => o == cmparg,
        1 => o != cmparg,
        2 => o < cmparg,
        3 => o <= cmparg,
        4 => o > cmparg,
        5 => o >= cmparg,
        else => return neg(ENOSYS),
    };
    if (hit) woke += sig.futexWake(uaddr2, n2);
    return woke;
}

// handle serves futex(uaddr, op, val, timeout|val2, uaddr2, val3).
pub fn handle(a: [6]u64) u64 {
    const uaddr = a[0];
    const op: u32 = @truncate(a[1]);
    const val: u32 = @truncate(a[2]);
    const val2: u32 = @truncate(a[3]);
    if (op & FUTEX_PRIVATE_FLAG == 0) {
        if (stub.mem.sharedKey(uaddr)) |k| return stub.request(LC_FUTEX, .{ uaddr, a[1], a[2], a[3], k[0], k[1] });
    }
    return switch (op & ~@as(u32, FUTEX_PRIVATE_FLAG | FUTEX_CLOCK_REALTIME)) {
        FUTEX_WAIT => wait(uaddr, val, a[3], false),
        FUTEX_WAIT_BITSET => wait(uaddr, val, a[3], true),
        FUTEX_WAKE, FUTEX_WAKE_BITSET => sig.futexWake(uaddr, val),
        FUTEX_REQUEUE => sig.futexWake(uaddr, val +| val2),
        FUTEX_CMP_REQUEUE => if ((stub.arch.guestLoad32(uaddr) orelse return neg(EFAULT)) != @as(u32, @truncate(a[5])))
            neg(EAGAIN)
        else
            sig.futexWake(uaddr, val +| val2),
        FUTEX_WAKE_OP => wakeOp(uaddr, val, val2, a[4], @truncate(a[5])),
        else => neg(ENOSYS),
    };
}
