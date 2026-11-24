# Stock Xubuntu/XFCE desktop inside qqbox

> Flag provenance: the single-dash `-net`, `-allow-domains`, `-allow-cidrs`
> and `-allow-ports` on this page are flags of the off-repo pre-removal Go
> runtime used by these historical desktop examples. The Rust `qqbox` CLI in
> this repository uses `--net`, `--no-net` and `--allow-domain`; CIDR and port
> limits are `Options`/`Config` fields, not CLI flags.

A live, witnessed `linuxserver/webtop` **ubuntu-xfce** desktop running under the
native qqbox guest runtime on macOS/arm64, served over VNC on TCP 5905, with a
real `xfce4-terminal` (VTE), a real Chromium browser, working `ping`, apt/apk
registry installs, and external DNS/TCP/HTTPS egress.

The reproducible successor is [`examples/qqbox/`](../qqbox/README.md); this
directory documents the pinned base image and the fixes that got it running.

## What "Xubuntu" means here

The user pinned
[Docker Hub layers `linuxserver/webtop/ubuntu-xfce-824a4465-ls171/images/sha256-5f170506…c55`](https://hub.docker.com/layers/linuxserver/webtop/ubuntu-xfce-824a4465-ls171/images/sha256-5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55)
and [xubuntu.org](https://xubuntu.org/). Live Registry API probes establish:

| | |
| --- | --- |
| Tag | `docker.io/linuxserver/webtop:ubuntu-xfce-824a4465-ls171` |
| Multi-arch index | `sha256:43141025abe50bde82b1a1757a524425ce36476ad50c187656e921b62b906f12` |
| **linux/arm64 manifest** | **`sha256:5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55`** (the digest in the URL) |
| Single-arch alias | `arm64v8-ubuntu-xfce-824a4465-ls171` → the same digest |
| Mirrors | `ghcr.io/linuxserver/webtop`, `lscr.io/linuxserver/webtop` (byte-identical) |
| Config digest | `sha256:68f14933221441169b722095ea156416d0cfaee0a9b7ca68ba0869a83194fcfe` |
| Layers | 14, 869,197,466 compressed bytes, arm64, `variant` absent |

`TITLE=Ubuntu XFCE`, `Entrypoint=["/init"]` (s6-overlay), `HOME=/config`,
`DISPLAY=:1`, `ExposedPorts 3000/3001`. Image history installs
`xubuntu-default-settings xubuntu-icon-theme xfce4 xfce4-terminal firefox
mousepad`; `/usr/share/xubuntu` and `/etc/xdg/xdg-xubuntu` carry the Xubuntu
desktop defaults.

**Honest boundary:** this is Ubuntu 24.04 + XFCE + Xubuntu's settings/theme, not
the Xubuntu *distribution* (its `/etc/os-release` says `ID=ubuntu`). There is no
`xubuntu` tag in `linuxserver/webtop` and xubuntu.org publishes no OCI image.
This is the exact image the user linked.

## Build (historical 24.04 image)

```sh
QQBOX_RUNTIME=/path/to/pre-removal-laboon-runtime
"$QQBOX_RUNTIME" build -net -t qqbox-xubuntu:stock \
  -f examples/xubuntu/Dockerfile -o qqbox-xubuntu.tar examples/xubuntu
```

`build` and `-image` here are commands of the **pre-removal Go runtime binary**,
preserved off-repo. The Rust `qqbox` CLI in this tree has neither a `build`
subcommand nor an `-image` flag: `qqbox build --help` returns rc=2 with
`error: unrecognized subcommand 'build'` (live-witnessed on this build). The
Rust workspace is built with `cargo build --workspace`; it prepares and validates
the control plane but does not boot this image. See `examples/qqbox/README.md`
for the current 26.04 image and its build procedure.

`FROM` is the pinned arm64 digest. `-net` is required for the `RUN apt-get` step.
The Dockerfile neutralizes a crashing `ldconfig.real` dpkg trigger and repairs
`libc-bin` status so later `apt-get install` keeps working. It adds `x11vnc`,
`Xvfb`, `scrot`, `xdotool`, `iputils-ping` and Debian Chromium 150 (see
`examples/qqbox/` for the fully provisioned variant). The rootfs, XFCE session,
Xubuntu theme, Firefox, Thunar and `xfce4-terminal` remain the image's own.

## Run

```sh
mkdir -p /tmp/qqbox-xubuntu-out
"$QQBOX_RUNTIME" -image qqbox-xubuntu:stock -net \
  -e VNC_PORT=5905 -mount /tmp/qqbox-xubuntu-out:/out
/opt/TurboVNC/bin/vncviewer 127.0.0.1:5905
```

`-net` is **not optional**: it is the switch that enables the supervisor's DNS
interception and host-socket egress. Without it a browser gets no DNS and no
host reach and renders its generic offline page ("Reconnecting to Wi-Fi" is
Chromium boilerplate, not a wifi driver fault).

## The fixes

### 1. A real native terminal

`xfce4-terminal` spawns a real PTY child and paints text. Root cause was Linux
`TIOCPKT` (`0x5420`) unhandled in the supervisor's ioctl switch; VTE got
`ENOTTY` → "Failed to open PTY: Not a tty". Forwarding it to the host pty
master works only with a **pointer-form** ioctl: `TIOCPKT` takes a pointer to
an int (Darwin `0x80047470`, `_IOC_IN`), so passing the value gives `EFAULT`
→ "Failed to open PTY: Bad address". `xfce4-terminal` also ignores
`terminalrc`; colors must be in `xfce4-terminal.xml` under Xfconf.

### 2. Stock image unpack

First pull failed with `usr/bin/thunar: openat usr/bin/thunar: too many levels
of symbolic links`. The image has `usr/bin/Thunar -> thunar` and the regular
file `usr/bin/thunar`; macOS APFS is case-insensitive, so the alias became a
self-link and `os.Root.OpenFile` followed it into `ELOOP`. The unpacker now
removes a symlink before writing a directory, regular file or hard link over it.

### 3. ICMP ping

`ping google.com` works with real replies and 0% packet loss. A Linux ping
socket (`SOCK_DGRAM/IPPROTO_ICMP`) delivers only the ICMP message; Darwin's
unprivileged ICMP datagram socket prepends the IPv4 header, which iputils
discards as a non-echo type. The supervisor sets `IP_STRIPHDR` on that host
socket. Live A/B: 0% loss with the fix, 100% without. `SOCK_RAW` ICMP and
`ping -6` remain unsupported.

### 4. Network / "wifi"

`-net` provides working egress. Live witnesses inside a `-net` guest:

```
getent hosts google.com -> AAAA records, rc=0
ping -c 3 -W 3 google.com -> 3 received, 0% packet loss
curl https://example.com/ -> HTTP 200 104.20.23.154
apt-get update -> Fetched 65.2 MB; apt-get install tree -> rc=0
apk update / apk add -> rc=0 (Alpine guest; see note below)
Chromium 150.0.7871.100 renders https://example.com/ over VNC
```

The supervisor answers every `:53` query and `-net` makes `connect()` use a
real host socket. The runtime does not generate `/etc/resolv.conf` (musl and
glibc fall back to `127.0.0.1:53`); `start-xubuntu.sh` writes an explicit
`nameserver 8.8.8.8` for deterministic glibc clients. `-allow-domains` /
`-allow-cidrs` / `-allow-ports` only *restrict*; adding them breaks egress.
`getent hosts` returns AAAA records that are not reachable; `curl -4` works,
`curl -6` returns `HTTP 000`. IPv6 egress is a known limitation.

**802.11 wifi cannot be emulated.** qqbox pairs socket endpoints, not packets:
no `AF_PACKET`/TUN, raw sockets, ICMP-in-guest-kernel, nl80211 or wireless
extensions. NetworkManager/wpa_supplicant/`iw` are absent. Working DNS+TCP+
HTTPS egress and a browser that loads real pages is the honest equivalent.

**apk** is Alpine's package manager. `apt` works in this Ubuntu image; `apk`
was witnessed in a separate Alpine guest (`apk update`, `apk add ripgrep jq`,
rc=0). A static aarch64 `apk` binary can also be added to this image, but it
manages an Alpine root, not the Ubuntu dpkg database.

### 5. Chromium: sandbox and GPU (honest limits)

A real Debian bookworm **Chromium 150.0.7871.100** linux/arm64 browser is
installed, not NetSurf, not the Ubuntu snap stub, and not Chrome-for-Testing
158 (which segfaults instantly under qqbox, rc=139 in every flag combination).
It renders the live `https://example.com/` page over VNC. Launch flags:

```
--no-sandbox --single-process --no-zygote --js-flags=--jitless
--use-gl=angle --use-angle=swiftshader --enable-unsafe-swiftshader
--disable-dev-shm-usage --disable-crash-reporter --disable-breakpad
```

| limit | measured reason |
| --- | --- |
| `--no-sandbox` required | Chromium refuses to run as root with sandbox on (`zygote_host_impl_linux.cc:101`); qqbox's `execve` ignores setuid, so the SUID helper cannot gain privileges. Installing a seccomp-bpf filter kills the qqbox stub; the zygote PID-namespace `SCM_CREDENTIALS` handshake is unimplemented. There is no `--enable-features` switch that turns the sandbox on. |
| No hardware GPU | `/dev/dri` does not exist; no render nodes and no host GPU passthrough. SwiftShader software GL works: a real `gl.clear()`/`readPixels()` round-trip returns `WebGL 1.0 (OpenGL ES 2.0 Chromium)`, `glError=0`. `--disable-gpu` yields `WEBGL_NULL`. |
| Site isolation / JIT off | `--single-process --no-zygote --js-flags=--jitless` are needed because the multi-process zygote path hangs (rc=124) and V8 JIT needs RWX pages the supervisor does not grant. This trades Chromium's renderer isolation for a working window. |
| Headless | `--headless=new --dump-dom` times out (rc=124); windowed rendering under Xvfb is the supported path. |

The Chromium sandbox is a **security boundary**, not a performance feature:
enabling it typically costs process/IPC overhead. The user's premise that
sandbox-on improves performance/stability is not technically correct, but the
sandbox-on configurations were attempted live anyway; see
`examples/qqbox/` and `FIXES.json`.

## Why not the image's own `/init`, KasmVNC or Firefox

`/init` is s6-overlay; qqbox runs one program and is not an init system. Stock
KasmVNC `Xvnc` completes the RFB 3.8 handshake in traditional mode
(`-rfbport N -noWebsocket -SecurityTypes None`) but sends **no** framebuffer
updates (`ComparingUpdateTracker: 0 pixels in / 0 pixels out`). `x11vnc` +
`Xvfb` is the proven pair.

Firefox **is installed** (stock) but cannot render under qqbox: its content
sandbox calls `chroot()`, refused by the vfs-Namespace-only filesystem
(`Sandbox: chroot: EPERM`), and its renderers exit on signal 11. Five
sandbox-disable variants were tried; all still show a blank page.

## Security note

`x11vnc -nopw` is wildcard-bound and LAN-reachable under `-net`. `-localhost`
is **not** a valid fix (it silently breaks qqbox port forwarding). To close it,
use `x11vnc -rfbauth <vncpasswd>` and `vncviewer -passwd <file>`. The shipped
default stays passwordless so the existing local connection keeps working; this
is a deployment choice, not a qqbox security invariant.

## Known warnings

`xdg-user-dir` is missing from the Xubuntu template script, AT-SPI/UPower/
PolicyKit services are absent, `SOCK_RAW` ICMP is unavailable, IPv6 egress is
not reachable, and the default browser (`firefox.desktop`) is not the browser
the startup script launches (Chromium). The terminal prompt's hostname is
addressed by `examples/qqbox/`.

## Files

| file | purpose |
| --- | --- |
| `Dockerfile` | pinned `FROM` the arm64 webtop digest |
| `start-xubuntu.sh` | boots Xvfb → x11vnc → stock XFCE → native terminal → Chromium |
| `FIXES.json` | claim-by-claim derivation and verdicts |
| `netsurf-http.png` | earlier NetSurf witness (superseded by Chromium) |
| `xubuntu-live-5905.png` | live 5905 cutover: Xubuntu wallpaper + browser + native terminal |
| `live-terminal-witness.png` | VNC-typed `FINAL_5905_NATIVE_OK` / `Ubuntu 24.04 LTS` / `HTTP 200` / `DONE` |
| `network-evidence.txt` | `getent` + `curl` HTTP 200 for example.com and google.com |
| `xubuntu-xwd.png` | stock XFCE session captured with `xwd` off the added Xvfb `:99` |
