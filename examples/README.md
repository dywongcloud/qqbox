# Examples

This directory holds reproducible inputs and the two desktop images. The
Go-era example programs were removed together with the Go source tree; the
Rust workspace (`cargo build --workspace`) is the only build entry point.

## OCI / Compose / Docker-API inputs

| file | use |
| --- | --- |
| `oci-config.json` | image config parsed by `qqbox oci-inspect` |
| `runtime-spec.json` | full OCI runtime-spec, advisory validation (`runtime-spec validate --advisory`) |
| `runtime-spec-minimal.json` | minimal spec, strict validation |
| `container-create.json` | `qqbox daemon-probe /containers/create --method POST --body ...` |
| `compose.yaml` | `qqbox compose-plan` / `qqbox compose-validate` / `qqbox network-plan` |

Run them all with `make witness`.

## `xfce/` — Alpine XFCE desktop over VNC

`Dockerfile`, `start-xfce.sh`, the RFB capture/typing clients and the live
evidence from the first desktop build. The native `xfce4-terminal` works because
`TIOCPKT` is forwarded to the host pty master with a pointer-form ioctl; the
browser-based pipe console is retained only as an opt-in fallback.

## `xubuntu/` — stock linuxserver/webtop ubuntu-xfce desktop

Pinned to the linux/arm64 manifest
`sha256:5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55`.
`Dockerfile` + `start-xubuntu.sh` boot Xvfb → x11vnc (TCP 5905) → the stock
XFCE session → a native terminal. See `README.md` there for the exact build and
run commands, and `FIXES.json` for claim-by-claim evidence.

## `qqbox/` — the fully provisioned desktop

The reproducible successor image: real Debian Chromium 150 (not NetSurf),
working `ping`, apt/apk registry installs, external DNS, a bottom dock,
audio, `root@qbox`, and the toolchain (python3/pip, node/npm, git, gh, Claude
Code). Build with `qqbox build -net -t qqbox-xubuntu:final -f Dockerfile .`
after running `fetch-chromium.sh` to assemble the Chromium payload.

## Traditional Chinese

繁體中文文件在 [`zh-hant/`](../zh-hant/README.zh-Hant.md). Go-era Go code
samples are gone; the Rust API is described in the main README.
