# Performance

We rewrite only syscall instructions; all other guest code runs at full native
speed (no DBT, no instrumentation). So slowdown is a function of syscall
density plus a fixed per-process startup, not a flat multiplier.

## Historical measurements (removed Go runtime, NOT this Rust tree)

The table below is quoted from the upstream Go runtime's benchmark record. It is
**not** a measurement of this Rust-only workspace: the Rust tree cannot yet run
those workloads end to end (the Linux seccomp/trap backend currently serves a
small witnessed syscall subset, and macOS/Windows guest launch is unported). No
Rust-side performance number is claimed until it is measured live.

| workload                              | native  | sandbox | slowdown |
|---------------------------------------|---------|---------|----------|
| CPU-bound (0.64s compute loop)        | 0.637 s | 0.644 s | 1.01x    |
| mixed, typical                        | --      | --      | ~1.2-2x  |
| I/O-bound (64 MB via 4 KB reads + sum)| 0.032 s | 0.146 s | 4.6x     |
| syscall storm (2M getppid)            | 0.178 s | 0.627 s | 3.5x     |
| startup (noop static process)         | --      | 18 ms   | fixed    |

Per-syscall overhead: ~224 ns for a trivial emulated syscall (getppid), ~6 us
for a 4 KB file read (channel + cross-process data copy), and ~native for hot
stdio (served in the stub on the held fd, no round trip).

## Rule of thumb

slowdown ~= 1 + (18 ms startup + syscalls x per-syscall-overhead) / native_time

- CPU-heavy / long-running: ~1.0-1.2x (CPU native, startup amortized).
- Mixed average process: ~1.2-2x.
- I/O-heavy (small reads/writes): ~2-5x (each syscall copies data across the
  process boundary; in-stub fast paths cut this).
- Short-lived (<10 ms): dominated by the ~18 ms startup.
- Pathological syscall storm: ~3.5x ceiling.

This is the same overhead class as gVisor (syscall-interception sandbox):
near-native CPU, ~2x syscall-heavy, higher on I/O.

## macOS

The macOS port is the same overhead class: the shared-memory channel spin
round-trip measures ~90 ns on an M4 (vs a ~90 ns native getppid), the
Mach-exception path (~5.5 us) is a rare backstop not the hot path, and hot I/O
stays in the stub. See macos/README.md for the primitive measurements.

## vs Docker Desktop (macOS)

On macOS (and Windows) Docker runs containers inside a Linux VM; qqbox uses no
VM -- the guest runs on the host CPU and the supervisor serves its syscalls in
the host process. Measured on an Apple M4 (macOS 26.5), warm (Docker Desktop's
VM already running, images pre-pulled), median end-to-end wall-clock:

| warm workload                          | Docker Desktop | qqbox | speedup |
|----------------------------------------|----------------|--------|---------|
| start a container (`alpine` `echo hi`) | 187 ms         | 12 ms  | 15x     |
| start a Node process (`node -e 0`)     | 200 ms         | 129 ms | 1.5x    |
| CPU loop (`node`, 1e8 adds)            | 559 ms         | 525 ms | ~1x     |

Startup is the win: qqbox has no VM to cross and no container to
create/start/remove, so per-task isolation is near-instant -- what an agent
spinning up many short tasks needs. CPU-bound work ties, because Docker
Desktop's VM also runs ARM code near-native on Apple Silicon. The numbers are
macOS; Docker Desktop on Windows is the same VM-based architecture (not yet
measured here). Method: `docker run --rm <img> <cmd>` vs `qqbox -image <img>
<cmd>`, 15 iterations each (6 for the CPU loop), median.

## Levers

In-stub fast paths for more syscalls (not just stdio), request batching, spin
tuning, and cutting startup (mostly the one-time rewrite of static libc, which
the on-disk plan cache already amortizes across runs).
