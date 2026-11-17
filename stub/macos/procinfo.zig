// procinfo.zig — where this process's guest image lives, for the supervisor's
// /proc/<pid>/{maps,auxv}. The stub maps the program, its interpreter and the
// initial stack itself, so only it knows their ranges. Each stub publishes the
// record's address in every channel slot it serves (Chan.pinfo): at program
// entry, in a fork child and in a new thread, since an exec may run in a fresh
// stub whose image sits elsewhere. The supervisor peeks the record through the
// process's own slot.

const chan = @import("chan.zig");

pub const Info = extern struct {
    sp: u64, // initial guest sp: argc, argv, envp, auxv follow
    main_lo: u64,
    main_hi: u64,
    interp_lo: u64,
    interp_hi: u64,
    stack_lo: u64,
    stack_hi: u64,
};

pub var info: Info = .{ .sp = 0, .main_lo = 0, .main_hi = 0, .interp_lo = 0, .interp_hi = 0, .stack_lo = 0, .stack_hi = 0 };

pub fn publish(ch: *chan.Chan) void {
    ch.pinfo = @intFromPtr(&info);
}
