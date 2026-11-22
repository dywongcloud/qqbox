# AGENTS.md

Guidance for coding agents (and humans) working in this repository.

## What this is

qqbox runs an unmodified Linux program — a static or dynamic ELF binary, or a
whole OCI container — inside a host process on Linux, macOS, or Windows, on
amd64 or arm64. There is no virtual machine and no second kernel. The guest's
own machine code runs natively on the host CPU; only its `syscall`/`svc`
instructions are redirected into a Rust supervisor that emulates the Linux ABI.

All Go source and Go build tooling were removed at the user's request; the tree is
Rust + Zig only. The Rust control plane (`crates/qqbox`) prepares runs and owns
OCI/Compose/daemon/runtime-spec/MCP/library surfaces. The Linux native
guest-execution backend (memfd/seccomp `RET_TRAP`/`SIGSYS`/Rust host-op hop) IS
ported and live-witnessed, including a real unmodified static aarch64 ELF via
`qqbox backend exec-elf`. macOS Mach/Seatbelt/HVF and Windows LPAC are NOT
ported; `qqbox run` returns `Error::UnsupportedPlatform` for the general case.
See docs/NATIVE_BACKEND.md and docs/PORT_STATUS.md. The live Xubuntu 26 desktop
is booted with the pre-removal Go runtime binary preserved off-repo. The
committed in-guest stubs and native helpers are Zig; stub and supervisor talk
over the shared-memory channel.

## Rule 1: only syscall instructions are rewritten

The guest runs at full native speed. We do **not** disassemble-and-translate,
instrument, or interpret guest code. The rewriter locates the syscall sites in a
loaded image and patches those instructions to trap into the supervisor; every
other byte of the program executes untouched.

- On Linux the installed seccomp program TRAPs the served syscalls
  (`SECCOMP_TRAP_TAG` → SIGSYS → chanPage) and KILLs a fixed deny-list of direct
  host escapes. The Go-era default-deny text-range prefix is NOT installed in
  Rust: the full ~205-syscall table is unported. `ErrKilled` fires only for a
  genuinely rejected call. macOS Seatbelt and the Windows instrumentation
  callback kill. Never add a silent emulation path around the filter, and never
  claim a default-deny backstop that is not installed.
- The rewriter finds syscall sites **without a disassembler** — hand-rolled
  per-architecture classifiers, with `arm64asm`/`x86asm` used only as test
  oracles. Do not pull in a full disassembler to "fix" a miss; fix the
  classifier and add the site to its corpus.

## Rule 2: the vfs Namespace is the only filesystem

Every path the guest names resolves through the single `vfs` Namespace
chokepoint. There is no second way to touch a file.

- No direct host-path access from a syscall handler. A backend is confined to
  its subtree; device nodes come from a fixed allowlist; `/proc`, `/sys`,
  `/dev` and the cgroup tree are synthetic filesystems mounted in the same
  Namespace, so they resolve like any other path.
- `mount`, `bind`, `pivot_root`, and the overlay union are all Namespace
  operations. A container's rootfs, its tmpfs mounts, and its relocated pseudo
  mounts are the same homogeneous `vfs` objects — no special-cased host dance.
- When something is hard (an unlinked O_PATH fd, a magic link, a relocated
  `/proc`), make it correct **generically**, matching real Linux semantics,
  routed through the vfs. Do not add a workload-specific shortcut.

## Rule 3: the supervisor owns the data plane

File and network I/O live in the Rust supervisor. The stub forwards; it does
not become an I/O engine.

- The stub handles only what must run in the guest address space: thread
  control (`clone`/`futex`/thread-exit), signal trampolines, and the hot stdio
  fast path on a held descriptor. Everything else is a channel round-trip to the
  supervisor.
- Moving fs/net I/O into the stub is a rejected design. Performance comes from a
  cheaper transport (in-stub fast paths, batching, spin tuning), never from
  relocating the data plane.
- On Linux the guest shares the stub's address space and can branch into the
  stub's syscall instructions, so what a guest thread's own seccomp filter
  admits (`arch.guest`, `stub/zig/seccomp.zig`) is the boundary: its channel,
  futexes, signals, clocks, a new thread, its exit and the stdio fast path.
  Mapping memory, holding descriptors and forking happen only on the process's
  host-op thread, which never runs guest code and keeps
  its loop's state in registers, because the guest can write everything else.
  Do not widen the guest filter or make the hop loop read writable memory for
  convenience; route the call through the hop thread instead.

## Rule 4: native code is Zig, supervisor code is Rust

- Every stub and native helper is Zig. No C. The freestanding stub links no
  libc.
- The Rust supervisor uses typed host bindings, not a cgo shim. No Go source or
  Go module files remain in the tree; the `x/sys/unix`, `goffi`/libffi and
  `purego` claims were Go-era drift. The host-op thread design is specified in
  `crates/qqbox/src/sandbox/mod.rs` and is gated on the native backend port.
- `crates/qqbox/src/zig/abi.rs` freezes the 128-byte channel layout. Any change
  to the Zig side must keep those offsets byte-identical.

## Rule 5: libraries take options, not environment

- No Rust crate reads a behavior knob from the process environment: there are
  zero `env::var`/`temp_dir()` calls. Behavior is set through `Options`/`Config`
  and caller-supplied paths. The default image cache is the fixed
  `/tmp/qqbox/images` (created 0700 and checked on every use), never `TMPDIR`.
  Only the example shell scripts read environment variables.
- The public default is permissive where a developer expects it (RWX guest
  memory allowed, no network) and tightened by an explicit option
  (`WithRejectRWX`, `WithNet` plus the egress allowlist options). Keep new
  surface area on the options, not on globals.

## Working in the tree

- Platforms are `linux`, `darwin`, `windows` × `amd64`, `arm64`. A change to a
  syscall handler, the rewriter, or the channel is cross-platform until proven
  otherwise — gate the item, then run the full corpus.
- The Rust workspace is the only build entry point: `cargo build --workspace`
  builds the library plus the `qqbox`, `qqboxd`, `qqbox-mcp` and `qqbox-runtime`
  binaries. There is no Go workspace. The Linux trap-to-supervisor backend IS
  ported for static aarch64 ET_EXEC; macOS Mach/Seatbelt/HVF, Windows LPAC,
  PIE/ET_DYN, trampolines and the full syscall table are not. The general
  `qqbox run` path returns `UnsupportedPlatform`. See docs/PORT_STATUS.md and
  docs/NATIVE_BACKEND.md.
- `cargo run -p qqbox-cli -- invariants` and the live CLI witnesses are the gate
  for control-plane changes. Zig stubs rebuild through `make stubs`; the committed
  `.bin` blobs are embedded with `include_bytes!` so a Rust build needs no Zig.
- Never use a sleep, timeout, or periodic recheck as a correctness fix, and
  never inject a delay to reproduce a race — fix the synchronization.
- Keep comments minimal; prefer code that reads without them. Do not tag a
  change with a tool or mode name in code, comments, or commit messages.
- Generated files (`gen/`, anything from a code generator) and vendored trees
  are left exactly as they are.
