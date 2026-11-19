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
| Linux | per-thread seccomp guest filter (`RET_KILL_PROCESS` for out-of-policy calls) and a text-range `RET_TRAP` backstop that routes an un-rewritten syscall to the supervisor, Landlock, `CLONE_CHILD_CLEARTID`, `Pdeathsig`, `process_vm_readv/writev`, memfd shared channel |
| macOS | Seatbelt `(deny default)` allowlist that kills an un-rewritten `svc`, bootstrap-port drop, ad-hoc codesign, `os_sync` shared channel |
| Windows | LPAC AppContainer token plus job kill-on-close, x86-64 ProcessInstrumentationCallback `BACKSTOP_KILL`, auto-reset events, PE cache |

The Linux backstop is **TRAP-to-supervisor**, not kill: `stub/zig/seccomp.zig:181-190`
changed it so static rewriting is an optimization and a missed site is still serviced;
`internal/sandbox/config.go:15-20` documents that `ErrKilled` fires only for a genuinely
rejected call. macOS and Windows do kill. README/AGENTS/llms still claim kill-on-miss and
are stale. A Rust port must preserve this three-way asymmetry or it regresses JIT-heavy
guests (V8/Node/.NET), lazy indirect-branch site discovery and stray `0f 05` bytes in data.
Every boundary above is attached to a process, which is the load-bearing fact for this
decision.

The `Pdeathsig`/SIGIO lifeline kills the stub when its supervisor dies. The guest filter
admits only the channel, futexes, signals, clocks, a new thread, exit and the stdio fast
path; mapping memory, holding descriptors and forking happen only on the host-op thread.

## Why merging regresses

1. Every backstop above is attached to a process. A single address space means a single
   process, so a `RET_KILL_PROCESS` seccomp verdict or Seatbelt/LPAC denial kills *all*
   instances at once. One guest's missed syscall becomes a total outage.
2. The guest can write every byte of the stub. With several instances in one address
   space, one guest can rewrite another instance's channel page, trampoline, signal
   frame, rewrite cache, vfs Namespace or egress policy. No existing primitive prevents
   it: `AGENTS.md:60-68` explicitly says the guest can write everything except the hop
   thread's register-held state.
3. The channel ABI is a fixed shared-memory layout (`chanabi.go:126-146`, 128 bytes;
   `chanwin.go`, 1 MiB slots + 16 MiB signals + 256 KiB windows). It is indexed by slot,
   not by instance. Sharing it requires a new per-instance keying scheme, and the
   message-kind space already collides (`repFault==repMaps==12`,
   `repExecPrepare==repAdopt==13`), so a naive union misroutes replies.
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

- `internal/sandbox/chanabi.go:126-146`, `chan.go:18-195`, `chanwin.go:8-20`,
  `hop_linux.go:20-155`, `run_linux.go:62-200`, `run_darwin.go:249-252`,
  `run_windows.go:85-98`, `stubproc_windows.go:121-189`.
- `stub/zig/seccomp.zig:118-216`, `stub/zig/landlock.zig:24-39`,
  `stub/macos/stub.zig:167-223`, `stub/windows/stub.zig:1-25,51-55`.
- `AGENTS.md:60-68`, `README.md:182-196`, `internal/sandbox/VNET.md:116`.
- Adversarial recon receipts: Zig ABI slice
  `1794973959109-7-9a13e21301646890` (43 Zig sources),
  `1794974411181-253-1a6544bd5e8fa11` (no single-address-space mode exists),
  `1794975103453-612-9572fd78437d5f62` (Windows mapping), and OCI slice
  `1794974373760-231-fae90a6b2abefafa` (per-instance daemon state).

## Status

Decided and recorded. The mutable `single-address-space-regression` is closed by this
document plus the live regression witnesses in `docs/OCI_MATRIX.md`. Reopen only if a
new isolation primitive is specified and witnessed.
