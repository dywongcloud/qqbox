// Second Zig stub piece: the Mach setup the stub does -- allocate a port and
// set the task's EXC_BAD_INSTRUCTION exception port to it (state+identity, the
// behavior the supervisor's handler uses). Proves Zig can make the Mach port
// and exception calls (extern "c"), cross-compiled from Linux. kr==0 means the
// task's traps would now be delivered to that port.
const std = @import("std");

const kern_return_t = c_int;
const mach_port_t = c_uint;
extern "c" fn mach_task_self() mach_port_t;
extern "c" fn mach_port_allocate(task: mach_port_t, right: c_int, name: *mach_port_t) kern_return_t;
extern "c" fn mach_port_insert_right(task: mach_port_t, name: mach_port_t, poly: mach_port_t, right: c_uint) kern_return_t;
extern "c" fn task_set_exception_ports(task: mach_port_t, mask: c_uint, port: mach_port_t, behavior: c_uint, flavor: c_int) kern_return_t;

const MACH_PORT_RIGHT_RECEIVE = 1;
const MACH_MSG_TYPE_MAKE_SEND = 20;
const EXC_MASK_BAD_INSTRUCTION = 1 << 1;
const EXCEPTION_STATE_IDENTITY = 3;
const MACH_EXCEPTION_CODES: c_uint = 0x80000000;
const ARM_THREAD_STATE64 = 6;

pub fn main() void {
    const me = mach_task_self();
    var port: mach_port_t = 0;
    var kr = mach_port_allocate(me, MACH_PORT_RIGHT_RECEIVE, &port);
    if (kr != 0) { std.debug.print("port_allocate kr={d}\n", .{kr}); return; }
    _ = mach_port_insert_right(me, port, port, MACH_MSG_TYPE_MAKE_SEND);
    kr = task_set_exception_ports(me, EXC_MASK_BAD_INSTRUCTION, port,
        EXCEPTION_STATE_IDENTITY | MACH_EXCEPTION_CODES, ARM_THREAD_STATE64);
    std.debug.print("task_self=0x{x} exc_port=0x{x} set_exception_ports kr={d} {s}\n",
        .{ me, port, kr, if (kr == 0) "-> ZIG-MACH-OK" else "-> FAIL" });
}
