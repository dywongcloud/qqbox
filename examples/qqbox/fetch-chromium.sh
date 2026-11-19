#!/bin/sh
# Assemble the Chromium payload for examples/qqbox/Dockerfile.
# Real Debian bookworm chromium 150.0.7871.100 linux/arm64 (glibc), not a snap
# stub, not NetSurf, and not Chrome-for-Testing (which segfaults under qqbox).
#
# Four bookworm SONAMEs are bundled in extra-libs/ because Ubuntu 26.04
# (resolute) ships newer ones: libdav1d.so.6, libjpeg.so.62,
# libopenh264.so.7 and libFLAC.so.12. Verified with ldd: 0 missing.
set -eu
cd "$(dirname "$0")"
OUT=chromium
mkdir -p "$OUT/extra-libs"
BASE=https://deb.debian.org/debian/pool/main/c/chromium
LIBS=https://deb.debian.org/debian
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
fetch() { curl -fsSL -o "$TMP/$1" "$2"; }
fetch chromium.deb "$BASE/chromium_150.0.7871.100-1~deb12u1_arm64.deb"
fetch chromium-common.deb "$BASE/chromium-common_150.0.7871.100-1~deb12u1_arm64.deb"
for u in \
  pool/main/d/dav1d/libdav1d6_1.0.0-2+deb12u1_arm64.deb \
  pool/main/libj/libjpeg-turbo/libjpeg62-turbo_2.1.5-2_arm64.deb \
  pool/main/o/openh264/libopenh264-7_2.3.1+dfsg-3+deb12u2_arm64.deb \
  pool/main/f/flac/libflac12_1.4.2+ds-2_arm64.deb; do
  fetch "$(basename "$u")" "$LIBS/$u"
done
# Extract only safe relative members; packages are pinned by exact filename.
python3 - "$TMP" "$OUT" <<'PY'
import sys, os, io, tarfile
tmp, out = sys.argv[1], sys.argv[2]
extra = os.path.join(out, 'extra-libs')
os.makedirs(extra, exist_ok=True)
for name in sorted(os.listdir(tmp)):
    raw = open(os.path.join(tmp, name), 'rb').read()
    if raw[:8] != b'!<arch>\n':
        continue
    pos = 8
    while pos + 60 <= len(raw):
        h = raw[pos:pos+60]
        if h[58:60] != b'`\n':
            break
        n = h[0:16].decode().strip()
        sz = int(h[48:58].decode().strip())
        data = raw[pos+60:pos+60+sz]
        pos += 60 + sz + (sz % 2)
        if not n.startswith('data.tar'):
            continue
        t = tarfile.open(fileobj=io.BytesIO(data))
        for m in t.getmembers():
            if m.name.startswith('/') or '..' in m.name.split('/') or not m.isfile():
                continue
            base = os.path.basename(m.name)
            payload = t.extractfile(m).read()
            if m.name.startswith('./usr/lib/chromium/') or m.name.startswith('usr/lib/chromium/'):
                dst = os.path.join(out, base)
                open(dst, 'wb').write(payload)
                os.chmod(dst, 0o755)
                print('wrote', dst)
            elif '/usr/lib/' in m.name and ('.so.' in base or '.so' in base):
                dst = os.path.join(extra, base)
                open(dst, 'wb').write(payload)
                os.chmod(dst, 0o755)
                print('wrote', dst)
# SONAME links for the versioned real files.
for link, target in [
    ('libopenh264.so.7', 'libopenh264.so.2.3.1'),
    ('libFLAC.so.12', 'libFLAC.so.12.0.0'),
]:
    p = os.path.join(extra, link)
    if os.path.exists(os.path.join(extra, target)) and not os.path.lexists(p):
        os.symlink(target, p)
        print('linked', link, '->', target)
print('CHROMIUM_PAYLOAD_READY; add chromium-wrapper.sh to the payload dir before building.')
PY
sh -n "$0"
