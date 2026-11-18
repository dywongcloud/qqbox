# Stock Xubuntu/XFCE desktop inside laboon

A live, witnessed `linuxserver/webtop` **ubuntu-xfce** desktop running under the
Go native laboon runtime on macOS/arm64, served over VNC on TCP 5905, with a
real `xfce4-terminal` (VTE) and working DNS/TCP/HTTPS egress.

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

## Build

```sh
go build -o ./laboon ./cmd/laboon
./laboon build -net -t laboon-xubuntu:stock \
  -f examples/xubuntu/Dockerfile -o laboon-xubuntu.tar examples/xubuntu
```

`FROM` is the pinned arm64 digest. `-net` is required for the `RUN apt-get` step.
Only `x11vnc`, `Xvfb`, `scrot`, `xdotool` and `netsurf-gtk` are added — the
rootfs, XFCE session, Xubuntu theme, Firefox, Thunar and `xfce4-terminal` are
the image's own. NetSurf is the default browser because Firefox's content
sandbox cannot run under laboon (see below).

## Run

```sh
mkdir -p /tmp/laboon-xubuntu-out
./laboon -image laboon-xubuntu:stock -net \
  -e VNC_PORT=5905 -mount /tmp/laboon-xubuntu-out:/out
/opt/TurboVNC/bin/vncviewer 127.0.0.1:5905
```

`-net` is **not optional**: it is the switch that enables the supervisor's DNS
interception and host-socket egress. Without it Chromium/Firefox render the
generic offline page whose boilerplate says "Reconnecting to Wi-Fi".

## Three fixes

### 1. A real native terminal

The browser-based pipe console is gone from the default. `xfce4-terminal` now
spawns a real PTY child and paints text. Root cause was Linux `TIOCPKT`
(`0x5420`) missing from `internal/sandbox/lx_termios.go` and the ioctl switches;
VTE got `ENOTTY` → "Failed to open PTY: Not a tty". Forwarding it to the host
pty master fixes both the error and the black screen — but only with
`unix.IoctlSetPointerInt`: `TIOCPKT` takes a **pointer** to an int (Darwin
`0x80047470`, `_IOC_IN`), so passing the value gives `EFAULT` →
"Failed to open PTY: Bad address". `xfce4-terminal` also ignores `terminalrc`;
colors must be set in `xfce4-terminal.xml` under Xfconf.

Witness (`examples/xfce/`): `xfce-terminal-typed.png` shows a command typed over
VNC, echoed input, `VNC_NATIVE_TERMINAL_OK`, `uid=0(root)`, `uname -a` and the
`/ #` prompt. The stock desktop's `xubuntu-xwd.png` shows `root@laboon:/#`.

### 2. Stock image unpack

First pull failed with `usr/bin/thunar: openat usr/bin/thunar: too many levels
of symbolic links`. The image contains `usr/bin/Thunar -> thunar` and the
regular file `usr/bin/thunar`; macOS APFS is case-insensitive, so the alias
becomes a self-link and `os.Root.OpenFile` followed it into `ELOOP`.
`internal/oci/unpack_unix.go` now removes a symlink before writing a directory,
regular file or hard link over it. `go test ./internal/oci/...` passes.

### 3. Network / "wifi"

`-net` already provides working egress; the failure mode is launching without
it. Live witnesses inside a `-net` guest:

```
getent hosts example.com -> 2606:4700:10::ac42:93f3 example.com
raw UDP DNS to 127.0.0.1:53 / 8.8.8.8:53 / 1.1.1.1:53 -> rcode=0, 2 answers
TCP 1.1.1.1:443 OK, example.com:443 OK
curl https://example.com/ -> HTTP 200 104.20.23.154
curl https://www.google.com/ -> HTTP 200 142.251.153.119
apt-get update inside the stock Xubuntu guest -> Fetched 65.2 MB
windowed Chromium on Xvfb -> "Example Domain - Chromium" with the real page
NetSurf on the stock rootfs -> "Example Domain - NetSurf", real page (netsurf-http.png)
VNC-typed curl in the native terminal -> LIVE_5905_XUBUNTU_OK / Ubuntu 24.04 LTS / HTTP 200 (live-terminal-witness.png)
```

`internal/sandbox/egress.go:99-127` allows everything except loopback and
unspecified addresses by default; every `:53` query is answered in the
supervisor (`net_unix.go:872-879`, `msg_unix.go:658-713`), and `-net` makes
`connect()` use a real host socket (`net_unix.go:317-321,889-895`). The Go
runtime does **not** generate `/etc/resolv.conf` (musl and glibc fall back to
`127.0.0.1:53`); `start-xubuntu.sh` writes an explicit `nameserver 8.8.8.8`
for deterministic glibc clients. `-allow-domains` / `-allow-cidrs` /
`-allow-ports` only *restrict*; adding them breaks egress.

**802.11 wifi cannot be emulated.** laboon pairs socket endpoints, not packets
(`internal/sandbox/VNET.md:19-26`): no `AF_PACKET`/TUN, no raw sockets, no
ICMP, no nl80211/wireless extensions. Netlink `RTM_GETLINK` mirrors the host's
interfaces (`lx_netlink.go:208-232`). NetworkManager/wpa_supplicant/`iw` are
absent from the image. `ping` failing is expected, not an outage. The honest
equivalent of "network working" — DNS, TCP and HTTPS egress, and a browser that
loads real pages — is what `-net` delivers.

## Why not the image's own `/init`, KasmVNC or Firefox

`/init` is s6-overlay; laboon runs one program and is not an init system. The
stock KasmVNC `Xvnc` can be started in traditional RFB mode
(`-rfbport N -noWebsocket -SecurityTypes None`) and completes the RFB 3.8
handshake with `ServerInit 1280x800 bpp=32 depth=24`, but sends **no**
framebuffer updates under laboon (`ComparingUpdateTracker: 0 pixels in / 0
pixels out`, `EncodeManager: 0 rects, 0 pixels`, across four encoding sets and
`-CompareFB 0`). `x11vnc` + `Xvfb` are the pair already proven in this repo, so
the Dockerfile adds only those. The stock XFCE session itself
(`/usr/bin/xfce4-session`) is unchanged.

Firefox **is installed** (stock) but cannot render under laboon: its content
sandbox calls `chroot()`, which the vfs-Namespace-only filesystem refuses
(`Sandbox: chroot: EPERM`), and its renderers exit on signal 11.
`MOZ_DISABLE_CONTENT_SANDBOX=1`, `MOZ_FORCE_DISABLE_E10S=1`,
`security.sandbox.content.level=0` and every other sandbox-disable variant
were tried; all still show a blank page. NetSurf (`netsurf-gtk`), added from
Ubuntu's own apt sources, renders both a local `file:` page and the live
`https://example.com/` page — `netsurf-http.png` is that witness. Firefox is
left installed but is not the default browser.

## Security note

`x11vnc -nopw` is wildcard-bound and LAN-reachable under `-net`. `-localhost`
is **not** a valid fix (it silently breaks laboon's port forwarding). To close
it, use `x11vnc -rfbauth <vncpasswd>` and `vncviewer -passwd <file>`. The
shipped default stays passwordless so the existing local connection keeps
working; this is a deployment choice, not a laboon security invariant.

## Known warnings (expected under laboon)

Firefox refuses to run as root when `$HOME` is owned by a non-root uid (the
startup script `chown`s `/config`) and its content sandbox is unusable under
laboon, so NetSurf is the default browser. `xdg-user-dir` is missing from the Xubuntu
template script, AT-SPI/UPower/PolicyKit services are absent, and `ping` cannot
open raw sockets. None of these affect the desktop, terminal or egress.

## Files

| file | purpose |
| --- | --- |
| `Dockerfile` | pinned `FROM` the arm64 webtop digest; adds x11vnc/Xvfb/NetSurf |
| `start-xubuntu.sh` | boots Xvfb → x11vnc → stock XFCE → native terminal → NetSurf |
| `FIXES.json` | claim-by-claim derivation and verdicts |
| `netsurf-http.png` | NetSurf rendering the real `https://example.com` page |
| `xubuntu-live-5905.png` | live 5905 cutover: Xubuntu wallpaper + browser + native terminal |
| `live-terminal-witness.png` | VNC-typed `FINAL_5905_NATIVE_OK` / `Ubuntu 24.04 LTS` / `HTTP 200` / `DONE` |
| `network-evidence.txt` | `getent` + `curl` HTTP 200 for example.com and google.com |
| `xubuntu-xwd.png` | stock XFCE session on the image's own KasmVNC X server |
