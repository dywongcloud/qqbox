# XFCE desktop inside qqbox

A reproducible, witnessed XFCE 4.18 session running inside a qqbox sandbox on
macOS/arm64, served over VNC. `xfce-desktop-native.png` shows the native
`xfce4-terminal` rendering real shell output; `xfce-terminal-typed.png` shows a
command typed through VNC, its echoed input, its output and the prompt. The
browser-based pipe console is no longer started by default; it remains an
opt-in, hardened, loopback-only fallback (`terminal-console.py`) for hosts
where a PTY cannot be used. The native terminal is the shipped default and
Chromium opens a public page, not the console.

For a **stock Xubuntu/XFCE** desktop (`linuxserver/webtop:ubuntu-xfce`), see
[`examples/xubuntu/`](../xubuntu/README.md).

This uses the **Go native runtime**: the Rust port on `feat/rust-port` implements
the control plane but cannot execute guests yet (see `docs/PORT_STATUS.md`).

## Build

```sh
cargo build --workspace
qqbox build -net -t qqbox-xfce:fixed \
  -f examples/xfce/Dockerfile -o /tmp/qqbox-xfce.tar examples/xfce
```

`-net` is required: the `RUN apk add` step needs outbound network. The image is
Alpine 3.20 arm64 with 378 packages (~1.1 GiB) — full XFCE, Xvfb, x11vnc,
Chromium 131, fonts, X diagnostics and an opt-in PTY-free command console.

## Run

```sh
mkdir -p /tmp/qqbox-xfce-out
./qqbox -image qqbox-xfce:fixed -net \
  -e VNC_PORT=5905 -mount /tmp/qqbox-xfce-out:/out \
  sh /usr/local/bin/start-xfce.sh
/opt/TurboVNC/bin/vncviewer 127.0.0.1:5905
```

`-mount HOST:GUEST` is a real read-write bind, so the guest writes evidence into
the host directory. The startup script starts `x11vnc` immediately after Xvfb
(VNC is reachable in ~7s), then D-Bus, `startxfce4`, the native
`xfce4-terminal` and Chromium on a public page. It stays live until the outer
`gtimeout` stops it.

## The two fixes

### 1. Terminal: "Failed to execute child / Failed to open PTY: Not a tty"

**Root cause (found by the adversarial swarm):** PTYs work in qqbox —
`pty.openpty()` and `pty.fork()` both succeed, `/dev/ptmx` and a `devpts` mount
exist. The failure is one unhandled ioctl: Linux `TIOCPKT` (`0x5420`) was missing
from `internal/sandbox/lx_termios.go` and from the `ttyIoctl`/`ptmxIoctl`
switches, so it fell through to `sErr(lxENOTTY)` — exactly VTE's "Not a tty".

The repo now carries that fix:

| file | change |
| --- | --- |
| `internal/sandbox/lx_termios.go:35` | `lxTIOCPKT = 0x5420` |
| `internal/sandbox/pty_unix.go` | `case lxTIOCPKT` in `ptmxIoctl` forwards packet mode to the host pty master via `unix.IoctlSetPointerInt` |
| `internal/sandbox/termios_unix.go` | guarded `case lxTIOCPKT` in `ttyIoctl` for the slave |

`TIOCPKT` takes a **pointer** to an int, not the int. On Darwin the request is
`0x80047470` (`_IOC_IN`, 4-byte payload), so passing `1` as the value makes the
kernel dereference address 1 and VTE reports `Failed to open PTY: Bad address`.
`unix.IoctlSetPointerInt` is correct on both Darwin and Linux. Merely returning
success without enabling packet mode removed the error but left VTE reading a
stream it could not frame, so the terminal stayed black.

`xfce4-terminal` ignores `terminalrc` ("settings have been migrated to Xfconf").
The stock transparent background paints black-on-black under Xvfb with
compositing off, so `start-xfce.sh` writes `xfce4-terminal.xml` into Xfconf with
a solid black background and white foreground.

Witnesses: `xfce-desktop-native.png` shows the child shell's output in a real
`xfce4-terminal` window; `xfce-terminal-typed.png` shows a command typed over
VNC (`echo VNC_NATIVE_TERMINAL_OK; id; uname -a`), its echoed input, its output
and the `/ #` prompt. A direct `fcntl.ioctl(master, TIOCPKT, struct.pack('i',1))`
witness returns 0.

**The shipped default terminal is the native `xfce4-terminal`.** The dock and
Applications-menu entries point at it. The browser-based
`terminal-console.py` is kept only as a fallback and is hardened (loopback-only
bind, `Origin`/`Host` checks, `application/json` required, body/output caps,
400 on malformed input, `no-store`/`nosniff` headers). Verdict:
`SAFE_WITHIN_SANDBOX` (guest `uid=0` is emulated; the host supervisor runs as
uid 501). It is **not** meant to run on the host.

### 2. Chromium

`chromium` + `chromium-swiftshader` (131.0.6778.108) and fonts are installed.
It MUST be launched **multi-process**:

```sh
chromium --no-sandbox --disable-gpu --disable-dev-shm-usage \
  --disable-crash-reporter --disable-breakpad --mute-audio \
  --disable-infobars --no-first-run --window-size=1100,720 \
  http://127.0.0.1:8765/
```

Do **not** pass `--single-process` or `--no-zygote`: with those, PulseAudio's
`pa_mutex_new()` assertion (`mutex-posix.c:57`) fires in the browser process and
Chromium aborts with exit 134. A five-variant launch matrix witnessed this
(multi-process variants rc=0 with a screenshot PNG; single-process variants
rc=134 with no PNG). `--version` and a headless `--screenshot` both work.

## Adjacent fixes

- Removed `xfce4-pulseaudio-plugin`: it loads libpulse and hit the same
  `pa_mutex_new()` abort, producing a blocking "Plugin Restart" dialog.
- Custom `xfce4-panel.xml` with only working plugins (no pulseaudio, no
  notification, no undefined `plugin-7`).
- Exact Alpine X tool names: `xprop`, `xwininfo`, `xdpyinfo`, `xsetroot`,
  `xrdb`, `xwd`, `xdotool` (not `xorg-*`). `wm.txt` now holds a real
  `_NET_SUPPORTING_WM_CHECK` window id.
- `mkdir -p /var/run/dbus` before the system D-Bus daemon.
- Disable `xfwm4` compositing via xfconf so the X root is screencapturable.
- Installed `mousepad`, `pm-utils`, `xfce4-notifyd`, `xfce4-clipman-plugin`,
  `font-noto`, `font-noto-cjk`, `hicolor-icon-theme`.
- `x11vnc` starts before `startxfce4` (7s VNC readiness vs ~40s).
- No fixed `sleep 7100`; the desktop is bounded by the outer `gtimeout`.

## Security note: VNC is unauthenticated

`x11vnc` is started with `-nopw` (security type `None`). Under qqbox's `-net`
port forwarding that listener is bound to the host's wildcard
(`*:5905`, IPv4 and IPv6), so it is reachable from the LAN — an adversarial
verifier confirmed an RFB handshake against the host's LAN address. This does
**not** cross a qqbox boundary (the console runs as an emulated guest root;
the host supervisor is uid 501), but anyone on the LAN can drive the guest
console while it is running.

`-localhost` is **not** a valid fix: a live probe showed it silently breaks
qqbox's `-net` port forwarding (host connections are refused). To close it,
start x11vnc with a password:

```sh
x11vnc -display :99 -rfbauth /path/to/vncpasswd -rfbport 5905 ...
```

then connect with `vncviewer -passwd /path/to/vncpasswd 127.0.0.1:5905`. The
shipped default stays passwordless so the existing local connection keeps
working; set a password before exposing the desktop on an untrusted network.
This is a deployment choice, not a qqbox security invariant.

## Known remaining warnings (expected under qqbox)

Chromium shows an `--no-sandbox` unsupported-flag infobar (cosmetic).
`xfce4-power-manager` CRITICALs, `xfdesktop` GLib-CRITICALs, missing
`/sys/class/backlight`, UPower, PolicyKit and DPMS all stem from qqbox's
synthetic `/sys` and Xvfb and do not affect the desktop. Guest `/dev/pts`
enumeration is synthetic (no slave nodes listed) though open-by-path works.

Only `linux/arm64` guests run on this host; an amd64 image is rejected by
`internal/oci/oci.go`. The Go CLI has no `-p`/`--publish`, but with `-net` a
guest-bound TCP port is reachable on host loopback (observed, not a documented
publishing contract).

## Files

| file | purpose |
| --- | --- |
| `Dockerfile` | Alpine 3.20 + XFCE/Xvfb/VNC/Chromium/console |
| `start-xfce.sh` | boots the fixed session, writes evidence, stays live |
| `terminal-console.py` | hardened, opt-in PTY-free pipe command console (not started by default) |
| `rfb-capture.py` | minimal RFB 3.8 framebuffer client (no VNC library) |
| `rfb-type.py` | injects keyboard/pointer events over RFB |
| `FIXES.json` | claim-by-claim derivation, evidence and verdicts |
| `xfce-desktop-fixed.png` | fixed desktop over VNC |
| `xfce-desktop-native.png` | native VTE terminal rendering real shell output |
| `xfce-terminal-typed.png` | command typed over VNC, echoed, executed, with prompt |
| `xfce4-terminal-native.png` | native VTE windows after the TIOCPKT fix |
| `terminal-console-typed.png` | earlier evidence from the opt-in pipe-console fallback |
| `chromium-version.txt`, `console-probe.txt`, `processes-fixed.txt`, `wm.txt`, `xdpyinfo.txt`, `xfce-session-fixed.log` | earlier live evidence |
