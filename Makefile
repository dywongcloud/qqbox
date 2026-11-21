# qqbox build orchestration.
#
# Rust workspace: Cargo.toml is the build entry point. The committed freestanding
# Zig stubs are embedded with include_bytes!, so a build needs no Zig toolchain.
# Go source and Go build tooling were removed; Rust is the only implementation.

SHELL := /bin/sh
CARGO ?= cargo
# The committed freestanding Zig stub sources target Zig 0.13.0. Installed
# toolchains may differ; stub/build.sh fails closed with a diagnostic rather
# than silently compiling against an incompatible major version. Set ZIG
# explicitly to a 0.13.0 binary when rebuilding the .bin payloads.
ZIG ?= $(shell command -v zig 2>/dev/null)
export ZIG

.DEFAULT_GOAL := build

.PHONY: build build-release check fmt clippy test stubs stubs-linux \
	stubs-windows stubs-macos cli daemon mcp runtime run invariants version routes \
	witness backend-witness backend-exec-elf zh-hant-verify clean

# ------------------------------------------------------------- rust build -----
build:
	$(CARGO) build --workspace

build-release:
	$(CARGO) build --workspace --release

check:
	$(CARGO) check --workspace

fmt:
	$(CARGO) fmt --all

clippy:
	$(CARGO) clippy --workspace -- -D warnings

test:
	$(CARGO) test --workspace

cli:
	$(CARGO) run -q -p qqbox-cli -- version

daemon:
	$(CARGO) run -q -p qqboxd -- --routes

mcp:
	$(CARGO) run -q -p qqbox-mcp -- --help

runtime:
	$(CARGO) run -q -p qqbox-runtime -- features

# --------------------------------------------------------------- stubs -------
stubs-linux:
	sh stub/build.sh

stubs-windows:
	sh stub/windows/build.sh

stubs-macos:
	sh stub/macos/build.sh
	sh stub/macos/build-hvf.sh

stubs: stubs-linux stubs-windows stubs-macos

# ------------------------------------------------------------ witnesses ------
invariants:
	$(CARGO) run -q -p qqbox-cli -- invariants

version:
	$(CARGO) run -q -p qqbox-cli -- version

routes:
	$(CARGO) run -q -p qqboxd -- --routes

run:
	$(CARGO) run -q -p qqbox-cli -- run $(PROGRAM)

witness: build
	$(CARGO) run -q -p qqbox-cli -- version
	$(CARGO) run -q -p qqbox-cli -- invariants
	$(CARGO) run -q -p qqbox-cli -- abi
	$(CARGO) run -q -p qqbox-cli -- backend capabilities
	$(CARGO) run -q -p qqbox-cli -- ports-check 127.0.0.1:8080:80/tcp 3000-3002:3000-3002/udp 53
	$(CARGO) run -q -p qqbox-cli -- config-validate examples/oci-config.json
	$(CARGO) run -q -p qqbox-cli -- oci-inspect examples/oci-config.json
	$(CARGO) run -q -p qqbox-cli -- compose-plan examples/compose.yaml
	$(CARGO) run -q -p qqbox-cli -- compose-validate examples/compose.yaml
	$(CARGO) run -q -p qqbox-cli -- runtime-spec validate --advisory examples/runtime-spec.json
	$(CARGO) run -q -p qqbox-cli -- runtime-spec validate examples/runtime-spec-minimal.json
	$(CARGO) run -q -p qqbox-cli -- runtime-spec features
	$(CARGO) run -q -p qqbox-cli -- image-plan alpine:3.20
	$(CARGO) run -q -p qqbox-cli -- network-plan examples/compose.yaml
	$(CARGO) run -q -p qqbox-cli -- daemon-probe /containers/create --method POST --body examples/container-create.json
	$(CARGO) run -q -p qqboxd -- --routes
	$(CARGO) run -q -p qqbox-runtime -- features
	$(CARGO) run -q -p qqbox-mcp -- --help >/dev/null

# Live Linux backend witness. Requires a real Linux kernel reachable via
# `podman machine ssh` with the repository visible through virtiofs. This is a
# real seccomp trap, not a mock. Cross-compiled with rust-lld for static musl.
backend-witness: build
	RUSTFLAGS='-C linker=rust-lld' $(CARGO) build --offline -p qqbox-cli --target aarch64-unknown-linux-musl
	podman machine ssh -- $(CURDIR)/target/aarch64-unknown-linux-musl/debug/qqbox backend capabilities
	podman machine ssh -- $(CURDIR)/target/aarch64-unknown-linux-musl/debug/qqbox backend witness
	podman machine ssh -- $(CURDIR)/target/aarch64-unknown-linux-musl/debug/qqbox backend memory

# Real unmodified ELF guest under the Rust supervisor. The guest ELF is built
# off-repo with aarch64-linux-musl-ld -Ttext=0x6f0000000000; the kernel-only
# baseline exits 1, the supervised run exits 42. Requires GUEST_ELF=<path>.
backend-exec-elf: build
	RUSTFLAGS='-C linker=rust-lld' $(CARGO) build --offline -p qqbox-cli --target aarch64-unknown-linux-musl
	podman machine ssh -- $(GUEST_ELF); echo kernel_baseline_rc=$$?
	podman machine ssh -- $(CURDIR)/target/aarch64-unknown-linux-musl/debug/qqbox backend exec-elf $(GUEST_ELF)

# Verify the SHIPPED Traditional Chinese deliverables, not stale off-repo
# copies. The off-repo harness holds the blocklist; ROOT and EXPECTED are
# overridden here so every .md/.txt under zh-hant/ is scanned.
zh-hant-verify:
	@python3 -c '\
import importlib.util, pathlib, sys; \
spec = importlib.util.spec_from_file_location("zhv", "$(HOME)/.gm-work/qqbox/zh-hant/verify_zh_hant.py"); \
v = importlib.util.module_from_spec(spec); spec.loader.exec_module(v); \
root = pathlib.Path("$(CURDIR)/zh-hant"); v.ROOT = root; \
v.EXPECTED = {str(p.relative_to(root)): (400 if p.suffix == ".txt" else 600) \
              for p in root.rglob("*") if p.is_file() and p.suffix in {".md", ".txt"}}; \
sys.exit(v.main())'

clean:
	$(CARGO) clean
