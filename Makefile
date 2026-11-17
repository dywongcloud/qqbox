# laboon build orchestration.
#
# Every laboon binary embeds its platform's freestanding stub(s) with //go:embed.
# This Makefile makes those a hard dependency of the binary so a stub is never
# missed or stale -- in particular the macOS HVF stub, which is a SEPARATE binary
# from the native stub and which the darwin backend embeds alongside it.
#
# Stub build steps (all easy to forget by hand; enforced here as dependencies):
#   linux:   stub/build.sh           -> stub/stub-{amd64,arm64}{,-pie}.bin
#   windows: stub/windows/build.sh   -> stub/windows/stub-{amd64,arm64}.bin
#   macOS:   stub/macos/build.sh     -> stub/macos/stub.bin       (native)
#            stub/macos/build-hvf.sh -> stub/macos/hvf_stub.bin    (HVF / VM)
#
# The stub .bin files are committed, so `go build`/`go get` work without Zig.
# `make` (and CI) rebuild them from the Zig sources with the pinned Zig ($ZIG);
# run `make stubs` after editing a stub so the committed binary stays current.

ZIG ?= /tmp/zig-linux-x86_64-0.13.0/zig
export ZIG
GO  ?= go

.DEFAULT_GOAL := laboon

# ------------------------------------------------------------------ stubs -----
.PHONY: stubs stubs-linux stubs-windows stubs-macos

stubs-linux:
	sh stub/build.sh

stubs-windows:
	sh stub/windows/build.sh

# BOTH macOS stubs: the native map-and-rewrite stub AND the HVF (VM) stub. The
# darwin backend embeds both, so building only one ships a stale/absent other --
# the mistake this target exists to prevent.
stubs-macos:
	sh stub/macos/build.sh
	sh stub/macos/build-hvf.sh

stubs: stubs-linux stubs-windows stubs-macos

# -------------------------------------------------------- laboon binaries -----
# Each binary rebuilds its platform's stubs first, so the embedded stub is never
# stale. Names match what the test/sweep runners and docs expect.
.PHONY: laboon build \
	laboon-linux-amd64 laboon-linux-arm64 \
	laboon-windows-amd64 laboon-windows-arm64 \
	laboon-darwin-arm64

laboon-linux-amd64: stubs-linux
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o laboon ./cmd/laboon

laboon-linux-arm64: stubs-linux
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o laboon-linux-arm64 ./cmd/laboon

laboon-windows-amd64: stubs-windows
	GOOS=windows GOARCH=amd64 $(GO) build -o laboon.exe ./cmd/laboon

laboon-windows-arm64: stubs-windows
	GOOS=windows GOARCH=arm64 $(GO) build -o laboon-arm64.exe ./cmd/laboon

laboon-darwin-arm64: stubs-macos
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o laboon-darwin ./cmd/laboon

# Host binary (default goal).
laboon: laboon-linux-amd64

# Every target binary in one shot.
build: laboon-linux-amd64 laboon-linux-arm64 laboon-windows-amd64 laboon-windows-arm64 laboon-darwin-arm64

# ------------------------------------------------------------------ checks ----
# Cross-compile vet for every target surface -- catches a per-OS build break
# (e.g. a Windows-only file) that a host-only build would miss.
.PHONY: vet test
vet:
	$(GO) vet ./...
	GOOS=windows GOARCH=amd64 $(GO) vet ./...
	GOOS=windows GOARCH=arm64 $(GO) vet ./...
	GOOS=darwin  GOARCH=arm64 $(GO) vet ./...

test:
	$(GO) test ./...


# ------------------------------------------------------- container sweep ------
# Run the language/database image corpus under laboon. Local linux runs in place;
# other targets ship a cross-built binary to their VM (see docs/SWEEP or the run
# scripts). SWEEP_NET=1 adds -net; PLATFORM overrides the image arch.
.PHONY: sweep
sweep: laboon-linux-amd64
	PLATFORM=$(if $(PLATFORM),$(PLATFORM),linux/amd64) sh scripts/container-sweep.sh ./laboon

# ------------------------------------------------------------------ clean -----
.PHONY: clean
clean:
	rm -f laboon laboon-linux-arm64 laboon.exe laboon-arm64.exe laboon-darwin
	rm -f stub/*.bin stub/windows/*.bin stub/macos/*.bin
