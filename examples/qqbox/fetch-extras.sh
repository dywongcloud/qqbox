#!/bin/sh
# Assemble the third-party artifacts the qqbox desktop preloads:
#   * the real Tencent BrowserSkill (MIT) MV3 extension from the host Chrome
#     profile + the matching browser-skill SKILL.md (or Claude Code skill dir).
#   * the pure-JS Claude Code 1.0.128 npm tarball (Node 18; the Bun-based
#     2.1.x native build aborts under laboon's futex emulation.
#
# These are NOT committed. Run this before `qqbox build`.
#
# Honest boundary: bsk 0.5.0 is a macOS Mach-O binary and cannot run in a
# Linux ELF guest; the extension and skill documentation are installed, but
# browser automation via bsk is not available in the guest. Chromium requires
# --single-process --no-zygote under laboon, which prevents the extension's
# service worker from running (multi-process mode loads it). See README.md.
set -eu
cd "$(dirname "$0")"
mkdir -p tencent-browserskill browser-skill/references claude extension skills/tencent-browser
EXT="$HOME/Library/Application Support/Google/Chrome/Default/Extensions/hhcmgoofomhgciiibhipgmgkgnoenaoi/0.5.0_0"
SKILL="$HOME/.tclaude/skills/browser-skill"
if [ -d "$EXT" ]; then
  cp -R "$EXT/." tencent-browserskill/
  echo "TENCENT_BROWSERSKILL_EXTENSION_READY from $EXT"
else
  echo "MISSING: $EXT (install BrowserSkill in host Chrome first)" >&2
  exit 1
fi
if [ -f "$SKILL/SKILL.md" ]; then
  cp "$SKILL/SKILL.md" browser-skill/SKILL.md
  cp -R "$SKILL/references/." browser-skill/references/ 2>/dev/null || true
  echo "BROWSER_SKILL_DOCS_READY"
fi
curl -fsSL -o claude/claude-code.tgz \
  https://registry.npmjs.org/@anthropic-ai/claude-code/-/claude-code-1.0.128.tgz
echo "CLAUDE_CODE_TARBALL_READY 1.0.128"
chmod -R a+rX tencent-browserskill browser-skill claude extension skills
echo FETCH_EXTRAS_DONE
