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
| wallpaper / backgrounds | Real Xubuntu backdrop renders: `xfce4-desktop.xml` `image-path` → `/usr/share/xfce4/backdrops/xubuntu-wallpaper.png` (→ `xubuntu-resolute-plucky-remix.png`), `xfconfd` + `xfwm4` + `xfdesktop --disable-wm-check`. Live screenshot: 9,049 distinct colors and desktop icons, not the old solid `#204a87`. |
| bottom dock/taskbar | XFCE panel `p=10;x=0;y=0` → `1280x41+0+759` on an 800px screen, applications menu, three launchers, tasklist. |
| vi / vim | `vim 9.1`, `vim-tiny` (the `vi` binary). Both witnessed editing a file with rc=0 and verified contents. |
| GNU userland | ~70 common packages: coreutils, findutils, grep, gawk, sed, diffutils, patch, make/gcc/g++/binutils/build-essential, autoconf/automake/libtool/pkg-config/cmake/ninja/gdb, tar/gzip/bzip2/xz/zip/unzip/cpio, procps/psmisc/util-linux, net-tools/iproute2/dnsutils/traceroute, wget/curl/rsync/openssh-client, less/man-db/nano/tree/file/which, jq/htop/bc/time/strace/lsof/tmux/screen, gnupg, locales/gettext, socat/netcat/telnet/xxd, pciutils/usbutils/parted/e2fsprogs, perl, fastfetch. |
| apt / package registries | Works repeatedly after the DBus fix; `apt-get install neofetch`-class failures are gone. |
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
qqbox build -net -t qqbox-xubuntu:26 -f Dockerfile -o qqbox-xubuntu26.tar .
```

Neither `chromium/` (~339 MiB) nor `claude/claude-code.tgz` (~34 MiB) is
committed. `fetch-extras.sh` refuses to invent the Tencent BrowserSkill artifacts
if the host has not installed them.

`-net` is required for the `RUN apt-get` steps. Chrome-for-Testing 158 was tried
and segfaults instantly under qqbox (rc=139 in every flag combination), so
Debian Chromium 150 is used instead.

## Run

```sh
mkdir -p /tmp/qqbox-out
qqbox -image qqbox-xubuntu:26 -net -e VNC_PORT=5905 \
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

Fix: start the system bus as **root** with `/etc/dbus-1/qqbox-system.conf`
(`<user>root</user>`, explicit `<listen>`, standard system service dirs). With
root as the bus owner, activations do not need the setuid helper. Witnessed:
`dbus_ping_rc=0`, `NO_DBUS_ERROR`, and `apt-get install` rc=0. A real fix would
be supervisor-side: honour `S_ISUID` on emulated `execve`.

**Two related 26.04 dpkg fixes.** (1) The static-pie `/usr/sbin/ldconfig`
segfaults under qqbox's rewriter, so `libc-bin`'s postinst exits 139 and leaves
dpkg half-configured. Ubuntu 26.04 is usrmerged, so `libc-bin` owns
`/usr/sbin/ldconfig` — a plain overwrite is restored at unpack. The durable fix
is `dpkg-divert --local --rename --divert /usr/sbin/ldconfig.qqbox-real
/usr/sbin/ldconfig` plus a no-op script. (2) `apt-get` can have its HTTP method
killed (`Method http has died unexpectedly! … signal 9`) under host CPU/memory
contention; the build uses `Acquire::Retries=2` and
`Acquire::http::Timeout=25`.

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

## Wallpapers (how it works)

The stock Xubuntu 26 image ships backdrops in `/usr/share/xfce4/backdrops/`
(`xubuntu-resolute-plucky-remix.png`, `xubuntu-questing.png`, …) and points
`xfce4-desktop.xml` at `xubuntu-wallpaper.png`. The previous boot painted a
solid `xsetroot -solid '#204a87'` because `xfdesktop` was never started with a
working `xfconfd`/WM. The fix: write `$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml/xfce4-desktop.xml`
with `image-path`, `image-style=5`, `image-show=true`; set
`XDG_CONFIG_DIRS=/etc/xdg/xdg-xubuntu:/etc/xdg`; start `xfce4-session` under
`dbus-run-session` (session bus for `xfconfd`), then `xfwm4 --compositor=off`
and `xfdesktop --disable-wm-check`. Without `--disable-wm-check`, xfdesktop
exits with `No window manager registered on screen 0`. Live witness: 1280x800
PNG with 9,049 distinct colors and a Home desktop icon.

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
