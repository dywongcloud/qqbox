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
# Ubuntu 26.04 only ships Node 22 (and nodejs.org tarballs), both of which
# abort under qqbox's futex emulation with rc=134. Ship the Ubuntu 24.04 noble
# Node 18 binary with its six SONAME libs, five externalized builtins, and
# npm 9.2.0 from the npm registry.
mkdir -p node18/usr/bin node18/usr/lib/aarch64-linux-gnu node18/usr/share/nodejs npm
TMPX=$(mktemp -d); trap 'rm -rf "$TMPX"' EXIT
NODEJS_BASE=http://ports.ubuntu.com/ubuntu-ports
for u in \
  pool/universe/n/nodejs/nodejs_18.19.1+dfsg-6ubuntu5_arm64.deb \
  pool/universe/n/nodejs/libnode109_18.19.1+dfsg-6ubuntu5_arm64.deb \
  pool/main/c/c-ares/libcares2_1.27.0-1.0ubuntu1_arm64.deb \
  pool/main/i/icu/libicu74_74.2-1ubuntu3_arm64.deb \
  pool/universe/n/node-undici/node-undici_5.26.3+dfsg1+~cs23.10.12-2_all.deb \
  pool/universe/a/acorn/node-acorn_8.8.1+ds+~cs25.17.7-2_all.deb \
  pool/universe/n/node-cjs-module-lexer/node-cjs-module-lexer_1.2.3+dfsg-1_all.deb; do
  curl -fsSL -o "$TMPX/$(basename "$u")" "$NODEJS_BASE/$u"
done
curl -fsSL -o "$TMPX/npm-9.2.0.tgz" 'https://registry.npmjs.org/npm/-/npm-9.2.0.tgz'
python3 - "$TMPX" <<'PYX'
import sys,os,io,tarfile,glob
tmp=sys.argv[1]; root=os.getcwd()
def ar(p):
    d=open(p,'rb').read(); o=8
    while o+60<=len(d):
        h=d[o:o+60]; n=h[:16].decode().strip(); sz=int(h[48:58].decode().strip()); b=d[o+60:o+60+sz]
        if n.rstrip('/').startswith('data.tar'):
            return tarfile.open(fileobj=io.BytesIO(b))
        o+=60+sz+(sz%2)
    return None
for p in glob.glob(tmp+'/*.deb'):
    t=ar(p)
    if not t: continue
    for m in t.getmembers():
        name=m.name.lstrip('./')
        if not m.isfile() or '..' in name.split('/'): continue
        if name.startswith('usr/bin/node') or name.startswith('usr/lib/aarch64-linux-gnu/lib') or name.startswith('usr/share/nodejs/'):
            dst=os.path.join(root,'node18',name)
            os.makedirs(os.path.dirname(dst),exist_ok=True)
            open(dst,'wb').write(t.extractfile(m).read())
            if name.startswith('usr/bin/'): os.chmod(dst,0o755)
links={'libcares.so.2':'libcares.so.2.12.0','libicudata.so.74':'libicudata.so.74.2','libicui18n.so.74':'libicui18n.so.74.2','libicuio.so.74':'libicuio.so.74.2','libicutest.so.74':'libicutest.so.74.2','libicutu.so.74':'libicutu.so.74.2','libicuuc.so.74':'libicuuc.so.74.2'}
D=os.path.join(root,'node18/usr/lib/aarch64-linux-gnu')
for link,target in links.items():
    if os.path.exists(os.path.join(D,target)) and not os.path.lexists(os.path.join(D,link)):
        os.symlink(target,os.path.join(D,link))
t=tarfile.open(tmp+'/npm-9.2.0.tgz')
for m in t.getmembers():
    if not m.isfile() or '..' in m.name.split('/') or m.name.startswith('/'): continue
    name=m.name.split('/',1)[1] if m.name.startswith('package/') else m.name
    dst=os.path.join(root,'npm',name); os.makedirs(os.path.dirname(dst),exist_ok=True)
    open(dst,'wb').write(t.extractfile(m).read())
print('NODE18_NPM_PAYLOAD_READY')
PYX
chmod -R a+rX tencent-browserskill browser-skill claude extension skills
echo FETCH_EXTRAS_DONE
