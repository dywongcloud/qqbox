// futex.zig — the Linux futex, served in-stub. Every guest thread of one
// process shares this task's address space, so a futex word is an ordinary
// in-process address.
//
// The threads waiting on a futex are queued here, as Linux queues them: a
// waiter checks the word and enters the table under the lock of the word's
// bucket, which a waker takes too, and sleeps on its own table entry. A wake
// takes up to n of the waiters queued at the time off the table, oldest
// first, and wakes each on its entry; requeue moves waiters to another word
// without waking them, and a bitset wake passes over waiters it shares no bit
// with. (Darwin's own wake of one waiter on the guest's word cannot do this:
// a woken thread that waits again can take a later wake meant for another,
// and a wake of all of them does not end while threads keep coming back.)
//
// A futex without FUTEX_PRIVATE_FLAG on a MAP_SHARED page (a process-shared
// mutex in memory a forked child shares) may have waiters in other processes,
// which this table does not hold: it waits and wakes on the guest's word in
// os_sync's shared mode, which matches waiters across processes, with a
// requeue folded into a wake and the bitset not looked at.

const std = @import("std");
const chan = @import("chan.zig");
const mem = @import("mem.zig");
const sig = @import("sig.zig");

const RTLD_DEFAULT: ?*anyopaque = @ptrFromInt(@as(usize, @bitCast(@as(isize, -2))));
extern "c" fn dlsym(handle: ?*anyopaque, symbol: [*:0]const u8) ?*anyopaque;
extern "c" fn __error() *c_int;
extern "c" fn clock_gettime(clk: c_int, ts: *Timespec) c_int;
extern "c" fn __ulock_wake(op: u32, addr: *anyopaque, wake_value: u64) c_int;

const Timespec = extern struct { sec: i64, nsec: i64 };

const WaitFn = *const fn (addr: *anyopaque, value: u64, size: usize, flags: u32, clockid: u32, timeout_ns: u64) callconv(.C) c_int;
const WaitNoTimeoutFn = *const fn (addr: *anyopaque, value: u64, size: usize, flags: u32) callconv(.C) c_int;
const WakeFn = *const fn (addr: *anyopaque, size: usize, flags: u32) callconv(.C) c_int;

var wait_timeout: WaitFn = undefined;
var wait_forever: WaitNoTimeoutFn = undefined;
var wake_any: WakeFn = undefined;
var wake_all: WakeFn = undefined;

const OS_CLOCK_MACH_ABSOLUTE_TIME: u32 = 32;
const DARWIN_CLOCK_REALTIME: c_int = 0;
const DARWIN_CLOCK_MONOTONIC: c_int = 6;
const DARWIN_ETIMEDOUT: c_int = 60;
const DARWIN_EINTR: c_int = 4;

// Linux errnos, returned negated.
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
const FUTEX_BITSET_MATCH_ANY: u32 = 0xffffffff;

// init resolves the os_sync entry points; call once before Seatbelt.
pub fn init() bool {
    wait_timeout = @ptrCast(@alignCast(dlsym(RTLD_DEFAULT, "os_sync_wait_on_address_with_timeout") orelse return false));
    wait_forever = @ptrCast(@alignCast(dlsym(RTLD_DEFAULT, "os_sync_wait_on_address") orelse return false));
    wake_any = @ptrCast(@alignCast(dlsym(RTLD_DEFAULT, "os_sync_wake_by_address_any") orelse return false));
    wake_all = @ptrCast(@alignCast(dlsym(RTLD_DEFAULT, "os_sync_wake_by_address_all") orelse return false));
    return true;
}

fn neg(e: i64) u64 {
    return @bitCast(-e);
}

fn word(addr: u64) *u32 {
    return @ptrFromInt(addr);
}

const OS_SYNC_SHARED: u32 = 1;
// The ulock wait under os_sync_wait_on_address: ulock_wait2(op, addr, value,
// timeout ns or 0, 0), with ULF_NO_ERRNO returning the negated errno.
const SYS_ULOCK_WAIT2: u64 = 544;
const UL_COMPARE_AND_WAIT: u64 = 1;
const UL_COMPARE_AND_WAIT_SHARED: u64 = 3;
const UL_COMPARE_AND_WAIT64: u64 = 5;
const ULF_WAKE_ALL: u64 = 0x100;
const ULF_NO_ERRNO: u64 = 0x01000000;

// waits[s] is the futex word the guest thread in channel slot s waits on, 0
// when it waits on none; masks[s] is its bitset and order[s] when it was
// queued. An entry is set and taken off under its word's bucket lock, and its
// thread sleeps on the entry until it is 0.
var waits: [chan.MAX_SLOTS]u64 = [_]u64{0} ** chan.MAX_SLOTS;
var masks: [chan.MAX_SLOTS]u32 = [_]u32{0} ** chan.MAX_SLOTS;
var order: [chan.MAX_SLOTS]u64 = [_]u64{0} ** chan.MAX_SLOTS;
var next_order: u64 = 0;
var buckets: [64]std.Thread.Mutex = [_]std.Thread.Mutex{.{}} ** 64;

fn bucket(addr: u64) *std.Thread.Mutex {
    return &buckets[@intCast(((addr >> 2) *% 0x9E3779B97F4A7C15) >> 58)];
}

// lock2 takes the bucket locks of two words, in a fixed order; unlock2 drops
// them.
fn lock2(a: u64, b: u64) void {
    const x = bucket(a);
    const y = bucket(b);
    if (x == y) return x.lock();
    if (@intFromPtr(x) < @intFromPtr(y)) {
        x.lock();
        y.lock();
    } else {
        y.lock();
        x.lock();
    }
}

fn unlock2(a: u64, b: u64) void {
    const x = bucket(a);
    const y = bucket(b);
    x.unlock();
    if (x != y) y.unlock();
}

fn queue(i: usize, addr: u64) void {
    order[i] = @atomicRmw(u64, &next_order, .Add, 1, .monotonic);
    @atomicStore(u64, &waits[i], addr, .seq_cst);
}

// oldest is the slot of the longest-queued waiter on addr whose bitset meets
// mask; its bucket lock is held.
fn oldest(addr: u64, mask: u32) ?usize {
    var best: ?usize = null;
    for (0..chan.MAX_SLOTS) |i| {
        if (@atomicLoad(u64, &waits[i], .monotonic) != addr or masks[i] & mask == 0) continue;
        if (best == null or order[i] < order[best.?]) best = i;
    }
    return best;
}

// Woken lists the slots taken off the table, to wake once the lock is dropped.
const Woken = struct {
    slots: [chan.MAX_SLOTS]u16 = undefined,
    n: u32 = 0,

    // take takes up to n waiters on addr whose bitset meets mask off the
    // table, oldest first; addr's bucket lock is held.
    fn take(w: *Woken, addr: u64, n: u32, mask: u32) void {
        var k: u32 = 0;
        while (k < n) : (k += 1) {
            const i = oldest(addr, mask) orelse return;
            @atomicStore(u64, &waits[i], 0, .seq_cst);
            w.slots[w.n] = @intCast(i);
            w.n += 1;
        }
    }

    fn wake(w: *const Woken) void {
        for (w.slots[0..w.n]) |i| _ = __ulock_wake(@truncate(UL_COMPARE_AND_WAIT64 | ULF_NO_ERRNO), &waits[i], 0);
    }
};

// count is how many waiters a wake of val takes: Linux takes the count as a
// signed int and wakes one for any count below one.
fn count(val: u32) u32 {
    return if (@as(i32, @bitCast(val)) < 1) 1 else val;
}

// mode is the os_sync flag for a futex op on addr: shared for a non-private
// op on a MAP_SHARED page, else private (address-keyed, like Linux's private
// futexes and any futex on private memory).
fn mode(op: u32, addr: u64) u32 {
    if (op & FUTEX_PRIVATE_FLAG != 0) return 0;
    return if (mem.isShared(addr)) OS_SYNC_SHARED else 0;
}

// wake releases up to n waiters on addr and returns how many it woke.
pub fn wake(addr: u64, n: u32) u32 {
    if (mem.isShared(addr)) return wakeShared(addr, n);
    return wakeQueued(addr, n, FUTEX_BITSET_MATCH_ANY);
}

fn wakeQueued(addr: u64, n: u32, mask: u32) u32 {
    var w: Woken = .{};
    const b = bucket(addr);
    b.lock();
    w.take(addr, n, mask);
    b.unlock();
    w.wake();
    return w.n;
}

fn wakeShared(addr: u64, n: u32) u32 {
    var woke: u32 = 0;
    while (woke < n) : (woke += 1) {
        if (wake_any(@ptrFromInt(addr), 4, OS_SYNC_SHARED) != 0) break; // ENOENT: no waiters left
    }
    return woke;
}

// wakeAll ends the wait of a thread sleeping on addr: its table entry, or the
// guest's word of a shared futex or the private word of a local wait. It runs
// in a signal handler, so it cannot ask mem whether addr is shared and wakes
// waiters of both modes.
pub fn wakeAll(addr: u64) void {
    if (addr >= @intFromPtr(&waits) and addr < @intFromPtr(&waits) + @sizeOf(@TypeOf(waits))) {
        _ = __ulock_wake(@truncate(UL_COMPARE_AND_WAIT64 | ULF_WAKE_ALL | ULF_NO_ERRNO), @ptrFromInt(addr), 0);
        return;
    }
    _ = wake_all(@ptrFromInt(addr), 4, 0);
    _ = wake_all(@ptrFromInt(addr), 4, OS_SYNC_SHARED);
}

// waitWord sleeps while *w == val, for at most ns (0 = no limit).
pub fn waitWord(w: *u32, val: u32, ns: u64) void {
    if (ns == 0) {
        _ = wait_forever(w, val, 4, 0);
    } else {
        _ = wait_timeout(w, val, 4, 0, OS_CLOCK_MACH_ABSOLUTE_TIME, ns);
    }
}

// nowNs reads CLOCK_MONOTONIC, the clock local deadlines use.
pub fn nowNs() i64 {
    return clockNs(DARWIN_CLOCK_MONOTONIC);
}

fn clockNs(clk: c_int) i64 {
    var ts: Timespec = undefined;
    _ = clock_gettime(clk, &ts);
    return ts.sec * std.time.ns_per_s + ts.nsec;
}

// deadlineOf turns a futex timeout, a guest timespec (relative for FUTEX_WAIT,
// an absolute time on the op's clock for FUTEX_WAIT_BITSET), into a
// CLOCK_MONOTONIC deadline: 0 for none, or a negated errno as an error.
fn deadlineOf(timeout: u64, absolute: bool, clk: c_int) union(enum) { at: i64, err: u64 } {
    if (timeout == 0) return .{ .at = 0 };
    const ts = sig.getTimespec(timeout) orelse return .{ .err = neg(EFAULT) };
    if (ts[0] < 0 or ts[1] < 0 or ts[1] >= std.time.ns_per_s) return .{ .err = neg(EINVAL) };
    var ns = ts[0] *| std.time.ns_per_s +| ts[1];
    if (absolute) ns -|= clockNs(clk);
    return .{ .at = nowNs() +| @max(ns, 0) };
}

// waitQueued blocks while *addr == val, queued until a wake takes it off. A
// signal with a handler ends the wait with ERESTARTSYS; one without (ignored,
// stopping) is acted on and the wait goes on to the same deadline, which is
// what Linux's restart of an interrupted wait amounts to.
fn waitQueued(addr: u64, val: u32, deadline: i64, mask: u32) u64 {
    const me = sig.mySlot();
    const b = bucket(addr);
    b.lock();
    const now = sig.load32(addr);
    if (now != val) {
        b.unlock();
        return if (now == null) neg(EFAULT) else neg(EAGAIN);
    }
    masks[me] = mask;
    queue(me, addr);
    b.unlock();
    // A signal for this thread ends the wait: its kick wakes the entry.
    sig.beginFutexWait(@intFromPtr(&waits[me]));
    defer sig.endFutexWait();
    while (true) {
        sig.clearKick(); // answered by the look for signals next
        const cur = @atomicLoad(u64, &waits[me], .seq_cst);
        if (cur == 0) return 0;
        if (sig.interrupted() and sig.settle()) return leave(me, neg(ERESTARTSYS));
        var ns: u64 = 0; // no limit
        if (deadline != 0) {
            const left = deadline - nowNs();
            if (left <= 0) return leave(me, neg(ETIMEDOUT));
            ns = @intCast(@min(left, 3600 * std.time.ns_per_s));
        }
        // The ulock call os_sync makes, issued through sig.cpSys so that a
        // kick landing as it starts still ends it.
        const r = sig.cpSys(SYS_ULOCK_WAIT2, .{ UL_COMPARE_AND_WAIT64 | ULF_NO_ERRNO, @intFromPtr(&waits[me]), cur, ns, 0, 0 });
        if (r < 0 and -r != DARWIN_ETIMEDOUT and -r != DARWIN_EINTR) return leave(me, neg(EFAULT));
    }
}

// leave takes this thread off the table and returns ret, or 0 if a wake took
// it off first: that wake counted it.
fn leave(me: usize, ret: u64) u64 {
    while (true) {
        const cur = @atomicLoad(u64, &waits[me], .seq_cst);
        if (cur == 0) return 0;
        const b = bucket(cur);
        b.lock();
        defer b.unlock();
        if (@atomicLoad(u64, &waits[me], .seq_cst) == cur) { // not requeued meanwhile
            @atomicStore(u64, &waits[me], 0, .seq_cst);
            return ret;
        }
    }
}

// waitShared blocks while the shared futex word *addr == val, sleeping on the
// word in os_sync's shared mode.
fn waitShared(addr: u64, val: u32, deadline: i64) u64 {
    if ((sig.load32(addr) orelse return neg(EFAULT)) != val) return neg(EAGAIN);
    sig.beginFutexWait(addr);
    defer sig.endFutexWait();
    while (true) {
        sig.clearKick();
        if (sig.interrupted() and sig.settle()) return neg(ERESTARTSYS);
        // A word unmapped meanwhile reads as unchanged: the waiter stays,
        // as Linux's does, until a wake or the deadline.
        if ((sig.load32(addr) orelse val) != val) return 0;
        var ns: u64 = 0;
        if (deadline != 0) {
            const left = deadline - nowNs();
            if (left <= 0) return neg(ETIMEDOUT);
            ns = @intCast(@min(left, 3600 * std.time.ns_per_s));
        }
        const r = sig.cpSys(SYS_ULOCK_WAIT2, .{ UL_COMPARE_AND_WAIT_SHARED | ULF_NO_ERRNO, addr, val, ns, 0, 0 });
        if (r >= 0) return 0;
        if (-r != DARWIN_ETIMEDOUT and -r != DARWIN_EINTR) return neg(EFAULT);
    }
}

fn wait(addr: u64, val: u32, timeout: u64, absolute: bool, clk: c_int, mask: u32, shared: bool) u64 {
    if (addr & 3 != 0 or mask == 0) return neg(EINVAL);
    const deadline = switch (deadlineOf(timeout, absolute, clk)) {
        .at => |at| at,
        .err => |e| return e,
    };
    if (shared) return waitShared(addr, val, deadline);
    return waitQueued(addr, val, deadline, mask);
}

// requeue wakes up to nwake waiters on uaddr and moves up to nreq more to
// uaddr2, after checking *uaddr against cmp for FUTEX_CMP_REQUEUE; it returns
// how many it woke and moved.
fn requeue(uaddr: u64, nwake: u32, nreq: u32, uaddr2: u64, cmp: ?u32) u64 {
    if (@as(i32, @bitCast(nwake)) < 0 or @as(i32, @bitCast(nreq)) < 0) return neg(EINVAL);
    var w: Woken = .{};
    var moved: u32 = 0;
    lock2(uaddr, uaddr2);
    if (cmp) |c| {
        const now = sig.load32(uaddr);
        if (now != c) {
            unlock2(uaddr, uaddr2);
            return if (now == null) neg(EFAULT) else neg(EAGAIN);
        }
    }
    w.take(uaddr, nwake, FUTEX_BITSET_MATCH_ANY);
    while (moved < nreq) : (moved += 1) {
        const i = oldest(uaddr, FUTEX_BITSET_MATCH_ANY) orelse break;
        queue(i, uaddr2); // queued anew, behind uaddr2's waiters
    }
    unlock2(uaddr, uaddr2);
    w.wake();
    return w.n + moved;
}

// sleepNs blocks the calling thread for ns: a timed wait on a private word
// nobody wakes, so it needs no syscall beyond the os_sync ones the channel
// already uses. Early returns resume the wait until the deadline passes.
pub fn sleepNs(ns: u64) void {
    var w: u32 = 0;
    const deadline = nowNs() + @as(i64, @intCast(ns));
    while (true) {
        const left = deadline - nowNs();
        if (left <= 0) return;
        _ = wait_timeout(&w, 0, 4, 0, OS_CLOCK_MACH_ABSOLUTE_TIME, @intCast(left));
    }
}

// wakeOp implements FUTEX_WAKE_OP: atomically apply the encoded op to *uaddr2,
// wake n on uaddr, and wake n2 on uaddr2 if the old *uaddr2 passes the compare.
fn wakeOp(uaddr: u64, n: u32, n2: u32, uaddr2: u64, enc: u32, shared: bool) u64 {
    const op = (enc >> 28) & 7;
    const cmp = (enc >> 24) & 15;
    var oparg: u32 = (enc >> 12) & 0xfff;
    const cmparg: i32 = @intCast(enc & 0xfff);
    if ((enc >> 31) != 0) oparg = @as(u32, 1) << @intCast(oparg & 31); // FUTEX_OP_OPARG_SHIFT
    if (op > 4 or cmp > 5) return neg(ENOSYS);
    if (uaddr2 & 3 != 0) return neg(EINVAL);
    if (sig.load32(uaddr2) == null) return neg(EFAULT);
    if (!shared) lock2(uaddr, uaddr2);
    const p = word(uaddr2);
    var old = @atomicLoad(u32, p, .seq_cst);
    while (true) {
        const new = switch (op) {
            0 => oparg,
            1 => old +% oparg,
            2 => old | oparg,
            3 => old & ~oparg,
            else => old ^ oparg,
        };
        old = @cmpxchgWeak(u32, p, old, new, .seq_cst, .seq_cst) orelse break;
    }
    const o: i32 = @bitCast(old);
    const hit = switch (cmp) {
        0 => o == cmparg,
        1 => o != cmparg,
        2 => o < cmparg,
        3 => o <= cmparg,
        4 => o > cmparg,
        else => o >= cmparg,
    };
    if (shared) {
        var woke = wakeShared(uaddr, count(n));
        if (hit) woke += wakeShared(uaddr2, count(n2));
        return woke;
    }
    var w: Woken = .{};
    w.take(uaddr, count(n), FUTEX_BITSET_MATCH_ANY);
    if (hit) w.take(uaddr2, count(n2), FUTEX_BITSET_MATCH_ANY);
    unlock2(uaddr, uaddr2);
    w.wake();
    return w.n;
}

// handle serves futex(uaddr, op, val, timeout|val2, uaddr2, val3) and returns
// the Linux result (negative errno on failure).
pub fn handle(a: [6]u64) u64 {
    const uaddr = a[0];
    const op: u32 = @truncate(a[1]);
    const val: u32 = @truncate(a[2]);
    const val2: u32 = @truncate(a[3]);
    const val3: u32 = @truncate(a[5]);
    const clk = if (op & FUTEX_CLOCK_REALTIME != 0) DARWIN_CLOCK_REALTIME else DARWIN_CLOCK_MONOTONIC;
    const shared = mode(op, uaddr) != 0;
    return switch (op & ~@as(u32, FUTEX_PRIVATE_FLAG | FUTEX_CLOCK_REALTIME)) {
        FUTEX_WAIT => wait(uaddr, val, a[3], false, clk, FUTEX_BITSET_MATCH_ANY, shared),
        FUTEX_WAIT_BITSET => wait(uaddr, val, a[3], true, clk, val3, shared),
        FUTEX_WAKE => if (shared) wakeShared(uaddr, count(val)) else wakeQueued(uaddr, count(val), FUTEX_BITSET_MATCH_ANY),
        FUTEX_WAKE_BITSET => if (val3 == 0)
            neg(EINVAL)
        else if (shared)
            wakeShared(uaddr, count(val))
        else
            wakeQueued(uaddr, count(val), val3),
        FUTEX_REQUEUE, FUTEX_CMP_REQUEUE => blk: {
            const cmp: ?u32 = if ((op & 0x7f) == FUTEX_CMP_REQUEUE) val3 else null;
            if (!shared and mode(op, a[4]) == 0) break :blk requeue(uaddr, val, val2, a[4], cmp);
            if (cmp) |c| if ((sig.load32(uaddr) orelse break :blk neg(EFAULT)) != c) break :blk neg(EAGAIN);
            break :blk wakeShared(uaddr, val +| val2);
        },
        FUTEX_WAKE_OP => wakeOp(uaddr, val, val2, a[4], val3, shared),
        else => neg(ENOSYS),
    };
}
