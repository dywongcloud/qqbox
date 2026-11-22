# Single address space for qqbox instances

Decision: **do not merge qqbox instances into one address space.** Keep one stub
process per instance and add a shared supervisor mode instead. A global single
address space is not achievable without regressions on any supported host. The
request asked for single-addressed-spaced instances "if possible without regressions";
the evidence below shows the merge removes the isolation primitives the security
model is built on.

## What "single address spaced" already means

Within one run on Linux, the guest and the Zig stub already share one address space
(`AGENTS.md:60-63`). The guest can write every byte of the stub; that is why the host-op
thread keeps its loop state in registers and the per-thread guest seccomp filter
(`stub/zig/seccomp.zig`) is the boundary. Nested qqbox composes by loading a PIE stub
into a free high window of the *same* address space (`stub/build.sh`, `load_linux.go`).
So an instance is already single-address-spaced internally. What is not shared is the
address space *across* instances.

## What currently separates instances

Each run is a separate stub process launched by a separate supervisor path. Isolation
is process-scoped on every host:

| Host | Boundary |
| --- | --- |
| Linux | per-thread seccomp guest filter (`RET_TRAP` for the syscalls the Rust supervisor serves, `RET_KILL_PROCESS` for a fixed deny-list of direct host escapes, `RET_ALLOW` otherwise), Landlock, `CLONE_CHILD_CLEARTID`, `Pdeathsig`, `process_vm_readv/writev`, memfd shared channel |
| macOS | Seatbelt `(deny default)` allowlist that kills an un-rewritten `svc`, bootstrap-port drop, ad-hoc codesign, `os_sync` shared channel |
| Windows | LPAC AppContainer token plus job kill-on-close, x86-64 ProcessInstrumentationCallback `BACKSTOP_KILL`, auto-reset events, PE cache |

The Linux filter has **three** verdicts, not a blanket kill: the syscalls the Rust
supervisor serves get `RET_TRAP` → `SIGSYS` → chanPage, a fixed 20-entry deny-list
of direct host escapes gets `RET_KILL_PROCESS`, and everything else is allowed.
There is **no default-deny text-range backstop**: the Go-era ~205-syscall table
and its text-range prefix are unported, and the Go paths (`stub/zig/seccomp.zig`,
`internal/sandbox/config.go`) are historical provenance only. The live Rust source
is `crates/qqbox/src/backend/filter.rs` (`trap_and_deny`) plus
`crates/qqbox/src/backend/linux.rs` (SIGSYS handler), witnessed on Linux aarch64.
A served-syscall rewrite miss is therefore correct but slower: static rewriting is
the optimization, the trap is the backstop. macOS Seatbelt and the Windows x86-64
instrumentation callback kill; Windows arm64 has no callback backstop. The Rust
Linux backend preserves the asymmetry. Collapsing it to kill-on-miss would regress
JIT-heavy guests (V8/Node/.NET), lazy indirect-branch site discovery and stray
`0f 05` bytes in data.
Every boundary above is attached to a process, which is the load-bearing fact for this
decision.

The `Pdeathsig`/SIGIO lifeline kills the stub when its supervisor dies. The
Go-era guest filter admitted only the channel, futexes, signals, clocks, a new
thread, exit and the stdio fast path; that allowlist is historical provenance.
In the Rust backend the filter traps the served syscalls and kills the deny-list,
while mapping memory, holding descriptors and forking happen only on a separate
host-op thread that never executes guest code.

## Why merging regresses

1. Every backstop above is attached to a process. A single address space means a single
   process, so a `RET_KILL_PROCESS` seccomp verdict or Seatbelt/LPAC denial kills *all*
   instances at once. One guest's missed syscall becomes a total outage.
2. The guest can write every byte of the stub. With several instances in one address
   space, one guest can rewrite another instance's channel page, trampoline, signal
   frame, rewrite cache, vfs Namespace or egress policy. No existing primitive prevents
   it: `AGENTS.md:60-68` explicitly says the guest can write everything except the hop
   thread's register-held state.
3. The channel ABI is a fixed shared-memory layout (128-byte `ChannelRequest`,
   frozen in `crates/qqbox/src/zig/abi.rs`). The Rust backend's chanPage is one
   4096-byte slot per guest process (`backend/channel.rs`); sharing it between
   instances would require a new per-instance keying scheme. The removed Go
   engine's `repFault==repMaps==12` / `repExecPrepare==repAdopt==13` message-kind
   collisions are historical evidence that a naive union misroutes replies.
4. The rewrite cache, vfs Namespace, egress allowlist and vnet registry are
   per-instance today (`VNET.md:116` says dnet is run-scoped). Sharing them without
   per-instance partitioning is cache poisoning and cross-instance file/network access.
5. A merged design must replace futex / `os_sync` / Windows events with in-process
   atomics, remove the exec/ELF note/window machinery, and drop the hop thread. That is
   a new isolation design, not a Go-to-Rust translation. It needs a replacement
   primitive (MPK/PKU, guard pages plus a verified in-process monitor, or a
   hardware-assisted compartment) before any code change.

## The regression-free Rust improvement

The Go backend keeps `stubLo`/`slotsAddr`/`stubHi` in package globals set once by
`adoptWindow`. The Rust control plane instead derives a `StubWindow` per instance
(`crates/qqbox/src/zig/abi.rs`), so a shared supervisor can plan distinct windows
without touching the two-page-table design the backstops depend on. This is the
cheap win: it removes a global collision without attempting a global SAS.

## What is delivered instead

- Rust supervisor can host many instances in one *process* (shared control plane,
  one daemon, one OCI/compose plan) while each instance keeps its own stub process and
  its own address space. This is the DX/UX win that motivates the request, without the
  isolation loss.
- The Zig stub ABI stays byte-identical. `crates/qqbox/src/zig/abi.rs` asserts the
  128-byte `chanPage` layout and all thirteen field offsets so a Rust supervisor can
  drive the committed stubs without changing Zig.
- A future single-address-space mode is gated behind a named isolation primitive and an
  adversarial witness on each host; it is not enabled by default.

## Evidence

- Rust ABI and backend: `crates/qqbox/src/zig/abi.rs`,
  `crates/qqbox/src/backend/{channel,filter,trap,hostop,guest,linux,macos,windows}.rs`.
  The Go-era files (`internal/sandbox/chanabi.go`, `chan.go`, `chanwin.go`,
  `hop_linux.go`, `run_linux.go`, `run_darwin.go`, `run_windows.go`,
  `stubproc_windows.go`) were removed with the Go tree; their line numbers are
  historical provenance only, not live citations.
- Committed freestanding Zig stubs: `stub/zig/seccomp.zig`,
  `stub/zig/landlock.zig`, `stub/macos/stub.zig`, `stub/windows/stub.zig`.
- `AGENTS.md`, `README.md`, `docs/OCI_MATRIX.md`. The Go-era `internal/sandbox/VNET.md:116` citation refers to the removed Go tree and is historical provenance only.
- Adversarial recon receipts: Zig ABI slice
  `1794973959109-7-9a13e21301646890` (43 Zig sources),
  `1794974411181-253-1a6544bd5e8fa11` (no single-address-space mode exists),
  `1794975103453-612-9572fd78437d5f62` (Windows mapping), and OCI slice
  `1794974373760-231-fae90a6b2abefafa` (per-instance daemon state).

## Status

Decided and recorded. The mutable `single-address-space-regression` is closed by this
document plus the live regression witnesses in `docs/OCI_MATRIX.md`. Reopen only if a
new isolation primitive is specified and witnessed.
