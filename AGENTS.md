# AGENTS.md

Guidance for coding agents (and humans) working in this repository.

## What this is

laboon runs an unmodified Linux program — a static or dynamic ELF binary, or a
whole OCI container — inside a host process on Linux, macOS, or Windows, on
amd64 or arm64. There is no virtual machine and no second kernel. The guest's
own machine code runs natively on the host CPU; only its `syscall`/`svc`
instructions are redirected into a Go supervisor that emulates the Linux ABI.

The supervisor is Go (with `purego` for host calls). The in-guest stub and every
native helper are Zig. The two talk over a shared-memory channel.

## Rule 1: only syscall instructions are rewritten

The guest runs at full native speed. We do **not** disassemble-and-translate,
instrument, or interpret guest code. The rewriter locates the syscall sites in a
loaded image and patches those instructions to trap into the supervisor; every
other byte of the program executes untouched.

- A syscall the rewriter missed must reach the host backstop (seccomp on Linux,
  Seatbelt on macOS, the AppContainer on Windows), which kills the guest
  (`ErrKilled`). That is the design: a miss is a loud failure, never a silent
  emulation path around the rewriter.
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

## Rule 4: native code is Zig, supervisor code is Go

- Every stub and native helper is Zig. No C. The freestanding stub links no
  libc.
- The supervisor is Go. Host system calls it cannot make from Go go through
  `purego`, not a cgo shim.

## Rule 5: libraries take options, not environment

- `laboon`, `sandbox`, `oci`, `vfs`, and `rewrite` read **no** environment
  variables; behavior is set through `With…` options (`options.go`) or a
  `Config`. Only `cmd/laboon`, examples, and test tooling read the environment.
- The public default is permissive where a developer expects it (RWX guest
  memory allowed, no network) and tightened by an explicit option
  (`WithRejectRWX`, `WithNet` plus the egress allowlist options). Keep new
  surface area on the options, not on globals.

## Working in the tree

- Platforms are `linux`, `darwin`, `windows` × `amd64`, `arm64`. A change to a
  syscall handler, the rewriter, or the channel is cross-platform until proven
  otherwise — gate the item, then run the full corpus.
- The repository is a Go workspace (`go.work`). The core library is the root
  module; `cmd/laboon` is a module of its own so the library stays lean, and a
  future daemon (Docker-API compatible plus ConnectRPC) is its own module too,
  keeping heavy client deps out of the core. Build the CLI with
  `go build ./cmd/laboon`.
- `go test ./...` for the Go side; the sandbox suite (`go test ./sandbox/`) is
  the main gate for guest-behavior changes. Zig stubs build through `build/`.
- Never use a sleep, timeout, or periodic recheck as a correctness fix, and
  never inject a delay to reproduce a race — fix the synchronization.
- Keep comments minimal; prefer code that reads without them. Do not tag a
  change with a tool or mode name in code, comments, or commit messages.
- Generated files (`gen/`, anything from a code generator) and vendored trees
  are left exactly as they are.
