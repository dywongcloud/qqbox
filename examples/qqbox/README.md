# Fully provisioned Tencent qqbox Xubuntu desktop

This is the reproducible successor to `examples/xubuntu/`. It boots the stock
`linuxserver/webtop` ubuntu-xfce arm64 image under the native qqbox guest
runtime and serves it over VNC TCP 5905 with:

| requirement | status |
| --- | --- |
| bottom dock/taskbar | XFCE panel `p=10;x=0;y=0` → `1280x41+0+759` on an 800px screen, with applications menu, three launchers and a tasklist. |
| real Chromium | Debian bookworm Chromium 150.0.7871.100 `linux/arm64` (not NetSurf, not the Ubuntu snap stub). Renders `https://example.com/` over VNC. |
| Chromium sandbox | **NOT ACHIEVABLE** — 27 sandbox-enabled variants tried, all rc=1/134. See below. |
| GPU | No `/dev/dri`; SwiftShader WebGL 1.0 works (`gl.clear()`/`readPixels()` round trip, `glError=0`). |
| audio | PulseAudio null sink: `pactl info` rc=0, `pactl list short sinks` lists `qqbox`, `paplay` rc=0. No `/dev/snd`/host playback. |
| prompt | `root@qbox` via `hostname qbox` + `PS1='\u@qbox:\w\$ '`. |
| ping / DNS / HTTPS | `ping -c 3 google.com` 0% loss, `getent hosts` rc=0, `curl -4 https://example.com/` HTTP 200. IPv6 AAAA records are not reachable. |
| apt / apk | `apt-get install` works in this Ubuntu image; `apk` was witnessed in a separate Alpine guest. |
| toolchain | `python3 3.12.3`, `pip3 24.0`, `node v18.19.1`, `npm 9.2.0`, `git 2.43.0`, `gh 2.45.0`, `claude 1.0.128` (pure-JS build). |
| browser extension | Real Tencent BrowserSkill 0.5.0 MV3 extension preloaded from `/opt/tencent-browserskill` plus a local qqbox witness extension at `/opt/qqbox-extension`. With the required `--single-process --no-zygote` flags Chromium logs `Cannot use V8 Proxy resolver in single process mode`, so the service worker does not run; multi-process mode loads the extension (CDP service-worker target witnessed). |
| Tencent browser skill | Resolves to the public `Tencent/BrowserSkill` (MIT): the host has `~/.tclaude/skills/browser-skill/SKILL.md` and `bsk` 0.5.0 (a macOS Mach-O binary). The skill docs and the real MV3 extension are installed into the guest; `bsk` itself cannot run in a Linux guest. A `tencent-browser/SKILL.md` pointer documents the boundary. |

## Build

```sh
sh fetch-chromium.sh     # downloads + extracts the pinned Debian Chromium debs
sh fetch-extras.sh       # copies the real Tencent BrowserSkill extension/skill,
                         # downloads the pure-JS Claude Code 1.0.128 npm tarball
qqbox build -net -t qqbox-xubuntu:complete -f Dockerfile -o qqbox-complete.tar .
```

Neither `chromium/` (~338 MiB) nor `claude/claude-code.tgz` (~34 MiB) is
committed; `fetch-extras.sh` also refuses to invent the Tencent BrowserSkill
artifacts if the host has not installed them.

**Extension caveat:** Chromium's `--single-process --no-zygote` (needed under
qqbox) conflicts with MV3 extensions: loading the real BrowserSkill extension
in that mode segfaults (rc=139, `Cannot use V8 Proxy resolver in single process
mode`). Multi-process mode loads the extension (rc=0, `Extensions.LoadAll2` and
`Extensions.LoadingFromCommandLine` histograms = 1) but the multi-process
zygote path hangs for windowed rendering. The wrapper preloads both extensions
so `--load-extension` is present by default; the honest status is that the
extension's service worker cannot run in the only windowed configuration that
works under qqbox. Fixing this requires the supervisor changes above.

`-net` is required for the `RUN apt-get` steps. The Dockerfile pins `FROM` to
`docker.io/linuxserver/webtop@sha256:5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55`.
It neutralizes a crashing `ldconfig.real` dpkg trigger and repairs `libc-bin`
status so later `apt-get install` keeps working. Chrome-for-Testing 158 was
tried and segfaults instantly under qqbox (rc=139 in every flag combination),
so Debian Chromium 150 is used instead.

## Run

```sh
mkdir -p /tmp/qqbox-out
qqbox -image qqbox-xubuntu:complete -net -e VNC_PORT=5905 \
  -mount /tmp/qqbox-out:/out
/opt/TurboVNC/bin/vncviewer 127.0.0.1:5905
```

The startup script writes `requirements.txt`, `windows.txt`,
`qqbox-desktop.png`, `audio.txt`, `hostname.txt`, `pulseaudio.log`,
`pulseaudio-rc.txt`, `chromium.log` and `result.txt` into `/out`.

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

## Audio (honest boundary)

`pulseaudio` with `module-null-sink` gives a real, working audio server in the
guest. `PULSE_SERVER` must **not** be exported to the daemon (it refuses to
autostart when a user-configured server is set); clients get it. There is no
host audio device and no `/dev/snd`, so nothing reaches the host speakers.
`aplay -D null` rc=0. `modprobe snd-dummy` fails (no kernel modules).

## Files

| file | purpose |
| --- | --- |
| `Dockerfile` | pinned base + toolchain + Chromium + Claude Code + skill + extension |
| `start-xubuntu.sh` | bottom panel, audio, `root@qbox`, VNC, Chromium, evidence capture |
| `chromium-wrapper.sh` | Chromium launch flags + SwiftShader + PULSE_SERVER |
| `fetch-chromium.sh` | reproducible Chromium payload assembly |
| `fetch-extras.sh` | copies real Tencent BrowserSkill artifacts + fetches Claude Code 1.0.128 |
| `chromium/` | assembled payload (not committed: ~338 MiB binary) |
| `extension/` | real MV3 extension |
| `skills/tencent-browser/SKILL.md` | honest skill substitute |
| `claude/claude-code.tgz` | Claude Code 1.0.128 npm tarball (~34 MiB) |
