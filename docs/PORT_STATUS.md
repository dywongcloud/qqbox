# Go-to-Rust port status

This document is the honest boundary of the port in this tree. It is deliberately
explicit about what is done and what is not, so no one mistakes a compiling control
plane for a complete runtime.

## Done and witnessed

- A Cargo workspace (`crates/laboon`, `crates/laboon-cli`, `crates/laboond`) builds
  cleanly with `cargo build --workspace` on darwin/arm64 with rustc 1.98.1.
- The full typed configuration surface: `Options` (every `With…` option), `Config`
  (55 fields), image config, runtime-spec config, compose spec, labels, ports,
  volumes, network mode, capabilities, resource limits, seccomp and AppArmor profiles,
  healthchecks, restart and log policies.
- The OCI image pipeline: reference parsing, registry manifest/index and blob URLs,
  auth from `~/.docker/config.json`, gzip+tar layer unpack with whiteouts, opaque
  dirs, path confinement, skipped device/FIFO nodes, dropped setuid/setgid and an
  enforced unpack cap, content-addressed cache, `LookPath` through the guest root.
- Ports and port forwarding: short and long syntax, ranges, IPv4/IPv6 host binding,
  tcp/udp/sctp, published-only forwarding plans, `/etc/hosts` and `/etc/resolv.conf`.
- Network policy: loopback-only default, `no_net`, supervisor-answered DNS, domain
  and subdomain allowlists, CIDR allowlists, destination-port allowlists, private /
  link-local / metadata address rejection, and an in-process loopback socket table.
- The vfs Namespace as the only path chokepoint with mount shadowing, `..` and
  absolute-symlink rejection, read-only mounts, and synthetic `/proc`, `/sys`, `/dev`.
- Docker Compose: JSON and YAML, short/long ports and volumes, list/map environment
  and labels, `depends_on` with health conditions, networks, volumes, healthchecks,
  restart, capabilities, security options, and a dependency-ordered instance plan.
- A Docker-compatible daemon with 48 routes, real JSON, label filters, HostConfig
  and NetworkingConfig, fail-closed handling of unsupported security keys, stored
  cancellation so `DELETE --force` tears down, and bearer-token authz on every route.
- The Zig stub ABI frozen in `crates/laboon/src/zig/abi.rs`: 128-byte `chanPage`,
  all 13 offsets, all eight committed stub blobs embedded with `include_bytes!`, and
  `validate_abi()` asserting the layout. No Zig source and no `.bin` blob changed.
- A typed supervisor control plane: bounds-checked ELF32/ELF64 parsing, syscall-site
  classification and trampoline patching, seccomp BPF filter construction, a typed
  syscall table, W^X and memory-cap enforcement, cancellation without timed polling,
  and a `RunPlan` that is computed and printed by `laboon run`.
- Security invariants enumerated in `security::audit()` and enforced by typed checks.
- Evidence-based decisions in `docs/SINGLE_ADDRESS_SPACE.md` and
  `docs/OCI_MATRIX.md`.
- Issue fixes: the aarch64 `svc #0` classifier signature was wrong (it looked for
  the big-endian byte order) and now finds and patches real sites; the four Zig build
  scripts no longer pin an absent Zig 0.13.0 path; the process cancellation watcher no
  longer uses `thread::sleep` as a correctness mechanism; the runtime-spec `apply`
  fails closed on unsupported security fields instead of silently dropping them.
- Adversarial fixes: Compose long-syntax port targets no longer truncate to `u16`,
  invalid port entries and numeric `published` values are handled, malformed registry
  hostnames and metadata/link-local published addresses are rejected, wildcard forward
  spoofing is denied, Docker's PascalCase HostConfig keys are parsed and unmodelled
  keys are surfaced as `Warnings`, isolation-breaking create requests (`Privileged`,
  host networking, `SYS_ADMIN`, `seccomp=unconfined`) are rejected, `force=10` no
  longer force-deletes a running container, the daemon has no unauthenticated default,
  and every security primitive (`enforce_wx`, `enforce_memory_cap`, `enforce_mount`,
  `enforce_egress`, `backstop_verdict`) is wired into the live `prepare` path.

## Adversarial verification findings still open

Four independent adversarial verifiers attacked the built artifact. The live findings
that were fixed above and in the two follow-up commits are: aarch64 `svc #0` signature,
Compose long-syntax port truncation and silent port skips, numeric `published`, bad
registry hostnames, wildcard forward spoofing, metadata/link-local published host
addresses, dead security enforcement functions, an unauthenticated default daemon,
`force=10` substring bypass, isolation-breaking HostConfig keys, PascalCase HostConfig
keys silently dropped, a wrong-schema image-config body, and a mixed JSON/prose CLI
response.

Still open, recorded rather than hidden:

- `runtime-spec validate --advisory` reports unsupported constructs but accepts the
  spec; strict mode rejects them. Some OCI runtime-spec fields (consoleSize, username,
  inheritable/ambient capabilities, rlimits soft, seccomp content, namespace paths,
  memory swap/reservation, rootfsPropagation, mountLabel) are parsed but not mapped
  into `Config`. `RLIMIT_NOFILE.hard` is currently mapped to `pids_limit`, which is
  semantically wrong.
- Docker create bodies still drop some HostConfig fields. They are now surfaced in the
  `Warnings` array of the 201 response rather than silently ignored; `Devices`,
  `Mounts` long syntax, `Sysctls`, `Ulimits`, `Init`, `Annotations` and the mode
  fields are typed but not all applied to `Config`.
- `daemon-probe` is an in-process diagnostic and always exits 0; the HTTP status is
  printed in stdout. It is not a network authz test.
- No live guest execution exists on this host, so no host-file read, RWX mapping or
  metadata connection can be demonstrated end to end. All security findings are
  control-plane acceptance/rejection results.

## Not done — the remaining work

The low-level, per-host **native guest execution backend** is not ported. On Linux the
Go engine creates a memfd channel, execs the ET_EXEC/ET_DYN stub with inherited fds
3/4/5, installs the seccomp text-range trap, runs the host-op thread and serves ~200
emulated syscalls. On macOS it drives a Mach task / Seatbelt / Hypervisor.framework
stub; on Windows an LPAC AppContainer with the instrumentation callback. Those three
backends are the part that makes a Linux program actually run without a VM.

`laboon::run` therefore prepares and validates the whole control plane and then
returns `Error::UnsupportedPlatform` with a precise diagnostic on this host. It does
not pretend to have executed a guest. `laboon run <elf>` prints the `RunPlan` instead.

**The Go sources are deliberately still in the tree** (`laboon.go`, `options.go`,
`internal/`, `cmd/`, `build/`, `go.work`). Removing them now would delete the only
working guest runtime without a replacement — a regression the request explicitly
asked to avoid. The PRD row `go-removal` stays open for that reason. The Rust build
is additive: `Cargo.toml` is the new build entry point and the Go tree is untouched.

To finish the port: port `internal/sandbox/run_linux.go`, `run_darwin.go` and
`run_windows.go` into a Rust `Platform` trait (the LiteBox North/South split), keep the
committed Zig stubs byte-identical, reproduce the memfd/Mach/LPAC launch contracts,
then serve the syscall table through the 128-byte channel. Only after that backend
passes the existing Go regression corpus on each host should the Go tree be removed.

## Platform notes

- Single address space across instances: rejected with evidence; see
  `docs/SINGLE_ADDRESS_SPACE.md`. One address space per instance is preserved.
- Linux backstop is TRAP-to-supervisor, macOS/Windows kill. README, AGENTS.md and
  llms.txt claimed kill-on-miss for all three; that drift is corrected here and in
  the rewritten docs.
- Zig 0.16.0 is installed; the 43 Zig sources were authored against 0.13.0. The build
  scripts now prefer `ZIG` then PATH and warn on a version mismatch; migrating all
  sources and rebuilding the eight committed blobs is a separate, gated change.
