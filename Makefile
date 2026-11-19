# qqbox build orchestration.
#
# Rust workspace: Cargo.toml is the build entry point. The committed freestanding
# Zig stubs are embedded with include_bytes!, so a build needs no Zig toolchain.
# The Go tree remains as the working native guest backend (docs/PORT_STATUS.md).

SHELL := /bin/sh
CARGO ?= cargo
ZIG ?= $(shell command -v zig 2>/dev/null || printf %s /tmp/zig-linux-x86_64-0.13.0/zig)
export ZIG

.DEFAULT_GOAL := build

.PHONY: build build-release check fmt clippy test stubs stubs-linux \
	stubs-windows stubs-macos cli daemon run invariants version routes \
	witness clean

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

clean:
	$(CARGO) clean
