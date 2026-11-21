<p align="center">
  <img src="docs/assets/logo.svg" alt="qqbox" width="300">
</p>

<p align="center">
  <a href="https://github.com/dywongcloud/qqbox/actions/workflows/ci.yml"><img src="https://github.com/dywongcloud/qqbox/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/dywongcloud/qqbox/releases/latest"><img src="https://img.shields.io/github/v/release/dywongcloud/qqbox" alt="Latest release"></a>
  <a href="https://docs.rs/qqbox"><img src="https://img.shields.io/badge/docs.rs-qqbox-blue" alt="Rust reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
  <img src="https://img.shields.io/badge/platforms-Linux%20macOS%20Windows-informational" alt="Platforms">
  <img src="https://img.shields.io/badge/arch-amd64%20arm64-informational" alt="Architectures">
</p>

**Tencent qqbox** runs untrusted Linux programs and containers safely, with no
VM, on Linux, macOS, and Windows. This is the Rust port of the upstream laboon
sandbox; the user experience below is the upstream one, with every command and
library path in Rust.

qqbox runs unmodified Linux binaries and OCI containers with the guest's machine
code executing natively on the host CPU. It intercepts only syscall
instructions: a supervisor emulates the Linux ABI, with no second kernel or
instruction emulator. This is the syscall-interception class of design, like
gVisor. Native execution means no VM on any supported host, but requires a
matching CPU architecture: amd64 or arm64, with no translation between them.

Isolation has **two independent layers**: supervisor mediation through a
confined filesystem with no direct host paths, a fixed device allowlist, and
networking off by default with configurable egress allowlists; plus the host-OS
sandbox (seccomp / Seatbelt / AppContainer) as a kill-on-escape backstop. Give
each untrusted program or agent-generated command a disposable sandbox, exposing
only the files and network access it needs.

Use it as a standalone binary, behind a container client, through MCP, or
embedded in your own program. The same Linux guest behavior is provided across
Linux, macOS, and Windows, subject to the documented
[platform limits](docs/permissions-and-limits.md). qqbox is newer, with a small
host surface by design; its daemon and OCI runtime should not be read as claims
of production maturity or complete engine compatibility.

[Install](#install) · [Get started](#get-started) · [Ways to run it](#ways-to-run-it) ·
[How it works](#how-it-works) · [Performance](#performance) ·
[AI agents and desktop sandboxes](#ai-agents-and-desktop-sandboxes)

Traditional Chinese: [zh-hant/README.zh-Hant.md](zh-hant/README.zh-Hant.md).

> **Port boundary (read this first).** All Go source was removed at the user's
> request; this tree is Rust + Zig only. The Rust control plane, OCI/Compose
> typing, 48-route daemon, runtime-spec validation, MCP server and library are
> implemented and witnessed. The three **native guest-execution backends**
> (Linux memfd/seccomp/hop, macOS Mach/Seatbelt/HVF, Windows LPAC) are **not**
> ported to Rust, so `qqbox run` and the MCP `run` tool still return a clear
> `UnsupportedPlatform` diagnostic after preparing the run. A from-source build
> cannot yet boot a guest. The live Xubuntu 26 desktop in
> [examples/qqbox/](examples/qqbox/README.md) is booted with the pre-removal Go
> runtime binary preserved off-repo. Full accounting:
> [docs/PORT_STATUS.md](docs/PORT_STATUS.md).

## Install

**From source** (Linux, macOS, Windows; amd64 or arm64):

```sh
cargo install --path crates/qqbox-cli
cargo install --path crates/qqboxd
cargo install --path crates/qqbox-mcp
cargo install --path crates/qqbox-runtime
```

Prebuilt binaries for every platform are on the
[latest release](https://github.com/dywongcloud/qqbox/releases/latest). To embed
qqbox as a Rust library, add it to your `Cargo.toml`:

```toml
[dependencies]
qqbox = { path = "crates/qqbox" }   # or from crates.io / git when published
```

To build from source you need Rust 1.98 or newer. The per-platform Zig stubs are
committed (`stub/*.bin`, `stub/macos/*.bin`, `stub/windows/*.bin`) and embedded
with `include_bytes!`, so importing or building qqbox needs neither Zig nor a C
toolchain. The first image run also needs host access to its OCI registry; later
runs use the cached image unless `Options::pull` requests a refresh. Image
fetching is separate from the guest's network permissions.

## Ways to run it

| Interface | Use it when… | Entry point |
|---|---|---|
| **Standalone CLI / binary** | You want to run a program or image directly, with no daemon. | [`crates/qqbox-cli`](crates/qqbox-cli/src/main.rs): `qqbox run-image alpine:3.20 cat /etc/os-release` |
| **Docker-API-compatible daemon** | You want a Docker or Moby client to drive sandboxes. It serves the Docker Engine API; each container runs as a qqbox sandbox, without a second kernel. | [`crates/qqboxd`](crates/qqboxd/src/main.rs): `qqboxd --listen 127.0.0.1:2375 --token <secret>` |
| **OCI runtime** | You want a runc/crun drop-in for Docker, containerd, Podman, or CRI-O. Register it with your engine; Docker selects it with `docker run --runtime=qqbox …`. | [`crates/qqbox-runtime`](crates/qqbox-runtime/src/main.rs): `qqbox-runtime create <id> <bundle>` |
| **MCP server** | You want an AI agent to call a `run` tool and receive the exit status, stdout, and stderr. Supports stdio and streamable HTTP server mode. | [`crates/qqbox-mcp`](crates/qqbox-mcp/src/main.rs): `qqbox-mcp` / `qqbox-mcp --http 127.0.0.1:8080` |
| **Rust library** | You want to embed the sandbox in your own program, with no daemon. | `qqbox` crate: `qqbox::run_image(&ctx, "alpine:3.20", &opts)` |
| **Live migration** | You want to checkpoint, pre-copy and restore supervisor-owned control-plane state between two hosts. | `qqbox migrate checkpoint\|verify\|restore\|serve\|send`, `/qqbox/v1/migrate/*` daemon routes, [`qqbox::migrate`](crates/qqbox/src/migrate/mod.rs) |

The daemon and OCI runtime are separate integrations: one serves clients, the
other runs bundles supplied by an existing engine. Selecting qqbox as an OCI
runtime does not remove an engine's own host-platform requirements.

**Migration boundary.** `qqbox migrate` transfers the workload spec, the vfs
Namespace, the chanPage/fd table and the network policy as a versioned,
SHA-256-checked ADT with affine typestate and capability-gated commit. It does
**not** transfer guest memory or vCPU registers: qqbox has no VMM and the three
native guest backends are not ported to Rust. See
[docs/LIVE_MIGRATION.md](docs/LIVE_MIGRATION.md) (Traditional Chinese:
[zh-hant/docs/LIVE_MIGRATION.zh-Hant.md](zh-hant/docs/LIVE_MIGRATION.zh-Hant.md)).

**Native guest backend:** the Linux trap-to-supervisor path is now real Rust —
a memfd chanPage, a seccomp-BPF `RET_TRAP` filter, a `SIGSYS` register
save/restore and a Rust host-op dispatcher, live-witnessed on a real Linux
aarch64 kernel. ELF mapping, trampolines, the ~205-syscall table and the
macOS/Windows launch contracts are not ported, so `qqbox run` still returns
`Error::UnsupportedPlatform` for the general case. `qqbox backend exec-elf` now
executes a real unmodified static aarch64 ELF under the Rust supervisor,
live-witnessed on Linux. See
[docs/NATIVE_BACKEND.md](docs/NATIVE_BACKEND.md) (Traditional Chinese:
[zh-hant/docs/NATIVE_BACKEND.zh-Hant.md](zh-hant/docs/NATIVE_BACKEND.zh-Hant.md)).

## Get started

### Run an image with the CLI

Download and extract the
[repository source](https://github.com/dywongcloud/qqbox/archive/refs/heads/feat/rust-port.zip),
then open a terminal in the extracted repository directory:

```sh
cargo build --workspace
./target/debug/qqbox version
./target/debug/qqbox run-image alpine:3.20 cat /etc/os-release
```

On Windows, invoke `qqbox.exe` instead. The command prints Alpine's release
information, including `ID=alpine`, once a native backend is available. Today
the pull fails first: `status: registry request failed: … status code 401`
(rc=1) against an unauthenticated registry config; on a host with a configured
registry token it reaches the `UnsupportedPlatform` diagnostic described above.
The guest has the image's filesystem and no external network access. Once
installed on your PATH, the command is
`qqbox run-image alpine:3.20 cat /etc/os-release`.

The CLI also exposes the typed control plane, all witnessed with
`cargo run -p qqbox-cli -- <command>`:

```
qqbox invariants                    the enforced security invariants
qqbox abi                           validate the 128-byte Zig channel ABI
qqbox address-space                 the single-address-space decision
qqbox run <program> [args...]       parse + rewrite + plan a guest ELF
qqbox config-validate <file>        print every OCI image-config construct
qqbox oci-inspect <file>            full image config + construct count
qqbox ports-check <spec>...         parse publishes and print the forward plan
qqbox compose-plan <compose.yaml>   lower a Compose file to an instance plan
qqbox compose-validate <file>       validate services, ports, volumes, depends_on
qqbox runtime-spec features         advertised OCI runtime-spec features
qqbox runtime-spec validate <file>  apply config.json, fail closed on unknowns
qqbox image-plan <ref>              registry/manifest/blob URLs and platform
qqbox network-plan <compose.yaml>   networks, aliases, forwarding, /etc files
qqbox daemon-probe <path>           exercise one Docker-API route in-process
qqbox daemon --listen <addr>        start the Docker-compatible daemon
```

### Drive sandboxes with a Moby/Docker client

`qqboxd` speaks the Docker Engine API (v1.43) with zero-trust bearer auth on
every route. Start it, then point any Docker or Moby client at it:

```sh
cargo run -p qqboxd -- --listen 127.0.0.1:2375 --token my-secret
export DOCKER_HOST=tcp://127.0.0.1:2375
```

For a client that can send an arbitrary `Authorization` header (the Moby Go
client via a custom `http.Client`, or plain `curl`), every request carries
`Authorization: Bearer my-secret`:

```sh
curl -H 'Authorization: Bearer my-secret' http://127.0.0.1:2375/_ping
# OK
curl -H 'Authorization: Bearer my-secret' http://127.0.0.1:2375/version
# {"ApiVersion":"1.43","Arch":"arm64","KernelVersion":"qqbox","MinAPIVersion":"1.24","Os":"linux","Version":"0.1.0"}
curl -X POST -H 'Authorization: Bearer my-secret' -H 'Content-Type: application/json' \
  -d '{"Image":"alpine:3.20","Cmd":["echo","hello from qqbox"],"Labels":{"qqbox.test":"1"}}' \
  http://127.0.0.1:2375/containers/create
# {"Id":"39aed3f4…","Warnings":[]}
```

Unauthenticated requests return HTTP 401. There is no unauthenticated default.
The token is generated and printed at startup when `--token` is omitted. The 48
routes are listed by `qqboxd --routes` and
`GET /qqbox/v1/routes`; the enforced invariants are at
`GET /qqbox/v1/invariants`.

### Register as an OCI runtime

`qqbox-runtime` implements the runc/crun command surface against a bundle's
`config.json`. Register it with your engine, for example in
`/etc/docker/daemon.json`:

```json
{
  "runtimes": {
    "qqbox": { "path": "/usr/local/bin/qqbox-runtime" }
  }
}
```

then `docker run --runtime=qqbox …`. Or drive it directly:

```sh
qqbox-runtime --root /run/qqbox-runtime features
qqbox-runtime --root /run/qqbox-runtime create demo-1 /path/to/bundle
qqbox-runtime --root /run/qqbox-runtime state demo-1
qqbox-runtime --root /run/qqbox-runtime start demo-1
qqbox-runtime --root /run/qqbox-runtime kill demo-1 SIGTERM
qqbox-runtime --root /run/qqbox-runtime delete demo-1
```

`create` parses and validates `config.json` with `qqbox::oci::runtime::parse`,
`validate` and `apply`; unsupported security constructs are **rejected**, never
silently dropped. State is real JSON under `--root`. `start` currently returns
the native-backend diagnostic described above.

### Embed a sandbox in Rust

In a new directory outside the source checkout:

```sh
cargo new qqbox-hello
cd qqbox-hello
```

`Cargo.toml`:

```toml
[package]
name = "qqbox-hello"
version = "0.1.0"
edition = "2021"

[dependencies]
qqbox = { path = "../literbox/crates/qqbox" }
```

`src/main.rs`:

```rust
use qqbox::{run_image, Context, Options, Stdio};

fn main() {
    let ctx = Context::default();
    let mut opts = Options::default();
    opts.args = Some(vec!["echo".into(), "hello from qqbox".into()]);
    opts.stdout = Stdio::Inherit;
    opts.stderr = Stdio::Inherit;
    match run_image(&ctx, "alpine:3.20", &opts) {
        Ok(code) => std::process::exit(code),
        Err(err) => {
            eprintln!("qqbox: {err}");
            std::process::exit(1);
        }
    }
}
```

```sh
cargo run
```

The guest prints `hello from qqbox` and exits successfully once a native backend
is available; today the error arm prints the `UnsupportedPlatform` diagnostic.
It has the image's filesystem and no external network access. Output is
connected explicitly because the library discards stdout and stderr by default
(`Stdio::Null`).

Use `qqbox::run` for a Linux ELF binary instead of an image. Without
`Options::root`, the binary is loaded from a host path and the guest has no
filesystem; `root` supplies a root directory. See
[crates/qqbox/src/options.rs](crates/qqbox/src/options.rs) for argument, mount,
environment, image-cache, and I/O options, and `cargo doc -p qqbox --open` for
the API.

## What runs under it

The upstream workload set is recorded in the upstream README; those are
exercised workloads of the Go runtime, not a promise that every Linux program or
syscall is supported, and **not** a claim about this Rust-only tree. The Rust
port's witnessed coverage is the typed control plane: OCI image config, runtime
spec, Compose, ports, network, volumes, labels, the vfs Namespace, the 48 Docker
API routes and the security invariants — see
[docs/OCI_MATRIX.md](docs/OCI_MATRIX.md). Guest-execution workloads are gated on
the native backend port; see [docs/PORT_STATUS.md](docs/PORT_STATUS.md).

| Workload | Upstream Go runtime | Rust port |
|---|---|---|
| Languages and runtimes | Go, C/C++, Rust, Java, Python, PHP, Ruby, Perl, Node.js (V8 JIT), .NET | Backend pending |
| Databases and servers | PostgreSQL, MariaDB, Redis | Backend pending |
| Browser | Headless Chromium: DOM and screenshot | Backend pending; live desktop proven with the off-repo runtime |
| Nested containers | podman and Docker, each running its own containers | Backend pending |
| Tools and nesting | `ptrace` tools such as `strace`; laboon inside laboon | Backend pending |
| Desktop | Full XFCE desktop | Witnessed live: [examples/qqbox/](examples/qqbox/README.md) |

## How it works

A loaded ELF image runs inside a host process with a small Zig stub. The
rewriter locates syscall sites with per-architecture classifiers, without a
disassembler, and patches those instructions to trap into the supervisor. Every
other instruction executes untouched on the CPU.

The stub forwards requests over a shared-memory channel to the Rust supervisor,
which emulates the Linux ABI and owns filesystem and network I/O. The stub
handles work that must stay in the guest address space: thread control, signal
trampolines, and the hot stdio fast path on a held descriptor. The 128-byte
`chanPage` layout is frozen in [crates/qqbox/src/zig/abi.rs](crates/qqbox/src/zig/abi.rs).

### Two independent isolation layers

**Supervisor mediation.** Every guest path resolves through one confined `vfs`
Namespace. Image rootfs, bind mounts, overlays, and synthetic `/proc`, `/sys`,
and `/dev` all use that same filesystem boundary; device nodes come from a fixed
allowlist. Guests cannot resolve host paths directly; host files are available
only through explicitly configured roots or mounts. This lets a run use an
image's files while sharing only the host directories its caller chooses.

**Host-OS backstop.** If a syscall escapes rewriting, the host sandbox kills the
guest (`Error::Killed`): seccomp on Linux, Seatbelt on macOS, AppContainer on
Windows. On Linux, mapping memory, holding descriptors, and forking belong to a
separate host-op thread that never executes guest code. The guest thread's
seccomp filter does not grant those operations merely because the stub needs
them. [AGENTS.md](AGENTS.md) defines these boundaries.

### Security defaults

qqbox is confined by default: the guest gets loopback-only networking, no view
of the host filesystem, and no shared memory — every grant (`Options::net`,
`Options::mounts`, `Options::allow_domains`, …) is explicit. The full set of
controls and their current limits is in
[docs/permissions-and-limits.md](docs/permissions-and-limits.md). Libraries take
options, not environment variables; only binaries read the environment.

### Where it fits

| Runtime | Guest execution | VM for Linux guests | Integration |
|---|---|---|---|
| qqbox | Native code; syscalls emulated | No | CLI, Docker API, OCI runtime, MCP, Rust library |
| Docker / Podman | Linux namespaces and cgroups | On macOS/Windows | Container runtime |
| gVisor | Syscall interception; user-space kernel | No | Sandbox runtime |
| Firecracker | Guest kernel in a microVM | Yes | VMM |
| Kata Containers | Guest kernel in a VM per container | Yes | Container runtime |
| Windows Sandbox | Windows guest, not Linux | Yes (Hyper-V) | OS feature |
| Microsoft LiteBox | Library OS; TEE / confidential-computing backends | Usually (TEE/VM) | Library |

The distinction is the execution boundary, not a security ranking. qqbox
combines supervisor mediation with a host-OS backstop. Docker on Linux shares
the host kernel; on macOS and Windows its Linux VM supplies another boundary.
Choose according to the workload and the boundary you need.

## Performance

The upstream README records measurements of the **Go runtime** (Apple M4
macOS 26.5: Alpine `echo` 12 ms vs Docker Desktop 187 ms, Node start 129 ms vs
200 ms, CPU loop ~tie; Linux CPU-bound slowdown ~1.01× native, ~224 ns per
trivial intercepted syscall). Those are **recorded measurements, not fresh
validation of this Rust tree**, and they do not carry over: no native Rust guest
backend exists yet, so no end-to-end measurement can be produced here. See
[docs/PERF.md](docs/PERF.md) for the method and the honest status. Native CPU
execution does not make every workload native-speed: syscall handling, data
copies, and process startup still cost time.

## AI agents and desktop sandboxes

For an agent that emits code, the MCP server provides a direct execution
boundary: every `run` call starts a disposable sandbox from an OCI image and
returns its exit code and captured output. Build and launch it from the
repository root:

```sh
cargo build --workspace
./target/debug/qqbox-mcp                        # stdio transport
./target/debug/qqbox-mcp --http 127.0.0.1:8080 # streamable HTTP
```

Configure your MCP client to launch the resulting `qqbox-mcp` binary by its
absolute path for stdio transport, or run `qqbox-mcp --http 127.0.0.1:8080`
and connect to `http://127.0.0.1:8080/` for HTTP server mode. On Windows, use
`qqbox-mcp.exe`. For example, call the `run` tool with:

```json
{"image":"python:3.12-alpine","command":["python3","-c","print(2+2)"]}
```

The upstream result contains exit code `0` and stdout `4\n`. In this Rust tree
the call returns `isError: true` with the native-backend diagnostic in
`stderr`, because no guest backend is ported yet; it does not fabricate a
successful run. External networking is available only when the server starts
with `--allow-net` and the call requests `"network": true`. By default, runs
have a 120-second timeout and each output stream is capped at 1 MiB; the server
exposes `--timeout` and `--max-output` to change those defaults.

The server also exposes real, fully witnessed tools that need no guest backend:
`qqbox_invariants`, `qqbox_routes`, `qqbox_runtime_spec_validate`,
`qqbox_config_validate`, `qqbox_ports_check`, `qqbox_compose_plan` and
`qqbox_version`.

The [Xubuntu 26 desktop sandbox](examples/qqbox/README.md) boots XFCE on Xvfb
with `x11vnc`, a bottom dock, working wallpapers, PulseAudio, real Chromium,
`root@qbox`, and a full GNU toolchain. It is the witnessed live desktop from
this fork, booted with the pre-removal Go runtime binary preserved off-repo;
`examples/qqbox/` carries the reproducible Dockerfile and startup script.

```sh
sh examples/qqbox/fetch-chromium.sh
sh examples/qqbox/fetch-extras.sh
QQBOX_RUNTIME=/path/to/pre-removal-laboon-runtime
"$QQBOX_RUNTIME" build -net -t qqbox-xubuntu:26 \
  -f examples/qqbox/Dockerfile -o qqbox-xubuntu26.tar examples/qqbox
"$QQBOX_RUNTIME" -image qqbox-xubuntu:26 -net -e VNC_PORT=5905 \
  -mount /tmp/qqbox-out:/out
vncviewer 127.0.0.1:5905
```

`build` and `-image` are commands of the **pre-removal Go runtime binary**,
preserved off-repo to boot this desktop. The Rust `qqbox` CLI in this tree has
no `build` subcommand and no `-image` flag (`qqbox build --help` returns rc=2);
its equivalent surface is `qqbox run-image`, `qqbox runtime-spec` and the
`qqboxd` Docker API daemon. Do not substitute the Rust binary into these two
lines. See [docs/PORT_STATUS.md](docs/PORT_STATUS.md).

## License

Apache-2.0. See [LICENSE](LICENSE), [COPYRIGHT](COPYRIGHT) and
[NOTICE.md](NOTICE.md) for the upstream attribution and branding notes.
