# XFCE desktop inside laboon

A reproducible, witnessed XFCE 4.18 session running inside a laboon sandbox on
macOS/arm64. `xfce-desktop.png` is the actual desktop captured from inside the
guest; `xfce-vnc.png` is the same desktop captured by a host-side VNC client.

This uses the **Go native runtime**: the Rust port on `feat/rust-port` implements
the control plane but cannot execute guests yet (see `docs/PORT_STATUS.md`).

## Build

```sh
go build -o ./laboon ./cmd/laboon
./laboon build -net -t laboon-xfce:latest \
  -f examples/xfce/Dockerfile -o /tmp/laboon-xfce.tar examples/xfce
```

`-net` is required: the `RUN apk add` step needs outbound network. The build
installs 311 Alpine/arm64 packages (463 MiB), including `xfce4`, `xfce4-session`,
`xfwm4`, `xfdesktop`, `xfce4-panel`, `xfce4-terminal`, `xvfb`, `x11vnc`, `scrot`
and X11 fonts/themes.

## Run

```sh
mkdir -p /tmp/laboon-xfce-out
cp examples/xfce/start-xfce.sh /tmp/laboon-xfce-out/run.sh
./laboon -image laboon-xfce:latest \
  -mount /tmp/laboon-xfce-out:/out -net sh /out/run.sh
```

`-mount HOST:GUEST` is a real read-write bind, so the guest writes its evidence
into `/tmp/laboon-xfce-out` on the host. The script:

1. disables `xfwm4` compositing in `xfconf` — with compositing enabled the X root
   window stays black in an `XGetImage`/`scrot` capture;
2. starts `Xvfb :99 -screen 0 1280x800x24`;
3. starts the system D-Bus daemon and `startxfce4` under `dbus-run-session`;
4. launches an `xfce4-terminal` window;
5. writes `processes.txt`, `wm.txt`, `windows.txt`, `stages.log`, `xfce.png` and
   `xfce.xwd` into the host mount;
6. starts `x11vnc` on TCP 5905 and keeps the desktop alive.

## Live evidence (2026-11-18, macOS arm64)

Guest process table (`processes.txt`) showed all of these running simultaneously:
`xfce4-session`, `xfwm4`, `xfsettingsd`, `xfce4-panel` (systray, power-manager
and actions plugins), `xfdesktop`, `Thunar --daemon`, `xfconfd`, `tumblerd` and
the at-spi registry. That is 20+ processes in one sandbox — the macOS backend's
documented "single-threaded" scope did **not** prevent a threaded XFCE session.

`xfce-desktop.png` shows the XFCE top panel (Applications menu, window list,
clock), the default XFCE wallpaper, "File System" and "Home" desktop icons, the
`LABOON-XFCE-WITNESS` terminal window and the bottom dock.

A second, independent capture used a minimal RFB 3.8 client
(`rfb-capture.py`) connecting to the guest's `x11vnc` over TCP:

```
RFB 003.008 / security None / ServerInit 1280x800 32bpp name='laboon:99'
1 Raw rect, 4,096,000 bytes, 3,688 distinct colours -> xfce-vnc.png
```

That proves a host VNC client can attach to the desktop while it is running.

## Limitations observed

- `xfce4-terminal` shows **"Failed to execute child: Failed to open PTY: Not a
  tty"**. laboon wires guest stdio through pipes, so the terminal's child cannot
  allocate a PTY. The desktop, panel, wallpaper and window manager are unaffected.
- The Go CLI has **no `-p`/`--publish` flag** and the Go daemon ignores
  `HostConfig.PortBindings`. Nevertheless, with `-net`, a guest-bound TCP port
  was reachable at `127.0.0.1:5905` on this host. Treat that as an observed
  behaviour of this backend, not a documented publishing contract.
- `x11vnc -unixsock` is not usable here: x11vnc 0.9.16 denies AF_UNIX clients
  with `check_access: denying empty host IP address string`. Five `-allow`
  variants were tried; all failed. Use TCP instead.
- `xprop`, `xwininfo`, `xdpyinfo`, `xsetroot`, `xrdb` and `xwd` are separate
  Alpine packages (`xprop`, `xwininfo`, ...), not `xorg-*`; the first run omitted
  them, so `wm.txt`/`windows.txt` are empty there. `scrot` and `imagemagick`'s
  `import` are in the image and work.
- Alpine's `dbus-daemon --system` needs `/var/run/dbus` to exist; the script
  creates it. Several `xfce4-power-manager` warnings are expected (no system bus,
  no `/sys/class/backlight`, no DPMS on Xvfb).
- Only `linux/arm64` guests run on this host. An amd64 image is rejected by
  `internal/oci/oci.go`.

## Files

| file | purpose |
| --- | --- |
| `Dockerfile` | Alpine 3.20 + full XFCE/Xvfb/VNC stack |
| `start-xfce.sh` | boots the session and writes evidence |
| `rfb-capture.py` | minimal RFB 3.8 client (no VNC library) |
| `xfce-desktop.png` | guest-side `scrot` capture |
| `xfce-vnc.png` | host-side VNC framebuffer capture |
| `processes.txt` | guest process table proving the session was live |
