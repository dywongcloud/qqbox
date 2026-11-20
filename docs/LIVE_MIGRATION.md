# Live migration in qqbox

**Status: implemented and live-witnessed for the supervisor-owned control plane.
Guest memory/CPU live migration is explicitly NOT implemented and is not
possible in this tree — see "The boundary" below.**

qqbox is not a VM. It runs an unmodified Linux ELF in a host process and
rewrites only its syscall instructions; the supervisor owns the data plane and
the vfs Namespace is the only filesystem. Firecracker, by contrast, is a VMM:
it owns guest physical memory, vCPU register files, a dirty-page bitmap and a
`pause`/`resume` switch. That difference defines what "live migration" can mean
here.

## 1. What Loophole Labs actually built (verified)

Studied: [loopholelabs/firecracker @ `main-live-migration-pvm`](https://github.com/loopholelabs/firecracker/tree/main-live-migration-pvm),
head `7f6c070fa09c32eebbbfaeac6c61a96b05cf91ee` (2025-07-31), 44 commits ahead /
1749 behind upstream `main`. One substantive implementation commit:
`cfed8de59a74` (2024-08-31) "feat: Add live migration and PVM support" — 370
added / 82 removed across 16 files. The three-dot diff is 18 files with **no new
Rust source files**; the HTTP route table, device serialization, vsock,
userfaultfd backend and pause/resume are all unchanged.

**There is no migration protocol in that fork.** It adds *primitives*:

| primitive | implementation |
| --- | --- |
| shared guest RAM | `vstate/memory.rs`: `snapshot_file(shared: bool)` chooses `MAP_NORESERVE\|MAP_SHARED` instead of `MAP_PRIVATE`, so the kernel writes dirty pages back to a file-backed region as the VM runs |
| explicit flush | `GuestMemoryExtension::msync()` → `libc::msync(MS_SYNC)`; `vm.rs`: `Vm::snapshot_memory_to_file` for new `SnapshotType::Msync` / `MsyncAndState` |
| state/memory split | `persist.rs`: `Msync` saves **no** state file, `MsyncAndState` saves both; `snapshot/mod.rs` prepends a `u64` length before header + bincode + CRC64 |
| PVM | 8 PVM MSRs `0x4b56_4df0`–`df7` added to `SERIALIZABLE_MSR_RANGES`; `set_tsc_khz` becomes a no-op; CPU-model check disabled |
| API | **no new endpoints**: `PUT /snapshot/create` (`snapshot_type: Full\|Diff\|Msync\|MsyncAndState`), `PUT /snapshot/load` (new `shared` field), `PATCH /vm` `Paused\|Resumed` |

The transfer itself is left to an **external orchestrator** copying two plain
files; there is no socket, TCP, vsock or convergence loop in the fork. KVM dirty
logging (`KVM_MEM_LOG_DIRTY_PAGES`, `get_dirty_log`, `dump_dirty`) is inherited
upstream and used only by `Diff` — `Msync` bypasses it. Known defects the fork
itself documents: `msync()`'s return value is discarded, the new length prefix
breaks `snapshot-editor`, vsock/queue restore fixes were reverted
(`f60a14959632`) because they broke VM resume, and it is 1749 commits behind
upstream. PVM requires the out-of-tree `virt-pvm/linux` host kernel.

### What we took, and what we did not

We took the **architectural discipline**, not the payload:

- a versioned, integrity-checked state file with an explicit length prefix
  (their CRC64 + bincode → our SHA-256 + canonical JSON);
- a clean separation between durable spec state and live runtime state
  (their `Msync` vs `MsyncAndState` → our four disjoint `MigrationState`
  partitions and explicit `Boundary::GuestExecution::NotCapturable`);
- an external, capability-gated orchestrator rather than an in-VM data plane;
- the `PUT /snapshot/create` / `PUT /snapshot/load` shape → our
  `/qqbox/v1/migrate/checkpoint` / `restore`.

We did **not** copy a pre-copy loop, because the fork has none. Our iterative
`delta` rounds are our own design, applied to supervisor-owned control-plane
values; they are emphatically not guest dirty-page tracking. Where the fork
relies on kernel writeback of `MAP_SHARED` guest RAM, qqbox has no guest RAM at
all — so that primitive is inapplicable. The fork's `Msync` path cannot be
ported, and claiming otherwise would be a category error.

### The layer above the fork: drafter + silo (verified)

Loophole Labs' actual live migration is **not** in the Firecracker fork; it is
in [`loopholelabs/drafter`](https://github.com/loopholelabs/drafter) (HEAD
`c963a0e4938c`) over
[`loopholelabs/silo`](https://github.com/loopholelabs/silo) (HEAD
`6acfd0038005`). Verified phases, from `pkg/common/migrations.go:MigrateToPipe`
and `pkg/peer/peer.go:MigrateTo`:

1. `protocol.NewRW` over one bidirectional `io.ReadWriter` — a plain TCP
   `net.Dial` / `net.Listen` (default `localhost:1337`) in `cmd/drafter-peer/net.go`.
   Destination gets `DeviceGroupInfo` + per-device `DevInfo`.
2. `dg.MigrateAll(concurrency=1024)`: bulk **pre-copy while the VM runs**.
3. Iterative **dirty rounds** (`NewDirtyManager`, `dg.MigrateDirty`):
   `PreGetDirty` → `VMStateMgr.Msync()` → `FlushData` →
   `CreateSnapshot(snapshot_type: "Msync")` **without pausing**; `dirtytracker.Remote.Sync()`
   yields a dirty-block bitfield. Convergence at `len(blocks) < 200` for 5
   cycles, capped at 20 cycles, 500 ms throttle.
4. `SuspendAndMsync()`: `BeforeSuspend` RPC → `PATCH /vm Paused` →
   `PUT /snapshot/create "MsyncAndState"` → `AfterSuspend`.
5. `authTransfer` → `EventCustom`; source sends `EventCompleted` and shuts down.
6. Destination resumes immediately; with post-copy (default) missing blocks are
   pulled on demand via Silo `waitingcache` (`NeedAt` / `DontNeedAt`).
   `--disable-postcopy-migration` instead waits for full completion.

State is packaged as six Silo block devices — `vmlinux`, `rootfs.ext4`,
`state.bin` (`MicrovmState`), `memory.bin` (guest RAM), `config.json`,
`oci.ext4` — each with a CoW `overlay` over an immutable `Base`, 64 KiB blocks,
exposed on the destination via `unix.Mknod` inside a fresh jailer chroot.
Silo's wire protocol is a bidirectional packet protocol with `DirtyList`,
`NeedAt`, `DontNeedAt`, `WriteAtWithMap`, `Hashes`, `Event`. Guest RPC
`BeforeSuspend` / `AfterResume` runs over panrpc on AF_VSOCK (port 26).
Drafter's README reports sub-100 ms same-DC and ~500 ms Europe↔North-America
migrations; the design source is F. Pojtinger's thesis *Efficient
Synchronization of Linux Memory Regions over a Network* (2023).

**Correcting an earlier draft of this document:** the first version attributed
pre-copy dirty-page migration to the Firecracker fork itself. That is wrong —
the fork supplies only the `Msync` primitives; drafter implements the protocol
in userspace via `MAP_SHARED` + `msync` + Silo block-level dirty tracking. There
is **no KVM dirty log** (`KVM_GET_DIRTY_LOG`) and **no userfaultfd** in drafter's
path: `LoadSnapshot` sets `track_dirty_pages=false` and `Diff` is never
requested. The correction is recorded in `.gm/prd-log.jsonl`; history is not
rewritten.

### What this means for qqbox

The drafter protocol shape — bulk pre-copy, bounded iterative dirty rounds, a
single suspend/commit barrier, on-demand post-copy — is the closest external
analogue to our `delta` rounds and `Commit` frame. But drafter's dirty set is a
bitmap of 64 KiB memory blocks; ours is a set of four control-plane partitions.
Both are finite convergence loops over a well-defined state ADT. That is the
extent of the analogy: drafter still needs a VMM's guest RAM, which qqbox does
not have. We did not copy drafter's transport (it has no built-in auth/TLS and
delegates that to the `io.ReadWriter`); our capability gate is in the protocol.

## 2. The boundary (stated first, on purpose)

The three native guest backends — Linux memfd/seccomp/hop, macOS
Mach/Seatbelt/HVF, Windows LPAC — are **not ported to Rust** in this tree (the
inherited `go-removal` PRD row remains pending). Verified from source:
`sandbox::run::run` returns `Error::UnsupportedPlatform` on every host,
`sandbox::install` does the same, and the Rust side never allocates a memfd,
Mach port, HVF VM or shared channel page. The eight committed Zig `.bin` stubs
are `include_bytes!` data only.

Consequently there is **no** guest physical memory, vCPU register file,
dirty-page bitmap, in-flight syscall, signal frame, real OS fd or TCP
connection state, and no VMM to suspend. `GuestState` in `sandbox/mod.rs` is a
four-field struct created and dropped inside `prepare`. Any claim of
Firecracker-style guest memory migration from this codebase would be fabricated.
`MigrationState::boundary.guest_execution` records
`GuestExecution::NotCapturable` so the ADT itself cannot be misread, and
`migrate::features()` reports the same boundary plus the explicit
`unsupported` list.

What **is** migrated is the state the Rust supervisor actually owns:

| partition | contents | source |
| --- | --- | --- |
| `spec` | args, env, stdio, user, security profile, resource limits, healthcheck, restart/log policy, image, entrypoint, cmd, labels, hostname | `Config` |
| `fs` | root, upper, mounts, devices, volumes, resolved vfs Namespace mount table | `Config` + `vfs::Namespace` |
| `chan` | pid, brk, mapped bytes, fd table, chanPage geometry and stub window base | `GuestState` + `zig::abi` constants |
| `net` | net flags, allow lists, exposed/published ports, network mode, DNS, extra hosts, derived resolved bindings | `Config` + `net::ports::expand` |

The partitions are **disjoint by construction**: `MigrationState::capture`
destructures `Config` exhaustively with no `..`, moving each of its fields into
exactly one partition. Adding a field to `Config` is a compile error until it is
assigned — a Poka-Yoke guard against silently losing state (`Γ_t ⊇ Γ_{t-1}`).

## 3. Algebra and invariants

`capture` and `apply` are pure morphisms:

- `capture(apply(s)) == normalize(s)` — verified live by `qqbox migrate verify`
  and again on the destination before it acks.
- `normalize(normalize(s)) == normalize(s)` — `f ∘ f ≡ f`. Sorts/dedups the
  unordered sets and pins the version and boundary.
- `apply_delta(apply_delta(s, d), d) == apply_delta(s, d)` — deltas assign,
  never increment.
- `digest(s) == digest(normalize(s))` — SHA-256 over canonical JSON.
- `delta(s, s)` is empty.

`normalize` is deterministic and side-effect free; `digest` is a pure function.
No ambient state, no env reads, no global mutable counters, no hidden mutation.

## 4. Wire protocol

```
MAGIC "QQBM" | wire_version u8 | kind u8 | length u32 BE | payload | SHA-256
```

- `length` is checked against `MAX_FRAME` (16 MiB) **before** allocation.
- The digest covers header + payload and is compared in constant time.
- `read_exact` everywhere; every failure is a typed `MigrationError::Wire`.
- Kinds: `Hello`, `State`, `Delta`, `Commit`, `Ack`, `Reject`.
- `read_expect(kind)` refuses type-confused frames before decoding.
- All readers and writers are explicit arguments — `Effects ≡ IO_Monad`.

## 5. Affine typestate and zero-trust

`Session<Idle> → Prepared → Streaming → Committed → Restored`. Each transition
**consumes `self`**, and `Session` is deliberately not `Clone` or `Copy`, so a
phase cannot be reused and a migration cannot be committed twice. Every
privileged transition takes a `Capability` — the SHA-256 of a caller-supplied
secret, compared in constant time. The secret is never a literal in source and
never appears in a frame; only its digest does.

The daemon endpoints are additionally behind the existing bearer token:

| route | behaviour |
| --- | --- |
| `GET /qqbox/v1/migrate/features` | version, phases, partitions, laws, boundary, unsupported list |
| `GET /qqbox/v1/migrate/state` | last checkpoint and digest, or 404 if none |
| `POST /qqbox/v1/migrate/checkpoint` | `{"container":"<id>"}` → capture from a real container's `Config` |
| `POST /qqbox/v1/migrate/restore` | `{"capability":"<hex>","state":<MigrationState JSON>}` → 403 on wrong capability, 200 + digest on a valid restore |

## 6. CLI

```bash
qqbox migrate features
qqbox migrate checkpoint --config config.json --out src.frame
qqbox migrate verify      --input src.frame
qqbox migrate restore     --input src.frame --out restored.json
qqbox migrate serve       --secret S --port-file p --ready-fifo f --out dest.frame
qqbox migrate send        --secret S --endpoint 127.0.0.1:P --input src.frame \
                          --delta delta1.json --final-out source-final.frame
```

`serve` binds an ephemeral port, publishes it, then writes `READY` to a FIFO.
The FIFO write blocks until a reader opens it: readiness is event-driven, with
no sleep, timeout or periodic recheck used as a correctness mechanism (gm
standing rule).

## 7. Live witness (same turn as the implementation)

Scratch drivers: `/Users/dylanwong/.gm-work/qqbox/migration-witness-driver.sh`
and `/Users/dylanwong/.gm-work/qqbox/migration-daemon-witness-driver.sh`.

| check | result |
| --- | --- |
| four algebraic laws | `capture_apply_identity=true`, `normalize_idempotent=true`, `digest_canonical=true`, `delta_idempotent=true` |
| corrupted frame | rejected, `integrity digest mismatch` |
| truncated frame | rejected, `digest read: failed to fill whole buffer` |
| oversized length | rejected, `frame 16777217 exceeds MAX_FRAME 16777216` |
| two real processes + real TCP socket | `send_rounds=1`, source digest == destination digest |
| state parity after pre-copy | `source_final_equals_dest=True`, `original_differs_after_delta=True` |
| wrong capability over TCP | receiver `migration capability rejected`, sender rc=1 |
| daemon without bearer token | 401 |
| daemon state before checkpoint | 404 |
| daemon checkpoint → state | 200, digests match |
| daemon restore wrong capability | 403 |
| daemon restore correct capability | 200, digests match |
| advertised migration routes | all 4 present |

## 8. What remains

- `go-removal` (inherited, intentionally pending): porting the three native
  guest backends to Rust is the prerequisite for any guest-memory migration.
- `migration-guest-memory` (this session, intentionally pending): guest RAM,
  vCPU registers, dirty-page tracking, in-flight syscalls, real fd/TCP state
  and guest quiesce. Requires a VMM or an equivalent guest-memory API.
- The extracted OCI rootfs bytes are content-addressed and re-pullable; this
  implementation migrates the spec that names them, not the layer tar bytes.

## 9. Design rationale (orthogonality)

- **Systems**: pure state morphisms separated from the `IO_Monad` boundary;
  separation logic over four disjoint partitions.
- **Scope**: strict lexical bounds, capability-passing, no ambient/global/
  implicit state, every reader/writer parameterized.
- **Alignment**: total functions (`Result`, no `unwrap`/`panic` in the
  migration path), constructive round-trip laws, no speculative claims about
  guest state.
