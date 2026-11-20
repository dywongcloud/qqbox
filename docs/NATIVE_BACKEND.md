# Native guest-execution backend (Rust port status)

**Status: the Linux trap-to-supervisor path is ported and live-witnessed on a
real Linux 6.17.7 aarch64 kernel. A full guest runtime is NOT ported.**
`go-removal` stays pending.

## What "the backend" is

qqbox runs an unmodified Linux ELF on the host CPU by rewriting syscall
instructions. The supervisor owns the data plane through a 128-byte chanPage
(`crates/qqbox/src/zig/abi.rs`). The removed Go engine had three per-host
backends:

| Host | Go-era launch contract |
|---|---|
| Linux | `memfd_create("lc-chan")` (fd 3), lifeline pipe (fd 4), `SOCK_DGRAM` fdpass socketpair (fd 5), stub exec via `/proc/self/fd/N`, seccomp text-range `RET_TRAP`, host-op hop thread, ~205 emulated syscalls |
| macOS | named `MAP_SHARED` temp chanPage (path in argv[1]), `os_sync` handshake, Mach broker bootstrap port, compiled Seatbelt profile, MAP_JIT W^X; low-linked non-PIE ET_EXEC guests used a Hypervisor.framework VM (a second address space) |
| Windows | `CreateFileMapping` chanPage, per-slot auto-reset events, LPAC AppContainer, VEH + x86-64 instrumentation callback, `RtlCloneUserProcess` fork |

The Go tree was 445 files / 85,620 lines; `internal/sandbox` alone was 322
files / ~69,300 lines. Reproducing it fully is not a one-session job.

## What is now Rust

`crates/qqbox/src/backend/`:

| module | content |
|---|---|
| `channel.rs` | real `memfd_create` + `MAP_SHARED` chanPage; `ChannelRequest`/`ChannelResponse` at the frozen ABI offsets; futex word is the chanPage `state` at byte 8 (ST_REQ/ST_REP) |
| `filter.rs` | seccomp-BPF builder: `text_range_prefix(lo,hi)` (the six-instruction `[__ehdr_start, etext)` TRAP prologue), `allow_list(arch, nrs)`, `trap_one(nr)`; real `prctl(PR_SET_NO_NEW_PRIVS)` + `seccomp(SET_MODE_FILTER, ...)` installation |
| `trap.rs` | real `sigaction(SIGSYS, SA_SIGINFO)` handler; reads `siginfo_t` (`si_code == SYS_SECCOMP`, `si_errno == 0x5ca1`), saves aarch64 `x0..x5`/`x8`/`pc` from `ucontext_t`, publishes the request, blocks on a futex, and writes the supervisor's result into `x0` so the guest resumes |
| `hostop.rs` | Rust host-op dispatcher over the vfs Namespace: process identity, brk, clock, stdio read/write, openat via `Namespace::resolve`, close, fd table; unimplemented syscalls return `-ENOSYS` rather than pretending |
| `linux.rs` | `LinuxBackend` implementing `Platform`; `witness()` forks a guest thread, installs the filter + handler, executes a real `getppid`, serves it through the chanPage and resumes |
| `macos.rs` | real Mach probe (`mach_task_self`, `vm_page_size`) via mach2; honest `UnsupportedPlatform` for guest launch |
| `windows.rs` | contract recorded, cross-compiled; no Windows host available |

`crates/qqbox/src/sandbox/mod.rs:install()` on Linux now calls
`backend::filter::install()` instead of returning `UnsupportedPlatform`.

CLI: `qqbox backend capabilities | witness | install | dispatch <nr> <args…>`.

## Live witness (this session)

The witness runs in a real Linux VM (`podman machine ssh`, Linux 6.17.7
aarch64, `seccomp/actions_avail` includes `trap`), using a statically linked
`aarch64-unknown-linux-musl` build of `qqbox` reached through virtiofs.

Real `qqbox backend witness` output:

```json
{
  "host": "linux", "arch": "aarch64",
  "channel_fd": 3, "channel_bytes": 4096,
  "filter_instructions": 4,
  "trapped_syscall": 173,
  "supervisor_result": 1364279887,
  "guest_observed_result": 1364279887,
  "untrapped_control_result": 51848,
  "control_differs": true,
  "trap_hits": 1, "served": 1, "match_ok": true
}
```

The **control** is the falsification: the same `getppid` executed *before*
the filter is installed returns the kernel's real parent pid 51848. After the
filter, the same instruction returns the Rust supervisor's sentinel
`0x5151424f` = 1364279887, with exactly one SIGSYS hit. That proves a real
seccomp trap, a real SIGSYS register save/restore, a real chanPage request,
a real Rust supervisor dispatch, and a real guest resume.

`qqbox backend dispatch` witnesses on the same kernel:
`getpid(172) -> 52472`, `brk(214) -> 0`, `clock_gettime(113) -> 0`,
unknown `999 -> -38` (ENOSYS, not a fake success).

`qqbox backend capabilities` on Linux reports `memfd_channel=true`,
`seccomp_trap=true`, `sigsys_handler=true`, `implemented_syscalls=16`.

Cross-target: `cargo check` and `cargo clippy -- -D warnings` pass for
`aarch64-apple-darwin` and `aarch64-unknown-linux-musl`. `x86_64-unknown-linux-gnu`
and `x86_64-pc-windows-msvc` could not be checked: `ring`'s C build needs a
cross C compiler/sysroot not present on this macOS host. That is a toolchain
gap, not a backend code failure, and is not claimed as verified.

## What is NOT ported (honest boundary)

- ELF segment mapping, `lc_syscall_entry` trampoline islands and symbol
  resolution, `StubWindow` adoption, `REP_START`, the 79 MiB memfd geometry,
  `PROT_R` hop alias / hop thread / `HOP_FORK`.
- The ~205 real syscall handlers (fs, net, procfs, signal, SysV IPC, ptrace).
  Rust currently implements 16; the rest return `-ENOSYS`.
- Signal frames, `tgkill` KICK, Landlock, `process_vm_readv/writev`, `SCM_RIGHTS`
  fd passing, `openat2` chokepoint, ptrace register emulation.
- macOS: named chanPage, `os_sync` handshake, compiled Seatbelt profile,
  MAP_JIT, Mach broker/fdlink/futex, ad-hoc codesign, `legacyX18` patch,
  `posix_spawn` special port, and the HVF VM (which needs the
  `com.apple.security.hypervisor` entitlement).
- Windows: LPAC AppContainer creation, VEH + instrumentation callback,
  `RtlCloneUserProcess`, `NtCreateSection`, event pair handshake.
- Consequently `qqbox::run` still returns `Error::UnsupportedPlatform` after
  preparing the control plane. No unmodified guest ELF is executed by a
  from-source Rust build. The live desktop on VNC 5905 still boots with the
  preserved pre-removal Go binary off-repo.

`go-removal` remains pending until all three backends pass the historical
regression corpus on each host.
