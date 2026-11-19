#!/bin/sh
# Assemble the Chromium payload for examples/qqbox/Dockerfile.
# Real Debian bookworm chromium 150.0.7871.100 linux/arm64 (glibc), not a snap
# stub, not NetSurf, and not Chrome-for-Testing (which segfaults under qqbox).
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
  pool/main/libj/libjpeg-turbo/libjpeg62-turbo_2.1.5-2_arm64.deb; do
  fetch "$(basename "$u")" "$LIBS/$u"
done
# Extract only safe relative members; packages are pinned by exact filename.
python3 - "$TMP" "$OUT" <<'PY'
import sys, os, io, tarfile
tmp, out = sys.argv[1], sys.argv[2]
for name in os.listdir(tmp):
    raw = open(os.path.join(tmp, name), 'rb').read()
    pos = 8
    while pos + 60 <= len(raw):
        h = raw[pos:pos+60]
        if h[58:60] != b'`\n':
            break
        n = h[0:16].decode().strip()
        sz = int(h[48:58].decode().strip())
        data = raw[pos+60:pos+60+sz]
        pos += 60 + sz + (sz % 2)
        if n.startswith('data.tar'):
            t = tarfile.open(fileobj=io.BytesIO(data), mode='r:xz')
            for m in t.getmembers():
                if m.name.startswith('/') or '..' in m.name.split('/'):
                    continue
                if m.isfile() and (m.name.startswith('./usr/lib/chromium/') or m.name.endswith('.so.62') or m.name.endswith('.so.6')):
                    dst = os.path.join(out, os.path.basename(m.name))
                    open(dst, 'wb').write(t.extractfile(m).read())
                    os.chmod(dst, 0o755)
                    print('wrote', dst)
print('CHROMIUM_PAYLOAD_READY; add chromium-wrapper.sh to the payload dir before building.
PY
