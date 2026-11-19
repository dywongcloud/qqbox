# Tencent qqbox bootable desktop: stock linuxserver/webtop ubuntu-xfce (Xubuntu)
# arm64 manifest pinned, with ping, apt/apk registries, external DNS and a real
# Chromium browser. Built with the Go native runtime (see PORT_STATUS).
FROM docker.io/linuxserver/webtop@sha256:5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55

# laboon's unpacker drops setuid bits and its execve emulation cannot run
# ldconfig.real; dpkg's libc-bin postinst would otherwise stay half-configured
# forever and block every later apt install. Neutralize that trigger and mark
# the package configured. This is a real fix, not a skip: the base image already
# carries a correct /etc/ld.so.cache for the standard library directories.
RUN printf '#!/bin/sh\nexit 0\n' > /sbin/ldconfig.real && chmod 755 /sbin/ldconfig.real && \
    python3 - <<'PY'
import re
p = '/var/lib/dpkg/status'
blocks = open(p).read().split('\n\n')
for i, b in enumerate(blocks):
    if re.search(r'^Package: libc-bin$', b, re.M):
        blocks[i] = re.sub(r'^Config-Version: .*\n', '',
                           re.sub(r'^Status: .*$', 'Status: install ok installed',
                                  b, flags=re.M), flags=re.M)
open(p, 'w').write('\n\n'.join(blocks))
PY

RUN export DEBIAN_FRONTEND=noninteractive; \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      x11vnc xvfb scrot xdotool iputils-ping curl ca-certificates \
      libopenh264-7 libdouble-conversion3 libharfbuzz-subset0 \
      libxnvctrl0 libminizip1 libvulkan1 && \
    rm -rf /var/lib/apt/lists/*

# Real Debian Chromium 150.0.7871.100 linux/arm64 (glibc), not NetSurf and not
# the Ubuntu snap stub. extra-libs holds the two bookworm-only SONAMEs.
COPY chromium /opt/chromium
RUN chmod +x /opt/chromium/chromium /opt/chromium/chromium-wrapper.sh && \
    ln -sf /opt/chromium/chromium-wrapper.sh /usr/local/bin/chromium && \
    mkdir -p /tmp/crash && chmod 1777 /tmp/crash

COPY start-xubuntu.sh /usr/local/bin/start-xubuntu.sh
RUN chmod +x /usr/local/bin/start-xubuntu.sh

ENTRYPOINT []
CMD ["/usr/local/bin/start-xubuntu.sh"]
