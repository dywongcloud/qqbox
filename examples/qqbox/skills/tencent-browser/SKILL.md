# Tencent browser skill (qqbox desktop)

"Tencent browser skill" resolves to the public **Tencent/BrowserSkill**
(MIT). The host already has the real artifacts:

| artifact | host path |
| --- | --- |
| skill | `~/.tclaude/skills/browser-skill/SKILL.md` (+ `references/`) |
| CLI | `~/.local/bin/bsk` 0.5.0 (macOS Mach-O arm64; cannot run in a Linux guest) |
| extension | `~/Library/Application Support/Google/Chrome/Default/Extensions/hhcmgoofomhgciiibhipgmgkgnoenaoi/0.5.0_0/` |

In the qqbox guest the real skill docs are installed at
`/root/.claude/skills/browser-skill/` and the real MV3 extension at
`/opt/tencent-browserskill/`. Chromium preloads it by default with
`--load-extension=/opt/tencent-browserskill,/opt/qqbox-extension`.

A pointer skill also exists at `/root/.claude/skills/tencent-browser/SKILL.md`;

## Honest limitation

`bsk` is a macOS binary; the guest is Linux ELF under qqbox. The extension
and the skill *documentation* are present, but browser automation via `bsk`
is not available inside the guest. Use the host `bsk` against the host Chrome
profile, or drive the guest Chromium over CDP.

Also: qqbox's supervisor has no real PID namespaces, no setuid execve, and
kills guests installing a seccomp-bpf filter, so Chromium requires
`--no-sandbox --single-process --no-zygote --js-flags=--jitless`. In
single-process mode Chromium logs `Cannot use V8 Proxy resolver in single
process mode`; the extension's service worker does not run. Multi-process
mode loads the extension (CDP service-worker target witnessed). No skill in
guest userspace can change that; a supervisor change is required.

## Commands

```sh
chromium --load-extension=/opt/tencent-browserskill https://example.com/
claude --version    # Claude Code 1.0.128 (pure JS, Node 18)
node --version      # v18.19.1
python3 --version   # 3.12.3
```
