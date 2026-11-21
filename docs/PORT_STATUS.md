# Go-to-Rust port status

This document is the honest boundary of the port in this tree. It is deliberately
explicit about what is done and what is not, so no one mistakes a compiling control
plane for a complete runtime.

## Done and witnessed

- A Cargo workspace of five crates (`qqbox`, `qqbox-cli`, `qqboxd`, `qqbox-mcp`,
  `qqbox-runtime`) builds cleanly with `cargo build --workspace` on darwin/arm64
  with rustc 1.98.1.
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
- A Docker-compatible daemon with 54 routes, real JSON, label filters, HostConfig
  and NetworkingConfig, fail-closed handling of unsupported security keys, stored
  cancellation so `DELETE --force` tears down, and bearer-token authz on every route.
- The Zig stub ABI frozen in `crates/qqbox/src/zig/abi.rs`: 128-byte `chanPage`,
  all 13 offsets, all eight committed stub blobs embedded with `include_bytes!`, and
  `validate_abi()` asserting the layout. No Zig source and no `.bin` blob changed.
- A typed supervisor control plane: bounds-checked ELF32/ELF64 parsing, syscall-site
  classification and trampoline patching, seccomp BPF filter construction, a typed
  syscall table, W^X and memory-cap enforcement, cancellation without timed polling,
  and a `RunPlan` that is computed and printed by `qqbox run`.
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

## Interfaces added for upstream-UX parity

The upstream laboon README documents five interfaces. Three existed in Rust
(`qqbox`, `qqboxd`, the `qqbox` library); two were added so the Rust tree offers
the same user experience rather than documenting commands that do not exist:

- `crates/qqbox-mcp` → `qqbox-mcp`: JSON-RPC 2.0 MCP server over newline-delimited
  stdio and streamable HTTP. `initialize`, `ping`, `tools/list`, `tools/call`.
  The `run` tool calls `qqbox::run_image` and returns `isError: true` with the
  real `UnsupportedPlatform` diagnostic; it does not fabricate a run. Seven
  control-plane tools are fully witnessed (invariants, routes, runtime-spec
  validate, image-config validate, ports, compose plan, version).
- `crates/qqbox-runtime` → `qqbox-runtime`: OCI runtime shim with
  `create`/`start`/`state`/`kill`/`delete`/`features`. `create` parses, validates
  and applies `config.json` fail-closed and writes real JSON state; `start`
  reports the native-backend diagnostic. Witnessed live.

These are control-plane integrations. Neither removes the need for the three
native guest-execution backends, and `go-removal` stays pending for that reason.

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

## Adversarial-synthesis corrections (witnessed after the sweep)

An eight-agent adversarial workflow (`wbets20lq`) re-derived every claim from
scratch. Four of its findings were real defects; all four are fixed here:

- `cargo test -p qqbox --lib` was red (7 passed, 2 failed). The two aarch64
  tests encoded `svc #0` as big-endian `d4 00 00 01`; the classifier correctly
  matches the little-endian in-memory bytes `01 00 00 d4`. The test vectors
  were corrected. `cargo test --workspace` now returns rc=0 (9 passed).
- `qqbox run-image` printed `status: registry request failed: … 401` but exited
  0, so callers mistook a failed pull for a successful run. It now returns
  `Err` and exits 1 while still printing the diagnostic. Witnessed rc=1.
- `README.md`, `zh-hant/README.zh-Hant.md`, `examples/qqbox/README.md` and
  `zh-hant/examples/qqbox/README.zh-Hant.md` documented the Go-era
  `qqbox build` / `qqbox -image` commands. The Rust CLI rejects both with rc=2;
  those lines now explicitly name the off-repo pre-removal Go runtime binary.
- `examples/qqbox/README.md` claimed "`apt-get install neofetch`-class failures
  are gone". On 26.04 `neofetch` is absent from the archive: rc=100
  `E: Unable to locate package neofetch` even after `apt-get update`. The docs
  now record that `fastfetch` 2.57.1 is the shipped substitute and that npm
  9.2.0 comes from the Node 18 payload, not apt.

The synthesis also confirmed three root causes and refuted three false ones:
the GDBus error is emitted by the `20packagekit` hook (not apt/dpkg), the real
defect is `execve` ignoring `S_ISUID` (the helper's 4754 mode is intact, so
`chmod` cannot fix it), and the 24.04 rc=100 installs were an unconfigured
`node-neo-async` dpkg state. It independently witnessed the Xubuntu 26 image,
the `xwallpaper` backdrop, vi/vim and the GNU toolchain, and the root-user
system bus. `dc`, `ed` and `info` were found missing; they install rc=0 and
are now in the image.

Still honestly unresolved from that sweep: no supervisor-side `S_ISUID` fix,
no real `docker run --runtime=qqbox` / containerd / Podman integration (only
`curl` against `qqboxd` and `qqbox-runtime` against a real bundle), no
translation-fidelity review of the zh-Hant docs, and the MCP `run` tool still
returns `isError: true` by design because no native backend exists.

## Inherited platform limits (from the security recon)

These belonged to the removed Go runtime and describe work a future Rust
backend must reproduce. They are
recorded so a port does not claim parity it does not have:

- Linux backstop is TRAP-to-supervisor; macOS Seatbelt and the Windows x86-64
  instrumentation callback kill. Windows arm64 has **no** callback backstop.
- macOS self-modifying `MAP_JIT` pages run unrewritten (`stub/macos/wx.zig:22-24`);
  only stub-placed traps are served. macOS non-PIE ET_EXEC guests run in an
  Hypervisor.framework VM, i.e. a second address space.
- Landlock ABI < 6 leaves an acknowledged SIGURG signal-scope hole.
- `MaxBytes` is not enforced on macOS, RWX is allowed by default for JIT engines,
  and Windows AF_INET loopback needs `WithNet`.
- The removed `stub/seccomp_test.go` expected allowlists omitted `rseq`/`getcpu`
  while the Zig `arch.guest` filter included them; that Go test was red at HEAD. The gm
  standing rule forbids editing test files; this is recorded, not patched.
- The removed Go tree had no production caller for `vfs.Policy` — confinement is structural
  (Namespace tree plus `openat2`). The Rust port does not invent a phantom Policy
  trait.

The Rust library reads no behavior knob from the environment. Its only host-path
lookups are `std::env::temp_dir()` for the default image cache and, when
`PullOptions.auth_config` is set, that path for registry auth; both come from
`Options` (`with_image_cache_dir`, `with_docker_config`) rather than being
hard-coded to `$HOME`.

## Not done — the remaining work

PARTIALLY PORTED (see [docs/NATIVE_BACKEND.md](NATIVE_BACKEND.md)). The Linux
trap-to-supervisor path is now real Rust (`crates/qqbox/src/backend/`): a memfd
chanPage, a seccomp-BPF `RET_TRAP` filter, a `SIGSYS` handler that saves aarch64
registers, and a Rust host-op dispatcher, all live-witnessed on a real Linux
6.17.7 aarch64 kernel. An untrapped control `getppid` returns the kernel's pid
51848 while the trapped one returns the Rust supervisor's sentinel 1364279887
with `trap_hits=1`. `sandbox::install()` on Linux is now real, and `qqbox backend exec-elf`
runs a real unmodified static aarch64 ELF under the Rust supervisor (witness:
kernel-only exit 1, supervised exit 42). A full guest
runtime — PIE/ET_DYN, trampolines, the ~205-syscall table, hop alias — is still
not ported, and macOS/Windows guest launch is not ported. `go-removal` stays
pending.

The low-level, per-host **native guest execution backend** is not fully ported. On Linux the
Go engine creates a memfd channel, execs the ET_EXEC/ET_DYN stub with inherited fds
3/4/5, installs the seccomp text-range trap, runs the host-op thread and serves ~200
emulated syscalls. On macOS it drives a Mach task / Seatbelt / Hypervisor.framework
stub; on Windows an LPAC AppContainer with the instrumentation callback. Those three
backends are the part that makes a Linux program actually run without a VM.

`qqbox::run` therefore prepares and validates the whole control plane and then
returns `Error::UnsupportedPlatform` with a precise diagnostic on this host. It does
not pretend to have executed a guest. `qqbox run <elf>` prints the `RunPlan` instead.

**All Go source and Go build tooling has been removed** at the user's explicit request
("replace the go code with rust and remove any go code"). `Cargo.toml` is the single
build entry point; `*.go`, `go.mod`, `go.sum`, `go.work`, `go.work.sum`, `build/`,
`cmd/`, `internal/` are gone. The committed freestanding Zig stubs and their build
scripts remain and are embedded with `include_bytes!` by `crates/qqbox/src/zig/abi.rs`.

**Honest consequence (updated):** the Linux seccomp/hop backend IS now ported to
Rust (see docs/NATIVE_BACKEND.md); macOS Mach/Seatbelt/HVF and Windows LPAC are
not. The Go sources are gone. `qqbox run` still returns
`Error::UnsupportedPlatform` for the general case, but `qqbox backend exec-elf`
executes a real unmodified static aarch64 ELF under the Rust supervisor from a
from-source build. The live desktop on VNC 5905 was booted with the pre-removal
Go runtime binary, preserved outside the repository in
`/Users/dylanwong/.gm-work/qqbox/ping-impl/laboon-pingfix`. `go-removal` stays
pending until all three backends pass the historical regression corpus.

The Rust `Platform` trait now exists (`backend/mod.rs`) and the Linux
memfd/chanPage/seccomp/`SIGSYS` launch contract is implemented and witnessed.
Remaining work: reproduce the macOS Mach/Seatbelt/HVF and Windows LPAC launch
contracts, port PIE/ET_DYN and trampoline islands, and serve the full ~205-syscall
table through the 128-byte channel. `go-removal` closes only after all three
backends pass the historical regression corpus on their own hosts.

### Sizing and fork identity

The recon census measured the removed Go tree: 445 Go files / 85,620 lines, of which
`internal/sandbox` alone was 322 files / 69,277 lines (81%). That package is the real
porting unit; `oci`, `vfs`, `rewrite`, `build` and the four `cmd/*` modules were thin
layers above it. The Rust workspace covers the control plane and OCI/Compose surface,
and the `Platform` trait now exists. The Linux memfd/seccomp/`SIGSYS`
trap-to-supervisor path is ported and witnessed (see docs/NATIVE_BACKEND.md); the
macOS and Windows launch contracts and the full ~205-syscall table remain unported.

The fork is `dywongcloud/literbox`, rebranded **Tencent qqbox**. All Rust crates,
README, `AGENTS.md`, `llms.txt`, `Makefile`, CI and `.goreleaser.yaml` use `qqbox`.
Traditional Chinese documentation lives in `zh-hant/`. The upstream Apache-2.0
attribution is preserved in LICENSE and COPYRIGHT.

The old `purego` claim in `AGENTS.md` is moot: there is no Go source left. The
historical host-call layer was `x/sys/unix` (Linux), `goffi`/libffi (macOS),
`process_vm_readv`/`writev` for guest memory, and the Linux host-op thread.


## Platform notes

- Single address space across instances: rejected with evidence; see
  `docs/SINGLE_ADDRESS_SPACE.md`. One address space per instance is preserved.
- Linux backstop is TRAP-to-supervisor, macOS/Windows kill. README, AGENTS.md and
  llms.txt claimed kill-on-miss for all three; that drift is corrected here and in
  the rewritten docs.

## Residual limitations (disclosed, not claimed as done)

- `go-removal`, `migration-guest-memory`, `backend-macos-mach`,
  `backend-windows-lpac` and `backend-linux-seccomp` PRD rows stay **pending**.
  The Linux backend runs a real unmodified static aarch64 ET_EXEC under the Rust
  supervisor; PIE/ET_DYN, trampoline islands, `PROT_R` hop, signal frames and the
  full syscall table are not ported. macOS Mach/Seatbelt/HVF and Windows LPAC
  launch contracts do not exist in Rust.
- Guest physical memory, vCPU registers and dirty-page bitmaps are NOT capturable:
  qqbox has no VMM and no guest-RAM API. `qqbox migrate` transfers only supervisor
  control-plane state.
- `cargo check --target x86_64-pc-windows-msvc`, `x86_64-unknown-linux-gnu` and
  `x86_64-unknown-linux-musl` return rc=101 on this macOS host: `ring`'s C build
  needs a cross C compiler and sysroot that are not installed. `aarch64-apple-darwin`
  and `aarch64-unknown-linux-musl` pass check + clippy -D warnings.
- `serde_yaml 0.9.34+deprecated` is still the YAML parser for Compose and the OCI
  runtime spec. It builds and parses correctly offline; no maintained fork is
  available in the local registry cache. Replacing it would require a network
  fetch and a fresh witness pass.
- The live Xubuntu 26 desktop on VNC 5905 still boots with the pre-removal Go
  runtime binary preserved off-repo; the Rust `qqbox run` path is not the desktop
  launcher. Chromium keeps `--no-sandbox --single-process --no-zygote` because all
  42 live sandbox variants failed; the launcher drops root to uid 911 via
  `setpriv` instead, and the unsupported-flag banner is deliberately not hidden.
- The live Xubuntu 26 desktop's native XFCE terminal still prints
  `bash: initialize job control: no job control in background: Bad file
  descriptor` on every interactive shell start. The off-repo pre-removal Go
  runtime's PTY emulation does not implement `TIOCSPGRP`, so `tcsetpgrp`
  returns `EBADF`. Job control is initialized before any rcfile: `SHELLOPTS=`,
  `set +m` in the rcfile and `setsid --ctty` were witnessed in a real guest
  PTY, and `setsid --ctty` was additionally typed into the live VNC 5905
  terminal. Each leaves the line in place. The shell is fully usable. Earlier documentation claimed this
  error was fixed; that claim is retracted here.
- Zig 0.16.0 is installed; the 43 Zig sources were authored against 0.13.0. The build
  scripts now prefer `ZIG` then PATH and warn on a version mismatch; migrating all
  sources and rebuilding the eight committed blobs is a separate, gated change.
