// Landlock confines the stub process, as far as the kernel's Landlock ABI
// goes, to what it does: it opens no path, binds and connects no socket, and
// signals no process but its own (the supervisor serves the guest's kill).
// Its seccomp filters already admit no call that names a path or a port, so
// the file and network rules are a second fence; the scope is the one only
// Landlock draws: a stub thread may send its kick signal with tgkill, which
// seccomp cannot pin to the stub's own pid, so on a kernel without Landlock's
// signal scope (ABI 6) a guest that reaches the stub's text can send SIGURG
// to any process of the user. Every later thread and fork inherits the
// domain. Without Landlock this does nothing.

const stub = @import("stub.zig");
const nr = stub.nr;

const CREATE_RULESET_VERSION = 1;
const PR_SET_NO_NEW_PRIVS = 38;

const RulesetAttr = extern struct {
    fs: u64, // handled_access_fs
    net: u64, // handled_access_net, ABI 4
    scoped: u64, // ABI 6
};

pub fn restrict() void {
    const abi = stub.sys(nr(.landlock_create_ruleset), 0, 0, CREATE_RULESET_VERSION, 0, 0, 0);
    if (abi == 0 or abi > stub.MAX_ERRNO) return;
    var attr = RulesetAttr{ .fs = 0x1fff, .net = 0, .scoped = 0 }; // execute .. make_sym
    if (abi >= 2) attr.fs |= 1 << 13; // refer
    if (abi >= 3) attr.fs |= 1 << 14; // truncate
    if (abi >= 5) attr.fs |= 1 << 15; // ioctl_dev
    if (abi >= 4) attr.net = 3; // bind_tcp, connect_tcp
    if (abi >= 6) attr.scoped = 3; // abstract_unix_socket, signal
    const size: u64 = if (abi >= 6) 24 else if (abi >= 4) 16 else 8;
    const fd = stub.sys(nr(.landlock_create_ruleset), @intFromPtr(&attr), size, 0, 0, 0, 0);
    if (fd > stub.MAX_ERRNO) return;
    if (stub.sys(nr(.prctl), PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0, 0) != 0) stub.die(121);
    if (stub.sys(nr(.landlock_restrict_self), fd, 0, 0, 0, 0, 0) != 0) stub.die(137);
    _ = stub.sys(nr(.close), fd, 0, 0, 0, 0, 0);
}
