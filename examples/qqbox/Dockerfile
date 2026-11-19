# Tencent qqbox fully provisioned Xubuntu/XFCE desktop.
# Base: stock linuxserver/webtop ubuntu-xfce linux/arm64 pinned by digest.
FROM docker.io/linuxserver/webtop@sha256:5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55

# laboon's unpacker drops setuid and its ldconfig.real crashes; repair dpkg so
# subsequent apt installs keep working.
RUN printf '#!/bin/sh\nexit 0\n' > /sbin/ldconfig.real && chmod 755 /sbin/ldconfig.real && \
    python3 - <<'PY'
import re
p='/var/lib/dpkg/status'
blocks=open(p).read().split('\n\n')
for i,b in enumerate(blocks):
    if re.search(r'^Package: libc-bin$',b,re.M):
        blocks[i]=re.sub(r'^Config-Version: .*\n','',re.sub(r'^Status: .*$','Status: install ok installed',b,flags=re.M),flags=re.M)
open(p,'w').write('\n\n'.join(blocks))
PY

RUN export DEBIAN_FRONTEND=noninteractive; \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      x11vnc xvfb scrot xdotool iputils-ping curl ca-certificates \
      libopenh264-7 libdouble-conversion3 libharfbuzz-subset0 \
      libxnvctrl0 libminizip1 libvulkan1 \
      xfce4-panel xfdesktop4 xfwm4 thunar \
      pulseaudio pulseaudio-utils \
      python3 python3-pip python3-venv \
      nodejs git gh && \
    rm -rf /var/lib/apt/lists/*

# apt npm pulls ~400 node-* packages and dpkg can fail on node-neo-async
# under laboon (rc=100). npm 9.2.0 is still installed and usable; make this
# step best-effort and verify the binary rather than trusting apt's rc.
RUN export DEBIAN_FRONTEND=noninteractive; \
    apt-get update -o Acquire::Retries=0 -o Acquire::http::Timeout=15 >/dev/null 2>&1 || true; \
    apt-get install -y --no-install-recommends npm >/tmp/npm-install.log 2>&1 || true; \
    dpkg --configure -a >/tmp/dpkg-configure.log 2>&1 || true; \
    npm --version >/etc/qqbox-npm-version.txt 2>&1 || true; \
    node --version >/etc/qqbox-node-version.txt 2>&1 || true; \
    rm -rf /var/lib/apt/lists/*

COPY chromium /opt/chromium
RUN chmod +x /opt/chromium/chromium /opt/chromium/chromium-wrapper.sh && \
    ln -sf /opt/chromium/chromium-wrapper.sh /usr/local/bin/chromium && \
    mkdir -p /tmp/crash && chmod 1777 /tmp/crash && \
    printf 'qbox\n' > /etc/hostname && \
    printf '127.0.0.1 localhost qbox\n::1 localhost ip6-localhost ip6-loopback qbox\n' > /etc/hosts && \
    printf 'export HOSTNAME=qbox\nPS1=\x27\\u@qbox:\\w\\$ \x27\n' >> /root/.bashrc

COPY extension /opt/qqbox-extension
# The real Tencent BrowserSkill (MIT, Tencent/BrowserSkill) MV3 extension and
# the matching browser-skill SKILL.md. bsk itself is a macOS Mach-O binary and
# cannot run in a Linux guest; the extension and skill docs are installed, and
# the extension is preloaded by default.
COPY tencent-browserskill /opt/tencent-browserskill
COPY browser-skill /root/.claude/skills/browser-skill
COPY skills/tencent-browser/SKILL.md /root/.claude/skills/tencent-browser/SKILL.md
COPY claude/claude-code.tgz /tmp/claude-code.tgz
RUN mkdir -p /opt/claude-code && tar -xzf /tmp/claude-code.tgz -C /opt/claude-code && rm -f /tmp/claude-code.tgz && \
    printf '#!/bin/sh\nexec /usr/bin/node /opt/claude-code/package/cli.js "$@"\n' > /usr/local/bin/claude && chmod 755 /usr/local/bin/claude && \
    claude --version > /etc/qqbox-toolchain.txt && node --version >> /etc/qqbox-toolchain.txt && \
    python3 --version >> /etc/qqbox-toolchain.txt && pip3 --version >> /etc/qqbox-toolchain.txt && \
    git --version >> /etc/qqbox-toolchain.txt && gh --version | head -1 >> /etc/qqbox-toolchain.txt

COPY start-xubuntu.sh /usr/local/bin/start-xubuntu.sh
RUN chmod +x /usr/local/bin/start-xubuntu.sh

ENTRYPOINT []
CMD ["/usr/local/bin/start-xubuntu.sh"]
