// life.zig — a stub exits when its supervisor dies. The supervisor keeps the
// write end of a pipe whose read end every stub inherits as fd 4 (forks share
// it); a thread blocks reading it and sees EOF once the supervisor is gone,
// SIGKILLed or not. It SIGKILLs its process group on the way out, which holds
// the run's stubs only: among them any a guest made with a raw Darwin fork,
// which the supervisor never saw and which have no watcher of their own. A
// process-directed SIGUSR1 landing on this thread is routed to the guest
// threads by sig.onRoute, so no signal mask is needed.

const fdlink = @import("fdlink.zig");

const FD: u64 = 4;
const SYS_read: u64 = 3;
const SYS_kill: u64 = 37;
const SIGKILL: u64 = 9;
const EINTR: i64 = 4;

extern "c" fn pthread_create(t: *usize, attr: ?*anyopaque, start: *const fn (?*anyopaque) callconv(.C) ?*anyopaque, arg: ?*anyopaque) c_int;
extern "c" fn _exit(code: c_int) noreturn;

fn watch(_: ?*anyopaque) callconv(.C) ?*anyopaque {
    var b: u8 = 0;
    while (true) {
        const r = fdlink.sys(SYS_read, FD, @intFromPtr(&b), 1, 0, 0, 0);
        if (r == -EINTR) continue;
        if (r == 0) {
            _ = fdlink.sys(SYS_kill, 0, SIGKILL, 0, 0, 0, 0);
            _exit(137);
        }
        return null; // no lifeline (or someone wrote to it): stop watching
    }
}

// start runs the watcher; call it once per process (main, and a fork child,
// which keeps only the forking thread).
pub fn start() void {
    var t: usize = 0;
    _ = pthread_create(&t, null, watch, null);
}
