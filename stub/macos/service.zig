// service.zig — what the supervisor asks of a stub on its own account, outside
// any guest thread's syscall: one thread answers it on a port the stub
// registers with the broker (this task's bootstrap port), so it is answered
// whatever the guest's threads are doing. Another process reading this one's
// /proc/<pid>/maps or smaps asks it for the mappings; the broker asks it to
// freeze or cool shared code (sandbox/codereg_darwin.go, wx.zig), and
// registering, it learns what shared code other stubs run. Only the broker
// holds a send right to the port, so a request comes from the supervisor.
// A guest thread asks the broker in turn (ask): to run a page of shared
// memory, or to write one another process runs.

const std = @import("std");
const smaps = @import("smaps.zig");

extern "c" fn mach_task_self() u32;
extern "c" fn mach_port_allocate(task: u32, right: u32, name: *u32) c_int;
extern "c" fn mach_port_deallocate(task: u32, name: u32) c_int;
extern "c" fn mach_msg(msg: *anyopaque, option: c_int, send_size: u32, rcv_size: u32, rcv_name: u32, timeout: u32, notify: u32) c_int;
extern "c" fn mach_port_mod_refs(task: u32, name: u32, right: u32, delta: i32) c_int;
extern "c" fn mach_reply_port() u32;
extern "c" fn vm_deallocate(task: u32, addr: u64, size: u64) c_int;
extern "c" fn task_get_special_port(task: u32, which: c_int, port: *u32) c_int;

const TASK_BOOTSTRAP_PORT: c_int = 4;
const MACH_PORT_RIGHT_RECEIVE: u32 = 1;
const MOVE_SEND_ONCE: u32 = 18;
const COPY_SEND: u32 = 19;
const MAKE_SEND: u8 = 20;
const COMPLEX: u32 = 0x80000000;
const SEND_MSG: c_int = 1;
const RCV_MSG: c_int = 2;
const RCV_TOO_LARGE: c_int = 0x10004004;

// Message ids; the broker's must match sandbox/broker_darwin.go.
const ID_SERVICE: i32 = 0x4c430105; // stub -> broker: its service port
const ID_MAPS: i32 = 0x4c430110; // supervisor -> stub: its mappings
const ID_MAPS_REPLY: i32 = 0x4c430111;
const ID_SMAPS: i32 = 0x4c430112; // supervisor -> stub: its mappings with their counts
const ID_SMAPS_REPLY: i32 = 0x4c430113;
const ID_FREEZE: i32 = 0x4c430114; // broker -> stub: page
const ID_COOL: i32 = 0x4c430115; // page
const ID_COOLFILE: i32 = 0x4c430116; // inode
const ID_STOP: i32 = 0x4c430117;
const ID_GO: i32 = 0x4c430118;
const ID_ACK: i32 = 0x4c430119;
const ID_HOTSET: i32 = 0x4c430106; // broker -> a registering stub: the pages held
pub const ID_HOT: i32 = 0x4c430120; // stub -> broker: page
pub const ID_COOL_ASK: i32 = 0x4c430121;
const ID_CODE_ACK: i32 = 0x4c430122;
const MAKE_SEND_ONCE: u32 = 21;

const Header = extern struct { bits: u32, size: u32, remote: u32, local: u32 = 0, voucher: u32 = 0, id: i32 };
const PortDesc = extern struct { name: u32, pad1: u32 = 0, pad2: u16 = 0, disposition: u8, typ: u8 = 0 };
const OolDesc = extern struct { address: u64 align(4), deallocate: u8 = 0, copy: u8 = 1, pad1: u8 = 0, typ: u8 = 1, size: u32 };
const PortMsg = extern struct { h: Header, n: u32, port: PortDesc, pid: i32 };
const MapsReply = extern struct { h: Header, n: u32, ool: OolDesc, count: u32 };

// MapEnt is a mapping as the supervisor renders it (mem.zig's and vmm.zig's).
pub const MapEnt = extern struct { lo: u64, hi: u64, off: u64, aux: u64, attr: u64 };
// MapsFn lists the mappings from address from on into out, at most cap,
// setting next to where the next call starts (0: all listed); it returns how
// many it wrote.
pub const MapsFn = *const fn (out: [*]MapEnt, cap: usize, from: u64, next: *u64) u64;

// SmapsFn is MapsFn listing each mapping with its counts (smaps.zig).
pub const SmapsFn = *const fn (out: [*]smaps.SmapEnt, cap: usize, from: u64, next: *u64) u64;

// Page is a page of memory, as the broker names it: its VM object's id and
// offset.
pub const Page = extern struct { obj: u32, pad: u32 = 0, off: u64 };

// Code is how a stub serves the broker's shared-code requests (wx.zig).
pub const Code = struct {
    held: *const fn (pages: []const Page) void, // what other stubs ran as it registered
    freeze: *const fn (p: Page) void,
    cool: *const fn (p: Page) void,
    coolFile: *const fn (ino: u64) void,
    stop: *const fn () void,
    go: *const fn () void,
};

var port: u32 = 0;
var broker_port: u32 = 0;
var maps_fn: MapsFn = undefined;
var smaps_fn: SmapsFn = undefined;
var code_fns: ?*const Code = null;

// start registers the service port with the broker and starts the thread that
// answers on it. It runs before Seatbelt.
// The broker answers the registration with the pages other stubs hold.
const HotSet = extern struct { h: Header, n: u32, ool: OolDesc, count: u32, trailer: [64]u8 };

pub fn start(maps: MapsFn, smapsF: SmapsFn, code: ?*const Code) void {
    const task = mach_task_self();
    maps_fn = maps;
    smaps_fn = smapsF;
    code_fns = code;
    if (task_get_special_port(task, TASK_BOOTSTRAP_PORT, &broker_port) != 0 or broker_port == 0) return;
    if (mach_port_allocate(task, MACH_PORT_RIGHT_RECEIVE, &port) != 0) return;
    var reply: u32 = 0;
    if (mach_port_allocate(task, MACH_PORT_RIGHT_RECEIVE, &reply) != 0) return;
    defer _ = mach_port_mod_refs(task, reply, MACH_PORT_RIGHT_RECEIVE, -1);
    var m = PortMsg{
        .h = .{ .bits = COPY_SEND | MAKE_SEND_ONCE << 8 | COMPLEX, .size = @sizeOf(PortMsg), .remote = broker_port, .local = reply, .id = ID_SERVICE },
        .n = 1,
        .port = .{ .name = port, .disposition = MAKE_SEND },
        .pid = 0,
    };
    if (mach_msg(&m, SEND_MSG, @sizeOf(PortMsg), 0, 0, 0, 0) != 0) return;
    // Registered, it hears of every page held from here; what was held before
    // comes in the answer.
    var in: HotSet = undefined;
    if (mach_msg(&in, RCV_MSG, 0, @sizeOf(HotSet), reply, 0, 0) == 0 and in.h.id == ID_HOTSET and in.h.bits & COMPLEX != 0) {
        if (in.ool.size > 0) {
            const pages: [*]const Page = @ptrFromInt(in.ool.address);
            if (code) |c| c.held(pages[0..@min(in.count, in.ool.size / @sizeOf(Page))]);
            _ = vm_deallocate(task, in.ool.address, in.ool.size);
        }
    }
    const t = std.Thread.spawn(.{ .stack_size = 64 << 10 }, serve, .{}) catch return;
    t.detach();
}

// ask sends the broker request id (ID_HOT, ID_COOL_ASK) for page p and waits
// for its answer, on this thread's own reply port (made by the trap Seatbelt
// leaves the stub, not mach_port_allocate).
threadlocal var ask_reply: u32 = 0;

pub fn ask(id: i32, p: Page) void {
    if (broker_port == 0) return;
    if (ask_reply == 0) ask_reply = mach_reply_port();
    if (ask_reply == 0) return;
    var m: extern struct { h: Header, p: Page, trailer: [64]u8 } = .{
        .h = .{ .bits = COPY_SEND | MAKE_SEND_ONCE << 8, .size = @sizeOf(Header) + @sizeOf(Page), .remote = broker_port, .local = ask_reply, .id = id },
        .p = p,
        .trailer = undefined,
    };
    _ = mach_msg(&m, SEND_MSG | RCV_MSG, @sizeOf(Header) + @sizeOf(Page), @sizeOf(@TypeOf(m)), ask_reply, 0, 0);
}

fn serve() void {
    const task = mach_task_self();
    const gpa = std.heap.c_allocator;
    var list: std.ArrayListUnmanaged(MapEnt) = .{};
    while (true) {
        var in: extern struct { h: Header, trailer: [64]u8 } = undefined;
        const r = mach_msg(&in, RCV_MSG, 0, @sizeOf(@TypeOf(in)), port, 0, 0);
        if (r == RCV_TOO_LARGE) continue; // not a request: the kernel destroyed it
        if (r != 0) return;
        const reply = in.h.remote; // the send-once right the request carried
        if (in.h.id >= ID_FREEZE and in.h.id <= ID_GO and in.h.bits & COMPLEX == 0 and reply != 0) {
            serveCode(in.h.id, @ptrCast(&in.trailer));
            const a = Header{ .bits = MOVE_SEND_ONCE, .size = @sizeOf(Header), .remote = reply, .id = ID_ACK };
            var am = a;
            _ = mach_msg(&am, SEND_MSG, @sizeOf(Header), 0, 0, 0, 0);
            continue;
        }
        if ((in.h.id != ID_MAPS and in.h.id != ID_SMAPS) or in.h.bits & COMPLEX != 0 or reply == 0) {
            if (reply != 0) _ = mach_port_deallocate(task, reply);
            continue;
        }
        if (in.h.id == ID_SMAPS) {
            replySmaps(reply, gpa);
            continue;
        }
        list.clearRetainingCapacity();
        var from: u64 = 0;
        while (true) {
            list.ensureUnusedCapacity(gpa, 256) catch break;
            var next: u64 = 0;
            list.items.len += maps_fn(list.items.ptr + list.items.len, list.capacity - list.items.len, from, &next);
            if (next == 0) break;
            from = next;
        }
        var m = MapsReply{
            .h = .{ .bits = MOVE_SEND_ONCE | COMPLEX, .size = @sizeOf(MapsReply), .remote = reply, .id = ID_MAPS_REPLY },
            .n = 1,
            .ool = .{ .address = @intFromPtr(list.items.ptr), .size = @intCast(list.items.len * @sizeOf(MapEnt)) },
            .count = @intCast(list.items.len),
        };
        _ = mach_msg(&m, SEND_MSG, @sizeOf(MapsReply), 0, 0, 0, 0);
    }
}

// replySmaps answers an ID_SMAPS request on reply: every mapping, with its
// counts.
fn replySmaps(reply: u32, gpa: std.mem.Allocator) void {
    var list: std.ArrayListUnmanaged(smaps.SmapEnt) = .{};
    defer list.deinit(gpa);
    var from: u64 = 0;
    while (true) {
        list.ensureUnusedCapacity(gpa, 64) catch break;
        var next: u64 = 0;
        list.items.len += smaps_fn(list.items.ptr + list.items.len, list.capacity - list.items.len, from, &next);
        if (next == 0) break;
        from = next;
    }
    var m = MapsReply{
        .h = .{ .bits = MOVE_SEND_ONCE | COMPLEX, .size = @sizeOf(MapsReply), .remote = reply, .id = ID_SMAPS_REPLY },
        .n = 1,
        .ool = .{ .address = @intFromPtr(list.items.ptr), .size = @intCast(list.items.len * @sizeOf(smaps.SmapEnt)) },
        .count = @intCast(list.items.len),
    };
    _ = mach_msg(&m, SEND_MSG, @sizeOf(MapsReply), 0, 0, 0, 0);
}

// serveCode does what a shared-code request asks, its body at b.
fn serveCode(id: i32, b: [*]const u8) void {
    const c = code_fns orelse return; // a stub with no shared code (hvf_stub.zig)
    const w: [*]align(1) const u64 = @ptrCast(b);
    switch (id) {
        ID_FREEZE, ID_COOL => {
            const p = Page{ .obj = @as(*align(1) const u32, @ptrCast(b)).*, .off = w[1] };
            if (id == ID_FREEZE) c.freeze(p) else c.cool(p);
        },
        ID_COOLFILE => c.coolFile(w[0]),
        ID_STOP => c.stop(),
        ID_GO => c.go(),
        else => {},
    }
}
