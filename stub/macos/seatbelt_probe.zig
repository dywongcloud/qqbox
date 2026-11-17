// First Zig-language macOS stub piece: apply the Seatbelt backstop (the
// deny-by-default syscall allowlist) from Zig, cross-compiled from Linux. The
// real stub applies this right before entering guest code so an un-rewritten
// `svc` that reaches Darwin is killed. Here we deny getppid and then call it:
// if Seatbelt enforces (from a Zig binary, ad-hoc signed), the process is
// SIGKILLed; otherwise it prints LEAKED. Proves Zig can set up the boundary.
const std = @import("std");

extern "c" fn sandbox_init(profile: [*:0]const u8, flags: u64, errorbuf: *?[*:0]u8) c_int;
extern "c" fn getppid() c_int;

const profile: [*:0]const u8 =
    \\(version 1)
    \\(allow default)
    \\(disable-syscall-inference)
    \\(deny syscall-unix (syscall-number 39) (with send-signal SIGKILL))
;

pub fn main() void {
    var err: ?[*:0]u8 = null;
    const rc = sandbox_init(profile, 0, &err);
    if (rc != 0) {
        std.debug.print("sandbox_init failed rc={d}\n", .{rc});
        return;
    }
    std.debug.print("seatbelt applied; calling getppid (should be killed)\n", .{});
    const p = getppid();
    std.debug.print("LEAKED: getppid returned {d} (NOT enforced)\n", .{p});
}
