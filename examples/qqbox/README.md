# Fully provisioned Tencent qqbox Xubuntu 26 desktop

This is the reproducible Xubuntu **26.04.1 LTS (Resolute Raccoon)** desktop,
running under the native qqbox guest runtime on macOS/arm64 and served over VNC
TCP 5905. It is the successor to `examples/xubuntu/` (Ubuntu 24.04).

The base is `linuxserver/webtop` `ubuntu-xfce-ce206d66-ls292`, pinned to the
linux/arm64 manifest `sha256:4721b798561ebabc9bfee60d3220f8c5d57b17fb78ab83168c94628e07e55e99`.
Live witness: `PRETTY_NAME="Ubuntu 26.04.1 LTS"`, `VERSION_CODENAME=resolute`,
`xubuntu-wallpapers 26.04.1`, `/etc/apt/sources.d/ubuntu.sources` → `resolute`.

**Honest boundary:** this is Ubuntu 26.04 + XFCE + Xubuntu's own 26.04 settings,
artwork and wallpapers, not the Xubuntu *distribution* (`/etc/os-release` still
says `ID=ubuntu`). Xubuntu 26.04 publishes no arm64 OCI image and xubuntu.org
does not publish OCI images at all; `linuxserver/webtop` has no `xubuntu` tag.
`xubuntu-desktop` 2.272 *is* available in the resolute archive, but installing
that metapackage would replace the already-working desktop, so it is not the
default. This is the exact Ubuntu 26 base carrying Xubuntu 26 artwork.

| requirement | status |
| --- | --- |
| Xubuntu 26 | Ubuntu 26.04.1 LTS + Xubuntu 26.04.1 settings/artwork/wallpapers, witnessed in `/etc/os-release`. |
| wallpaper / backgrounds | Real Xubuntu 26 backdrop renders via `xwallpaper --zoom /usr/share/xfce4/backdrops/xubuntu-wallpaper.png`. 26.04's glycin/gdk-pixbuf loaders need `bwrap`, which fails under qqbox, so xfdesktop alone paints a solid root; `xwallpaper` uses libpng directly. Live 26.04 screenshot: 17,855 distinct colors. xfconf still records `monitorscreen/workspace0/last-image` with `image-show=false` so xfdesktop doesn't repaint over it. |
| bottom dock/taskbar | XFCE panel `p=10;x=0;y=0` → `1280x41+0+759` on an 800px screen, applications menu, three launchers, tasklist. |
| vi / vim | `vim 9.1`, `vim-tiny` (the `vi` binary). Both witnessed editing a file with rc=0 and verified contents. |
| GNU userland | ~70 common packages: coreutils, findutils, grep, gawk, sed, diffutils, patch, make/gcc/g++/binutils/build-essential, autoconf/automake/libtool/pkg-config/cmake/ninja/gdb, tar/gzip/bzip2/xz/zip/unzip/cpio, procps/psmisc/util-linux, net-tools/iproute2/dnsutils/traceroute, wget/curl/rsync/openssh-client, less/man-db/nano/tree/file/which, jq/htop/bc/time/strace/lsof/tmux/screen, gnupg, locales/gettext, socat/netcat/telnet/xxd, pciutils/usbutils/parted/e2fsprogs, perl, fastfetch. |
| apt / package registries | Works repeatedly after the DBus fix (`fastfetch`/`tree`/`htop` rc=0). `neofetch` was removed from the 26.04 archive: `apt-get install -y neofetch` returns rc=100 `E: Unable to locate package neofetch` even after `apt-get update`; `fastfetch` 2.57.1 is the shipped substitute. `npm` is likewise unavailable from apt on 26.04 — npm 9.2.0 comes from the Node 18 payload. |
| GDBus setuid helper | **Fixed** — root-user system bus config, see below. |
| real Chromium | Debian bookworm Chromium 150.0.7871.100 `linux/arm64`, four bundled bookworm libs for resolute. Renders `https://example.com/` (DOM 2,375 bytes) over VNC. |
| Chromium sandbox | **NOT ACHIEVABLE** — 27 sandbox-enabled variants tried, rc=1/134. See below. |
| GPU | No `/dev/dri`; SwiftShader WebGL 1.0 works (`gl.clear()`/`readPixels()` round trip, `glError=0`). |
| audio | PulseAudio null sink: `pactl info` rc=0, `paplay` rc=0. No `/dev/snd`/host playback. |
| prompt | `root@qbox` via `hostname qbox` + `PS1='\u@qbox:\w\$ '`. |
| ping / DNS / HTTPS | `ping -c 3 google.com` 0% loss, `getent hosts` rc=0, `curl -4 https://example.com/` HTTP 200. IPv6 AAAA records are not reachable. |
| toolchain | `python3 3.14`, `pip3 25.1`, `node v18`, `npm 9.2`, `git 2.53`, `gh 2.46`, `claude 1.0.128` (pure-JS build). |
| browser extension | Real Tencent BrowserSkill 0.5.0 MV3 extension preloaded from `/opt/tencent-browserskill` plus a qqbox MV3 extension at `/opt/qqbox-extension`. With the required `--single-process --no-zygote`, MV3 service workers do not run; multi-process mode loads them. |
| Tencent browser skill | Resolves to `Tencent/BrowserSkill` (MIT): host `~/.tclaude/skills/browser-skill/SKILL.md` and `bsk` 0.5.0 (macOS Mach-O, cannot run in a Linux guest). Skill docs and the real MV3 extension are installed. |

## Build

```sh
sh fetch-chromium.sh     # downloads + extracts the pinned Debian Chromium debs
sh fetch-extras.sh       # copies the real Tencent BrowserSkill extension/skill,
                         # downloads the pure-JS Claude Code 1.0.128 npm tarball
QQBOX_RUNTIME=/path/to/pre-removal-laboon-runtime
"$QQBOX_RUNTIME" build -net -t qqbox-xubuntu:26 -f Dockerfile \
  -o qqbox-xubuntu26.tar .
```

`build` and `-image` below are commands of the **pre-removal Go runtime
binary**, preserved off-repo to boot this desktop. The Rust `qqbox` CLI in this
tree has no `build` subcommand and no `-image` flag (`qqbox build --help`
returns rc=2).

Neither `chromium/` (~339 MiB) nor `claude/claude-code.tgz` (~34 MiB) is
committed. `fetch-extras.sh` refuses to invent the Tencent BrowserSkill artifacts
if the host has not installed them.

`-net` is required for the `RUN apt-get` steps. Chrome-for-Testing 158 was tried
and segfaults instantly under qqbox (rc=139 in every flag combination), so
Debian Chromium 150 is used instead.

## Run

```sh
mkdir -p /tmp/qqbox-out
"$QQBOX_RUNTIME" -image qqbox-xubuntu:26 -net -e VNC_PORT=5905 \
  -mount /tmp/qqbox-out:/out
/opt/TurboVNC/bin/vncviewer 127.0.0.1:5905
```

The startup script writes `requirements.txt`, `windows.txt`,
`qqbox-desktop.png`, `01-wallpaper.png`, `audio.txt`, `hostname.txt`,
`dbus-ping.txt`, `pulseaudio.log`, `chromium.log` and `result.txt` into `/out`.

## The GDBus setuid-helper failure (fixed)

Reproduced verbatim inside a guest with `dbus-daemon --system --fork` running:

```
$ gdbus call --system --dest org.freedesktop.PackageKit --object-path / \
    --method org.freedesktop.DBus.Peer.Ping
Error: GDBus.Error:org.freedesktop.DBus.Error.Spawn.PermissionsInvalid:
The permission of the setuid helper is not correct
```

The same error appeared in `apt-get -f install` output (line 165 of the log).
Root cause: `dbus-daemon --system` drops to the `messagebus` user and activates
system services (`PackageKit`, `systemd1`, …) through
`/usr/lib/dbus-1.0/dbus-daemon-launch-helper`, a setuid-root binary. qqbox's
`execve` **ignores `S_ISUID`** (the same reason Chromium's SUID sandbox helper
cannot work), so the helper cannot gain root and the stat-based permission check
fails. The helper's mode is fine (`4754 root:messagebus`); chmod'ing it to 4755
or 4750 does not help (both still rc=1). It is a supervisor property, not a
guest file-mode bug.

Fix, verified in the live 26.04 boot: start the system bus as **root** with
`/etc/dbus-1/qqbox-system.conf` (`<user>root</user>`, explicit `<listen>`,
standard system service dirs), **and** remove the two triggers that ask the bus
to activate a `User=root` service:

```sh
rm -f /etc/apt/apt.conf.d/20packagekit \
      /usr/share/dbus-1/system-services/org.freedesktop.PackageKit.service
```

Witnessed: `dbus_ping_rc=0`, `NO_DBUS_ERROR` across all apt logs,
`apt-get update` rc=0, and repeat `apt-get install fastfetch` / `tree` rc=0
with `dpkg-query` reporting `install ok installed`. A real fix would be
supervisor-side: honour `S_ISUID` on emulated `execve`.

**Adversarial corrections.** An adversarial verifier independently refuted the
claim that this line is what made `apt-get install` fail on the old 24.04
image: there the rc=100 came from 17 unpacked-but-unconfigured `npm`-stack
packages (`node-neo-async`'s `angelfall.js` vs `AngelFall.js` collision on the
case-insensitive host filesystem). Removing the two D-Bus triggers alone
silences the stderr line but leaves those installs at rc=100; conversely, with
the triggers intact and a repaired dpkg state, installs return rc=0 **while
still printing the identical error line**. On this 26.04 image both are fixed:
no `apt npm` is installed (npm ships as a registry tarball), the usrmerge
`ldconfig` divert keeps dpkg consistent, and the root-user bus plus trigger
removal stops the message. Witnessed in `live-5926/requirements.txt`:
`apt-get update` rc=0, repeat `fastfetch` and `tree` installs rc=0 with
`install ok installed`, `NO_DBUS_ERROR`, and no unconfigured packages.

The error is emitted by a `gdbus` call inside
`/etc/apt/apt.conf.d/20packagekit` (an `APT::Update::Post-Invoke-Success`
hook), not by apt/dpkg itself. dbus 1.14's launch helper checks
`getuid()==messagebus && geteuid()==0`; qqbox's `execve` ignores setuid, so
the helper exits 7 (`BUS_SPAWN_EXIT_CODE_PERMISSIONS_INVALID`) and the daemon
maps that to the human string. There is no stat()-based mode check in this
D-Bus version: `chmod 4754` on the helper does not help, and the OCI unpacker's
S_ISUID stripping is real but not the cause. A root-user bus alone replaces the
error with a 4-second `Timeout was reached` in some 24.04 probes, so removing
the triggers is required as well. Seven other `User=root` service files
(`PolicyKit1`, `hostname1`, `locale1`, `login1`, `network1`, `systemd1`,
`timedate1`, `com.ubuntu.SoftwareProperties`) would fail the same way if a
client activates them; the shipped config keeps the bus as root to avoid that.

**Two related 26.04 dpkg fixes.** (1) The static-pie `/usr/sbin/ldconfig`
segfaults under qqbox's rewriter, so `libc-bin`'s postinst exits 139 and leaves
dpkg half-configured. Ubuntu 26.04 is usrmerged, so `libc-bin` owns
`/usr/sbin/ldconfig` — a plain overwrite is restored at unpack. The durable fix
is `dpkg-divert --local --rename --divert /usr/sbin/ldconfig.qqbox-real
/usr/sbin/ldconfig` plus a no-op script. (2) `apt-get` can have its HTTP method
killed (`Method http has died unexpectedly! … signal 9`) under host CPU/memory
contention; the build uses `Acquire::Retries=2` and
`Acquire::http::Timeout=25`.

## Live 5905 witness

The final `qqbox-xubuntu:26` image (built digest
`sha256:9fa9634a8dea82c92fca169c6fc9a5eeea6d072cd165a393d085a5d544b2b59c`) is
booted on VNC TCP 5905. `live-5905-x26-final/requirements.txt` records:

| check | live result |
| --- | --- |
| OS | `PRETTY_NAME="Ubuntu 26.04.1 LTS"`, `VERSION_ID="26.04"` |
| wallpaper | `xwallpaper_final_rc=0`; `01-wallpaper-final.png` 1280x800, 7,918 distinct colors; six sampled pixels match the source Xubuntu 26 backdrop byte-for-byte |
| independent RFB | RFB 3.8 over TCP 5905: `ServerInit` name `qbox:99`, 1280x800, 1 raw rect 4,096,000 bytes, 7,917 colors |
| toolchain | `v18.19.1`, `npm 9.2.0`, `1.0.128 (Claude Code)`, Python 3.14.4, pip 25.1.1, git 2.53.0, gh 2.46.0, Chromium 150.0.7871.100 |
| vi / vim | `vim_rc=0` → `qqbox-vim`, `vi_rc=0` → `qqbox-vi` |
| GNU tools | zero `MISSING` across ~50 probed binaries |
| audio | `paplay_rc=0`, PulseAudio null sink |
| network | `ping_rc=0` (0% loss), `curl -4` HTTP 200 |
| D-Bus fix | `dbus_list_rc=0`, `NO_DBUS_ERROR`; PackageKit ping returns `ServiceUnknown` (expected after trigger removal), never `PermissionsInvalid` |
| repeat apt | `apt_update_rc=0`, `fastfetch`/`tree`/`htop` all `rc=0` with `install ok installed` |
| desktop | `QQBOX_BOOT_READY`, `Example Domain - Chromium` and `QQBOX-TERMINAL` windows |

`01-wallpaper.png` (captured before Xvfb flushed the root pixmap) is solid white;
the deterministic final repaint `01-wallpaper-final.png` is the witness. The
`xfdesktop_procs=0` line reflects that 26.04's xfdesktop exits on a Wnck
assertion after the glycin loader failure — the wallpaper is painted by
`xwallpaper`, not by xfdesktop, and that is why the workaround is needed.

## Why Chromium's sandbox cannot be on

**The user's premise ("sandbox should be on for performance/stability") is
technically incorrect**: Chromium's Linux sandbox is a security boundary
(user/PID/mount namespaces + setuid helper or unprivileged user namespace +
seccomp-bpf syscall filtering). It generally *costs* performance (extra
processes, IPC, a BPF filter on every syscall). The sandbox-enabled configs
were attempted live anyway and none rendered.

| blocker | live evidence |
| --- | --- |
| Mandated flags are self-contradictory | `--single-process`/`--no-zygote` — the flags that make Chromium work under qqbox — are refused whenever the sandbox is on: `content_main_runner_impl.cc:348 Zygote cannot be disabled if sandbox is enabled`. |
| Root impossible | `zygote_host_impl_linux.cc:101 Running as root without --no-sandbox is not supported`; rc=1. |
| Non-root + real zygote → namespace handshake abort | `FATAL:zygote_host_impl_linux.cc:221 Check failed` rc=134. qqbox's `unshare(CLONE_NEWUSER\|CLONE_NEWPID\|CLONE_NEWNS)` is emulated as success-without-effect: `pid=40` not 1. |
| SUID path dead | qqbox `execve` ignores setuid: a 4755 root helper run as `nobody` stays uid 65534. With both layers off: `No usable sandbox!`. |
| seccomp-bpf filter kills the stub | `prctl(PR_SET_SECCOMP, MODE_FILTER)` and `seccomp(SET_MODE_FILTER)` → `laboon: stub died: signal: killed`. |
| No `/dev/dri` | `ls: cannot access '/dev/dri'`. SwiftShader software GL works instead. |

These are supervisor properties, not guest-userspace gaps. Enabling the real
sandbox requires a supervisor change: a real Linux kernel (VM via
Virtualization.framework/HVF) so namespaces/setuid/nested seccomp are real; or
three separate supervisor changes: real PID namespaces, honour `S_ISUID` on
emulated `execve`, and virtualize nested seccomp filters so the guest's filter
install does not kill the stub. Until then `--no-sandbox --single-process
--no-zygote --js-flags=--jitless` is the only working configuration; site
isolation and V8 JIT are off. Headless `--headless=new --dump-dom` times out;
windowed rendering under Xvfb is the supported path.

## Live dock survives runtime crashes (event-driven supervision)

The dock, navbar and every window disappeared from a running 5905 desktop after
~15h uptime while the wallpaper root pixmap survived. Forensics:

| evidence | value |
| --- | --- |
| RFB before fix | bare wallpaper, 1280x800, 17,916 colors, `ServerInit qbox:99` unchanged |
| `x11vnc.log` | one `screen setup finished` at 06:18:58, no X restart |
| `xfce.log` | Thunar aborted; repeated `Wnck:ERROR:../libwnck/xutils.c:1510:default_icon_at_size: assertion failed (base)` + `Bail out!` |
| `xfce.log` | `xfce4-terminal`, `xfsettingsd`, `xfwm4`, `xfce4-panel` all `ICE I/O Error` / `Disconnected from session manager` at 21:10:23 |

`xfce4-session` owned the session bus, so its death killed every client and
unmapped every window. X server restart and VNC reconnect were ruled out (same
`ServerInit` name, no second screen setup, normal `client_count`). There was no
restart mechanism.

Fix: **remove `xfce4-session` entirely**. Own one persistent session bus
(`dbus-daemon --session --fork`) plus `xfconfd`, and supervise `xfwm4`,
`xfce4-panel`, `xfdesktop`, the terminal and Chromium with an event-driven
`wait $pid` loop per component. `wait` blocks on child exit: no sleep, no
timeout, no periodic recheck (gm standing rule). A circuit breaker stops a
component that exits immediately six times. Chromium restarts with its
singleton locks cleared. This keeps exactly one panel and one xfconfd, so the
earlier duplicate-manager regressions do not return.

Witnessed: deliberate `pkill -x xfce4-panel` rc=0 → `panel exited rc=0 →
restarting` and the window back at `1280x41+0+0 +0+759`. After cutover an
independent RFB 3.8 frame on live 5905: `qbox:99`, 1280x800, 4,096,000 raw
bytes, 7,484 colors, 1,182 bottom-band colors, showing the Applications menu,
three launchers, a tasklist with `Example Domain - Chromium` and
`QQBOX-TERMINAL`. Fresh boot logs contain no ICE error, Wnck assertion or
duplicate-manager message. Chromium is launched with `--password-store=basic`
and the gnome-keyring autostart entries hidden, removing the "Choose password
for new keyring" modal.

## Clean session and child PTY (no random XFCE errors)

The first 26.04 boots emitted a stream of errors from duplicate processes and
startup scripts. All are fixed and witnessed in a fresh live boot:

| error | root cause | fix |
| --- | --- | --- |
| `x27: command not found` in EVERY interactive child PTY | the Dockerfile wrote `PS1=\x27…\x27` with `printf`; dash does not interpret `\x`, so the literal `\x27` landed in `/root/.bashrc` | Dockerfile uses octal `\047`; startup script removes any stale `\x27` lines in place |
| `bash: initialize job control: no job control in background: Bad file descriptor` | bash's job-control init calls `tcsetpgrp` on a PTY qqbox cannot fully emulate | terminal child wrapper exports `SHELLOPTS=` (monitor off); witnessed clean `root@qbox:/out#` with typed input echoed |
| `bash: cannot set terminal process group (1): Inappropriate ioctl for device` | the evidence script ran `bash -i -c` with no PTY | replaced with a plain `grep -F 'PS1='` check; the real terminal is witnessed by typed RFB input |
| `Another Window Manager (Xfwm4) is already running` / `Could not find a screen to manage` / `Another desktop manager is already running` | the startup script started `xfwm4`, `xfce4-panel`, `xfdesktop` **and** `xfce4-session`, which autostarts its own copies (two panels, two xfconfd) | `xfce4-session` owns exactly one of each; manual duplicates removed |
| `Failed to run gnome-keyring-daemon: Failed to execute child process` (No such file) | `xfce4-session` compat `LaunchGNOME=true` | `xfce4-session.xml` sets `compat/LaunchGNOME=false`; `gnome-keyring` also installed |
| `FileNotFoundError: 'xdg-user-dir'` Python traceback from `xdg-xubuntu-templates` | `xdg-user-dirs` absent | installed |
| `xfdesktop_regular_file_icon_new: assertion 'G_IS_FILE_INFO' failed`, `thunarx_menu_provider…` GLib CRITICAL | desktop-icon plugins on the vfs-only root | `desktop-icons/style=0` (wallpaper still painted by `xwallpaper`) |
| `Failed to connect to session manager: SESSION_MANAGER environment variable not defined` | terminal launched outside the session | terminal launched from `autostart/qqbox-terminal.desktop`, owned by `xfce4-session` |
| `/usr/bin/chromium: ... Segmentation fault` when clicked from the dock | the stock `chromium.desktop` → `wrapped-chromium` → Ubuntu snap stub | `.desktop` Exec overridden to `/usr/local/bin/chromium` and `wrapped-chromium` symlinked to the Debian wrapper |

Remaining log lines are informational, not errors: one
`libxfce4util-WARNING: Failed to get a systemd proxy` (qqbox has no systemd
`login1`), `_NET_*` "assuming" messages before xfwm4 publishes properties,
and the expected `org.freedesktop.PackageKit ServiceUnknown` after the
DBus-trigger removal. A fresh boot's panel/terminal/wm/xfdesktop logs contain
no ERROR, CRITICAL, assertion, child-exec, PTY or Traceback lines.

## The dock / taskbar (and why it disappeared on 26.04)

On the first Xubuntu 26 build the bottom dock was missing entirely. Live
`panel.log` showed the cause:

```
(xfce4-panel): Gtk-WARNING: Invalid icon size 16
Gtk:ERROR:gtkiconhelper.c:495:ensure_surface_for_gicon: assertion failed
(error == NULL): Failed to load
/usr/share/icons/Adwaita/scalable/status/image-missing.svg:
Loader process exited early with status '1'
Command: "bwrap" ... "/usr/libexec/glycin-loaders/2+/glycin-svg"
```

Ubuntu 26.04's gdk-pixbuf 2.44 routes scalable icons through **glycin**, whose
loaders are spawned by bubblewrap. Under qqbox, `bwrap --unshare-all ...`
fails verbatim with `bwrap: Failed to make / slave: Bad address`. xfce4-panel
aborts on the GTK assertion, so there is no Applications menu, no launchers and
no tasklist. `xfdesktop` dies on the same loader path. Installing
`librsvg2-common` alone does NOT fix it: the native `libpixbufloader_svg.so`
appears in `loaders.cache`, but GTK still prefers the glycin registrations in
`/usr/share/glycin-loaders/2+/conf.d/`; removing those registrations produces
`No image loaders are configured`. A `GDK_PIXBUF_MODULE_FILE` override alone
also fails (witnessed).

Fix, baked into the image: replace `/usr/bin/bwrap` with a narrow compatibility
shim that execs the **real** glycin loader with its own argv when it sees a
`/usr/libexec/glycin-loaders/...` argument, and delegates every other
invocation to the preserved `/usr/bin/bwrap.qqbox-orig`. Only the mount sandbox
that qqbox cannot provide is dropped; qqbox's `unshare()` is already
success-without-effect, so bubblewrap could never provide real isolation here.
The guest remains confined by qqbox itself. This is disclosed rather than
hidden.

Witnessed with the shim: `xfce4-panel 1280x41+0+0 +0+759`, two live panel
processes, 2,623 distinct colors in the bottom 50px band, and a screenshot
showing the Applications menu, three launcher icons and a tasklist with both
`Example Domain - Chromium` and `QQBOX-TERMINAL`.

## Wallpapers (how it works)

The stock Xubuntu 26 image ships backdrops in `/usr/share/xfce4/backdrops/`
(`xubuntu-resolute-plucky-remix.png`, `xubuntu-questing.png`, …) with
`xubuntu-wallpaper.png` as the current-release symlink. Two 26.04-specific
blockers were witnessed live:

1. **Xubuntu 26.04's own `/etc/xdg/xdg-xubuntu/.../xfce4-desktop.xml` has zero
   backdrop keys**, and XFCE 4.20 uses
   `/backdrop/screen0/monitorscreen/workspaceN/last-image`, not the 24.04
   `monitor0/image-path` keys.
2. **Ubuntu 26.04's gdk-pixbuf loaders are glycin-based and require bubblewrap**:
   `bwrap --ro-bind / / --dev /dev sh -c …` fails verbatim with
   `bwrap: Failed to make / slave: Bad address` under qqbox. xfdesktop therefore
   cannot decode the PNG and paints a solid root (witnessed as one distinct
   color on `live-5926/01-wallpaper.png`). `feh` sets `_XROOTPMAP_ID` but
   xfdesktop repaints over it.

The fix that works: set `XDG_CONFIG_DIRS=/etc/xdg/xdg-xubuntu:/etc/xdg`, write
`$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml/xfce4-desktop.xml` with
`monitorscreen/workspace0/last-image`, `image-style=5`, `color-style=0` and
`image-show=false`; start `xfce4-session` under `dbus-run-session` (session bus
for `xfconfd`), `xfwm4 --compositor=off`, `xfce4-panel`, then
`xfdesktop --disable-wm-check` (without that flag xfdesktop exits with `No
window manager registered on screen 0`). Finally **paint the root pixmap with
`xwallpaper --zoom "$WALL"`**, which uses libpng directly and bypasses glycin.

Live 26.04 witness: `xwallpaper_rc=0`, 1280x800 PNG with **17,855 distinct
colors** showing the real Xubuntu sunset-lake artwork
(`wall-26b/out/01-after.png`). Control `00-before.png` has 1 distinct color.
The 24.04 noble image rendered its wallpaper through xfdesktop alone (9,049
colors); that path does not transfer to 26.04 — an adversarial verifier caught
this and it is recorded here rather than hidden.

## Audio (honest boundary)

`pulseaudio` with `module-null-sink` gives a real, working audio server in the
guest. `PULSE_SERVER` must **not** be exported to the daemon (it refuses to
autostart when a user-configured server is set); clients get it. There is no
host audio device and no `/dev/snd`, so nothing reaches the host speakers.

## Files

| file | purpose |
| --- | --- |
| `Dockerfile` | pinned 26.04 base, dpkg/ldconfig + DBus fixes, GNU tools, Chromium, Claude Code, skill, extensions |
| `start-xubuntu.sh` | root bus, wallpaper, bottom panel, audio, `root@qbox`, VNC, Chromium, evidence capture |
| `chromium-wrapper.sh` | Chromium launch flags + SwiftShader + PULSE_SERVER, bundled bookworm libs |
| `fetch-chromium.sh` | reproducible Chromium payload assembly |
| `fetch-extras.sh` | copies real Tencent BrowserSkill artifacts + fetches Claude Code 1.0.128 |
| `chromium/` | assembled payload (not committed: ~339 MiB binary, four bookworm SONAMEs) |
| `extension/` | real MV3 qqbox witness extension |
| `skills/tencent-browser/SKILL.md` | honest skill pointer |
| `claude/claude-code.tgz` | Claude Code 1.0.128 npm tarball (~34 MiB) |
