# AGENTS.md

Guidance for coding agents (and humans) working in this repository.

## What this is

laboon runs an unmodified Linux program — a static or dynamic ELF binary, or a
whole OCI container — inside a host process on Linux, macOS, or Windows, on
amd64 or arm64. There is no virtual machine and no second kernel. The guest's
own machine code runs natively on the host CPU; only its `syscall`/`svc`
instructions are redirected into a Go supervisor that emulates the Linux ABI.

The Rust supervisor is the current control plane (`crates/laboon`); the original Go
supervisor remains in the tree as the working native guest backend until the three
per-OS backends are ported. See docs/PORT_STATUS.md. The in-guest stub and every
native helper are Zig. The two talk over the shared-memory channel.

## Rule 1: only syscall instructions are rewritten

The guest runs at full native speed. We do **not** disassemble-and-translate,
instrument, or interpret guest code. The rewriter locates the syscall sites in a
loaded image and patches those instructions to trap into the supervisor; every
other byte of the program executes untouched.

- A syscall the rewriter missed reaches the host backstop. On Linux the seccomp
  text-range check TRAPs (`SECCOMP_TRAP_TAG`) and the SIGSYS handler routes the
  call to the supervisor, so static rewriting is an optimization and the trap is
  the correctness backstop; `ErrKilled` fires only for a genuinely rejected call.
  macOS Seatbelt and the Windows instrumentation callback kill. Never add a
  silent emulation path around the rewriter.
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

File and network I/O live in the Go supervisor. The stub forwards; it does not
become an I/O engine.

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
  host-op thread (`sandbox/hop_linux.go`), which never runs guest code and keeps
  its loop's state in registers, because the guest can write everything else.
  Do not widen the guest filter or make the hop loop read writable memory for
  convenience; route the call through the hop thread instead.

## Rule 4: native code is Zig, supervisor code is Rust

- Every stub and native helper is Zig. No C. The freestanding stub links no
  libc.
- The Rust supervisor uses typed host bindings, not a cgo shim. The retained Go
  supervisor's host-call layer is `x/sys/unix` on Linux, `goffi`/libffi on macOS,
  `process_vm_readv`/`writev` for guest memory, and the Linux host-op thread.
  `purego` is a stale indirect dependency in the `cmd/*/go.mod` files and is not
  imported by any Go source — do not follow that claim when porting.
- `crates/laboon/src/zig/abi.rs` freezes the 128-byte channel layout. Any change
  to the Zig side must keep those offsets byte-identical.

## Rule 5: libraries take options, not environment

- `laboon`, `sandbox`, `oci`, `vfs`, and `rewrite` read **no** environment
  variables; behavior is set through `With…` options (`options.go`), a `Config`,
  or their Rust equivalents `Options` / `Config` in `crates/laboon`. Only
  `cmd/laboon`, examples, and test tooling read the environment.
- The public default is permissive where a developer expects it (RWX guest
  memory allowed, no network) and tightened by an explicit option
  (`WithRejectRWX`, `WithNet` plus the egress allowlist options). Keep new
  surface area on the options, not on globals.

## Working in the tree

- Platforms are `linux`, `darwin`, `windows` × `amd64`, `arm64`. A change to a
  syscall handler, the rewriter, or the channel is cross-platform until proven
  otherwise — gate the item, then run the full corpus.
- The Rust workspace is the build entry point: `cargo build --workspace` builds the
  library, the `laboon` CLI and the `laboond` daemon. The Go workspace (`go.work`)
  remains as the native backend reference; build its CLI with `go build ./cmd/laboon`.
- `cargo run -p laboon-cli -- invariants` and the live CLI witnesses are the gate
  for control-plane changes. Zig stubs rebuild through `make stubs`; the committed
  `.bin` blobs are embedded with `include_bytes!` so a Rust build needs no Zig.
- Never use a sleep, timeout, or periodic recheck as a correctness fix, and
  never inject a delay to reproduce a race — fix the synchronization.
- Keep comments minimal; prefer code that reads without them. Do not tag a
  change with a tool or mode name in code, comments, or commit messages.
- Generated files (`gen/`, anything from a code generator) and vendored trees
  are left exactly as they are.
