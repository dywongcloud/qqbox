const stub = @import("stub.zig");
const arch = stub.arch;
const sig = stub.sig;

pub const State = extern struct {
    kind: u64,
    dirty: u64,
    regs: arch.Registers,
};

comptime {
    if (@offsetOf(State, "regs") != 16 or @sizeOf(State) > 4096 - 256)
        @compileError("trace register layout");
}

pub fn state() *State {
    return @ptrFromInt(@intFromPtr(sig.rec(sig.thr())) + 256);
}

pub fn frame(f: *const arch.Frame, pc: u64, sp: u64) void {
    state().* = .{ .kind = 1, .dirty = 0, .regs = arch.frameRegs(f, pc, sp) };
}

pub fn capture(uc: [*]u8) void {
    state().* = .{ .kind = 2, .dirty = 0, .regs = arch.saveRegs(uc) };
}

// inCall records, for a stop that follows a syscall, the number of the call
// it is in, which x86-64 keeps in orig_rax until the call is done or
// restarted (arm64's register set has no such field).
pub fn inCall(nr: u64) void {
    if (@hasField(arch.Registers, "orig_rax")) state().regs.orig_rax = nr;
}

pub fn restore(uc: [*]u8) void {
    const s = state();
    if (s.dirty != 0) {
        arch.restoreRegs(uc, &s.regs);
        s.dirty = 0;
    }
}

pub fn enter() noreturn {
    sig.thr().frame = 0;
    arch.trap();
}

export fn lc_trace_child() callconv(.C) void {
    if (state().dirty != 0) enter();
}

pub fn result(r: u64) void {
    const s = state();
    if (s.kind == 1) {
        if (@hasField(arch.Registers, "rax")) s.regs.rax = r else s.regs.x[0] = r;
    }
}
