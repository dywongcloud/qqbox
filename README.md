# literbox

A fork of [laboon](https://github.com/samyfodil/laboon): run untrusted Linux
programs and OCI containers safely, on Linux, macOS and Windows, with **no VM and no
second kernel**. Guest machine code runs natively on the host CPU; only its
`syscall`/`svc` instructions are redirected into a supervisor that emulates the
Linux ABI.

This tree is mid-port to Rust. `Cargo.toml` is the build entry point; the Go sources
remain as the working native guest runtime until the Rust backends catch up. The exact
boundary is in [docs/PORT_STATUS.md](docs/PORT_STATUS.md) — read it before assuming a
`laboon run` executes a guest.

## Build

```sh
cargo build --workspace
cargo run -p laboon-cli -- version
cargo run -p laboon-cli -- invariants
```

Requirements: Rust 1.98 or newer. The committed freestanding Zig stubs
(`stub/*.bin`, `stub/macos/*.bin`, `stub/windows/*.bin`) are embedded with
`include_bytes!`, so Zig is not needed to build. To rebuild them:

```sh
ZIG=/path/to/zig-0.13.0/zig make stubs   # see stub/build.sh and the version warning
```

The Zig build scripts now prefer `ZIG`, then `zig` on PATH, and warn when the version
is not 0.13.0. The 43 Zig sources were authored against 0.13.0; a full 0.16 migration
and blob rebuild is a separate gated change.

## CLI

```
laboon version                       host, arch, supported OCI version
laboon invariants                    the enforced security invariants
laboon abi                           validate the 128-byte Zig channel ABI
laboon address-space                 the single-address-space decision
laboon run <program> [args...]       parse + rewrite + plan a guest ELF
                                     --root, --net, --no-net, --allow-domain,
                                     --publish, --volume, --label, --no-rewrite,
                                     --reject-rwx, --max-memory
laboon run-image <ref>               pull and prepare an OCI image
laboon config-validate <file>        print every OCI image-config construct
laboon oci-inspect <file>            full image config + construct count
laboon ports-check <spec>...         parse publishes and print the forward plan
laboon compose-plan <compose.yaml>   lower a Compose file to an instance plan
laboon compose-validate <file>       validate services, ports, volumes, depends_on
laboon runtime-spec features         advertised OCI runtime-spec features
laboon runtime-spec validate <file>  apply config.json, fail closed on unknowns
laboon image-plan <ref>              registry/manifest/blob URLs and platform
laboon network-plan <compose.yaml>   networks, aliases, forwarding, /etc files
laboon daemon-probe <path>           exercise one Docker-API route in-process
laboon daemon --listen <addr>        start the Docker-compatible daemon
```

`laboond --listen 127.0.0.1:2375 --token <secret>` starts the daemon as a standalone
binary. If no token is given, one is generated and printed at startup. Every route
requires `Authorization: Bearer <secret>` — there is no unauthenticated default.

`runtime-spec validate <file>` fails closed on unsupported security constructs;
`--advisory` prints the report without failing. `examples/runtime-spec-minimal.json`
is the strict-mode fixture.

## What the Rust code covers

- **OCI image config**: `Entrypoint`, `Cmd`, `Env`, `WorkingDir`, `User`,
  `ExposedPorts`, `Volumes`, `Labels`, `StopSignal`, `Healthcheck`, `Hostname`,
  `Domainname`, attach/tty flags, `ArgsEscaped`, `Shell`, `OnBuild`, architecture,
  OS, variant, `os.version`, `os.features`, created, author, history, `rootfs.diff_ids`.
- **OCI runtime-spec `config.json`**: root (incl. `readonly`), process (user, args,
  env, cwd, capabilities, rlimits, no-new-privileges, AppArmor), mounts with options
  and propagation, hostname/domainname, hooks, annotations, and the `linux` section.
  Unsupported security fields are **rejected with a diagnostic**, never silently
  dropped.
- **Ports**: `80`, `8080:80`, `127.0.0.1:8080:80/tcp`, `3000-3002:3000-3002/udp`,
  bracketed IPv6, `/sctp`, ephemeral host ports, conflict detection.
- **Network**: supervisor-answered DNS, domain/subdomain/CIDR/destination-port
  allowlists, private/link-local/metadata rejection, loopback-only default, generated
  `/etc/hosts` and `/etc/resolv.conf`, bridge/host/none/container modes.
- **Volumes and mounts**: bind, read-only bind, named/anonymous/tmpfs volumes,
  propagation flags, device allowlist, synthetic `/proc`, `/sys`, `/dev`, cgroup tree,
  `O_PATH` and magic-link resolution.
- **Labels**: image and container labels, validation, list filters, inspect metadata.
- **Compose**: JSON and YAML, services, networks, volumes, `depends_on` with health
  conditions, healthchecks, restart, deploy, capabilities, security options, secrets,
  configs, env/labels in list or map form, dependency-ordered instance plan.
- **Docker Engine API**: 48 routes across containers, images, networks, volumes,
  exec, events, build, auth, system and a `/laboon/v1` control surface. Real JSON,
  real counts, zero-trust authz, stored cancellation so forced delete tears down.
- **Security**: W^X, memory caps, vfs Namespace chokepoint, egress confinement,
  per-thread guest seccomp filter (Linux), Seatbelt (macOS), LPAC (Windows), the
  host-op thread, and a kill/trap backstop for a missed syscall.

The full construct-by-construct comparison with the original Go fork is in
[docs/OCI_MATRIX.md](docs/OCI_MATRIX.md).

## Security model

These rules are inherited from laboon and are not weakened by the Rust port:

1. **Only syscall instructions are rewritten.** Guest code runs at full native speed;
   a missed site reaches the host backstop. On Linux the backstop is a
   `RET_TRAP` that routes the syscall to the supervisor (`stub/zig/seccomp.zig`);
   macOS Seatbelt and the Windows instrumentation callback kill. Earlier docs claimed
   kill-on-miss on all three — that was drift, corrected here.
2. **The vfs Namespace is the only filesystem.** Every path resolves through it;
   no syscall handler touches a host path directly. Synthetic `/proc`, `/sys`, `/dev`
   and the cgroup tree are vfs objects.
3. **The supervisor owns the data plane.** The Zig stub handles only what must run
   in the guest address space: thread control, signal trampolines and the stdio fast
   path. File and network I/O are channel round-trips.
4. **Native code is Zig, supervisor code is Rust.** The freestanding stub links no
   libc; the Rust supervisor talks to it over the 128-byte shared channel ABI.
5. **Libraries take options, not environment.** Behavior is set through `Options`
   or `Config`; only binaries read the environment.

## Single address space

The request asked for single-addressed-spaced laboon instances "if possible without
regressions". The evidence says it is **not** possible on this architecture: the
seccomp/Landlock/Seatbelt/LPAC backstops, the `PROT_R` hop-request alias, fork
copy-on-write and the process-global stub window all depend on separate page tables
for supervisor and guest. One address space per instance is preserved; the supervisor
control plane, OCI/compose state, image cache, rewrite cache and daemon are shared.
Full analysis: [docs/SINGLE_ADDRESS_SPACE.md](docs/SINGLE_ADDRESS_SPACE.md).

## Port status and known gaps

See [docs/PORT_STATUS.md](docs/PORT_STATUS.md). Summary: the Rust control plane,
OCI/Compose/daemon surface, Zig ABI and security invariants are implemented and
witnessed; the three native guest-execution backends are not yet ported, and the Go
sources remain as the working runtime. `laboon run` reports a clear
`UnsupportedPlatform` diagnostic after preparing the run rather than pretending to
execute a guest.

The Go baseline itself had two red test packages at HEAD (`internal/rewrite`
x86 decoder and `stub` seccomp allowlist), a stale `NoRewrite` contract on macOS, a
leaked sandbox on `DELETE --force`, and a 2-second timed attach gate. The Rust port
does not reproduce any of those; the runtime-spec parser fails closed where the Go
shim silently dropped fields.

## License

Apache-2.0. See [LICENSE](LICENSE) and [COPYRIGHT](COPYRIGHT).
