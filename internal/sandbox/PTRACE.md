# Linux ptrace v2

Each traced tid has supervisor-owned state under `dfs.mu`: tracer identity,
options, resume mode, one pending wait report, and a register snapshot.
A stop publishes its report and waits on `dfs.cond`, releasing the lock;
resume consumes the stop. Ordinary pending signals do not release a ptrace
stop. Kill and teardown do. Job-control stops remain separate. Tracer waits
include auto-attached descendants and threads; the real parent retains its
eventual process exit report.

Syscall entry snapshots precede dispatch; exit snapshots precede the reply to
the stub, including clone and exec. Fork events precede the vfork wait. Exec
moves the surviving thread's tracing state to the leader tid after retiring
siblings, and reports the old tid through GETEVENTMSG. GET_SYSCALL_INFO remains
the strace interface. Traced calls bypass the native clock and stdio fast paths.

`stub/zig/trace.zig` reserves offset 256 in each thread's shared signal record,
outside the channel scratch: kind and dirty words, then the native NT_PRSTATUS
register image (216 bytes on amd64, 272 on arm64). Syscalls publish the complete
saved Frame, including flags; signal delivery and synchronous faults publish
the kernel ucontext. Exec supplies a fresh initial frame and clone inherits
the parent's frame with the child's stack, TLS and return value.

GETREGS/GETREGSET/PEEKUSER read the stopped snapshot. SETREGS,
SETREGSET(NT_PRSTATUS) and POKEUSER validate and edit it under `dfs.mu`.
Resume copies edits back to the shared record. Trap handlers restore edited
GPRs, PC, SP and flags through ucontext; a modified trampoline return takes
`lc_sig_trap` to obtain a kernel frame and resumes through host rt_sigreturn.
Syscall number/argument edits affect dispatch, and result edits affect the
returned value. The stub's GS slot and privileged register state cannot be
changed. FP/SIMD register sets and hardware watchpoints are not exposed.

An x86 int3 reports SIGTRAP with PC after the breakpoint; arm64 BRK reports
its instruction address. The tracer can restore the instruction, rewind PC
on x86, and resume. x86 SINGLESTEP uses TF and reports TRAP_TRACE, including
the DR6 status gdb reads. Stepping a rewritten syscall, its trampoline jump,
or resuming a syscall-entry stop with SINGLESTEP returns EIO: executing the
stub machinery under TF would expose internal instructions. arm64 SINGLESTEP
returns EIO because EL0 cannot enable MDSCR_EL1 single-step. Ordinary x86 guest
instructions, including stepping past a software breakpoint, are supported.
PTRACE_ATTACH stops the thread with a SIGSTOP whose signal-delivery stop is
the tracer's first. PTRACE_SEIZE attaches without a stop; a seized tracee's
group stops, and the trap of an auto-attached child, are PTRACE_EVENT_STOP,
and its exec has no legacy SIGTRAP. PTRACE_INTERRUPT asks the thread for a
trap: the supervisor publishes it in the thread's pending word as a SIGSTOP
(no mask holds that back, and the signal queue itself is untouched), so the
stub comes to take a signal wherever it is -- guest code, a stub wait, or a
supervisor-served blocking call, which is cut short and restarted after the
stop -- and the supervisor runs the trap first, before any signal, as Linux's
get_signal runs a pending jobctl trap. The trap reports the group stop's
signal while the process is stopped, else SIGTRAP. PTRACE_LISTEN holds a
tracee in its EVENT_STOP, and a SIGCONT re-traps it at once; a SIGCONT asks
every other seized thread of the process for a trap, as ptrace_trap_notify.
Attaching to a task stopped by job control re-traps it into that stop for
the tracer, and a tracee let go while its process is stopped goes back to
the stop. A job-control stop is the host's: the whole stub process on Linux
and macOS, each thread on Windows. For one thread among several the trap
needs that thread to run alone. Windows resumes just that thread, its
context already redirected to take the trap. Linux hands the stop to the
threads: every other thread is asked to park (a phantom SIGSTOP in its
pending word, as the trap is) and kicked while the host still holds it, then
the host lets go; a stopped thread takes its pending kick before any
instruction, so none runs guest code before it parks in the supervisor, as
Linux's do_signal_stop. macOS kicks a process, not a thread (its stub routes
the kick once running), so there the re-trap waits for a SIGCONT.
`/proc/<pid>/stat` and `status` show a stopped task `T` and one in a ptrace
stop `t`.

`/proc/<pid>/mem`, including self and task paths, checks ptraceMayAccess at
open. Reads and writes use bounded page chunks and support seek, pread and
pwrite. Both this file and PEEK/POKETEXT share the stub-region guard and
process_vm access with a host `/proc/<pid>/mem` fallback for forced text
access. arm64 writes always use the host mem file so the kernel performs
[instruction-cache synchronization](https://github.com/torvalds/linux/blob/v6.8/arch/arm64/mm/flush.c).
No memory request runs a host operation through a stopped tracee's channel.

Wait reports, status (including NSpid and TracerPid), stat and self use the
reader's PID namespace. Namespace magic links have identity derived from
the namespace rather than the PID path; different PID namespaces retain
different identities.

Build both stubs with Zig 0.13.0:

```
ZIG=/tmp/zig-linux-x86_64-0.13.0/zig sh stub/build.sh
```

Validation:

```
go test ./sandbox -run Ptrace -race
go test ./sandbox -race
```

The raw C tests require static gcc, not gdb. They cover breakpoint insertion
through POKETEXT and proc mem, register reads and writes, x86 single-step,
syscall argument/result edits, signal context edits, namespace identity,
large memory transfers, denied memory access and the stub guard. Both syscall
trap and trampoline paths run. Host strace tests run when installed.

A static debug build can be checked with gdb using `break add`, `run`, `bt`,
`info args`, `info registers`, `print a`, `step`, `finish`, and `continue`.
Alpine package installation needs `-net`:

```
qqbox -net -image alpine:3.20 sh -c 'apk add gdb gcc musl-dev && gcc -static -g -O0 -o /t /t.c && gdb --batch -ex "break add" -ex run -ex bt -ex "info args" -ex finish -ex continue /t'
```
