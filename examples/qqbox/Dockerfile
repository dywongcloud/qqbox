# Tencent qqbox fully provisioned Xubuntu 26 desktop.
# Base: linuxserver/webtop ubuntu-xfce ls292, Ubuntu 26.04.1 LTS "Resolute
# Raccoon" with Xubuntu 26.04.1 default settings and wallpapers, linux/arm64.
FROM docker.io/linuxserver/webtop@sha256:4721b798561ebabc9bfee60d3220f8c5d57b17fb78ab83168c94628e07e55e99

# qqbox's execve ignores setuid and the static-pie /usr/sbin/ldconfig
# segfaults under the rewriter. Ubuntu 26.04 is usrmerged: libc-bin owns
# /usr/sbin/ldconfig (not /sbin/ldconfig), so a plain overwrite is restored by
# unpack. A dpkg divert on the packaged path is the only durable fix.
RUN dpkg-divert --local --rename --divert /usr/sbin/ldconfig.qqbox-real /usr/sbin/ldconfig && \
    printf '#!/bin/sh\nexit 0\n' > /usr/sbin/ldconfig && chmod 755 /usr/sbin/ldconfig && \
    python3 - <<'PY'
import re
p='/var/lib/dpkg/status'
blocks=open(p).read().split('\n\n')
for i,b in enumerate(blocks):
    if re.search(r'^Package: libc-bin$',b,re.M) and 'Status: install ok half-configured' in b:
        blocks[i]=re.sub(r'^Config-Version: .*\n','',re.sub(r'^Status: .*$','Status: install ok installed',b,flags=re.M),flags=re.M)
open(p,'w').write('\n\n'.join(blocks))
PY

# GDBus.Error:org.freedesktop.DBus.Error.Spawn.PermissionsInvalid:
# "The permission of the setuid helper is not correct".
# Root cause: dbus-daemon --system drops to the messagebus user and activates
# system services via the setuid-root dbus-daemon-launch-helper. qqbox's execve
# ignores S_ISUID, so the helper cannot gain root and its stat()-based check
# fails. A root-user system bus serves the same activations without the helper.
# This is a guest-userspace workaround; the real fix is supervisor-side setuid.
RUN cat > /etc/dbus-1/qqbox-system.conf <<'CONF'
<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>system</type>
  <user>root</user>
  <listen>unix:path=/run/dbus/system_bus_socket</listen>
  <policy context="default">
    <allow user="*"/>
    <allow own="*"/>
    <allow send_type="method_call"/>
    <allow send_type="signal"/>
    <allow send_type="method_return"/>
    <allow send_type="error"/>
    <allow receive_type="method_call"/>
    <allow receive_type="signal"/>
    <allow receive_type="method_return"/>
    <allow receive_type="error"/>
  </policy>
  <standard_system_servicedirs/>
  <servicedir>/usr/share/dbus-1/system-services</servicedir>
  <includedir>/etc/dbus-1/system.d</includedir>
</busconfig>
CONF

# 20packagekit's gdbus hook and the PackageKit system-service file both ask
# the bus to activate a User=root service, which needs the unusable setuid
# helper. Removing them makes apt installs reliable even if another client
# reaches the system bus.
RUN rm -f /etc/apt/apt.conf.d/20packagekit \
      /usr/share/dbus-1/system-services/org.freedesktop.PackageKit.service

RUN export DEBIAN_FRONTEND=noninteractive; \
    apt-get update -o Acquire::Retries=2 -o Acquire::http::Timeout=25 && \
    apt-get install -y --no-install-recommends \
      x11vnc xvfb scrot xdotool iputils-ping curl ca-certificates \
      xfce4-panel xfdesktop4 xfwm4 thunar xfce4-terminal xwallpaper \
      pulseaudio pulseaudio-utils \
      python3 python3-pip python3-venv git gh dbus-x11 \
      vim vim-tiny nano less man-db coreutils findutils grep gawk sed \
      diffutils patch make gcc g++ binutils build-essential \
      autoconf automake libtool pkg-config cmake ninja-build gdb \
      file which tree net-tools iproute2 dnsutils traceroute wget rsync \
      openssh-client zip unzip xz-utils bzip2 gzip tar cpio \
      procps psmisc util-linux htop jq bc time strace lsof tmux screen \
      gnupg bash-completion command-not-found locales gettext \
      socat netcat-openbsd telnet xxd units pciutils usbutils parted \
      e2fsprogs dosfstools plocate perl fastfetch && \
    dpkg --audit && \
    rm -rf /var/lib/apt/lists/*

# Node 22 (the only apt candidate on 26.04) aborts under qqbox with
# "The futex facility returned an unexpected error code" (rc=134), and
# official nodejs.org tarballs do the same. The Ubuntu 24.04 noble Node 18
# works, so its binary, six SONAME libs, five externalized builtins, npm 9.2.0
# and Claude Code 1.0.128 (pure JS) are shipped as a self-contained payload.
COPY node18 /opt/qqbox-node
COPY npm /opt/qqbox-npm
RUN chmod 755 /opt/qqbox-node/usr/bin/node && \
    printf '#!/bin/sh\nexport LD_LIBRARY_PATH=/opt/qqbox-node/usr/lib/aarch64-linux-gnu\nexec /opt/qqbox-node/usr/bin/node "$@"\n' > /usr/local/bin/node && chmod 755 /usr/local/bin/node && \
    printf '#!/bin/sh\nexec /usr/local/bin/node /opt/qqbox-npm/bin/npm-cli.js "$@"\n' > /usr/local/bin/npm && chmod 755 /usr/local/bin/npm && \
    printf '#!/bin/sh\nexec /usr/local/bin/node /opt/qqbox-npm/bin/npx-cli.js "$@"\n' > /usr/local/bin/npx && chmod 755 /usr/local/bin/npx && \
    ln -sfn /opt/qqbox-node/usr/share/nodejs /usr/share/nodejs && \
    node -e 'console.log("node_ok",process.version)' > /etc/qqbox-node.txt && \
    npm --version >> /etc/qqbox-node.txt

COPY chromium /opt/chromium
RUN chmod +x /opt/chromium/chromium /opt/chromium/chromium-wrapper.sh && \
    ln -sf /opt/chromium/chromium-wrapper.sh /usr/local/bin/chromium && \
    mkdir -p /tmp/crash && chmod 1777 /tmp/crash && \
    printf 'qbox\n' > /etc/hostname && \
    printf '127.0.0.1 localhost qbox\n::1 localhost ip6-localhost ip6-loopback qbox\n' > /etc/hosts && \
    printf 'export HOSTNAME=qbox\nPS1=\x27\\u@qbox:\\w\\$ \x27\n' >> /root/.bashrc

COPY extension /opt/qqbox-extension
COPY tencent-browserskill /opt/tencent-browserskill
COPY browser-skill /root/.claude/skills/browser-skill
COPY skills/tencent-browser/SKILL.md /root/.claude/skills/tencent-browser/SKILL.md
COPY claude/claude-code.tgz /tmp/claude-code.tgz
RUN mkdir -p /opt/claude-code && tar -xzf /tmp/claude-code.tgz -C /opt/claude-code && rm -f /tmp/claude-code.tgz && \
    printf '#!/bin/sh\nexec /usr/local/bin/node /opt/claude-code/package/cli.js "$@"\n' > /usr/local/bin/claude && chmod 755 /usr/local/bin/claude && \
    { claude --version; node --version; npm --version; python3 --version; \
      pip3 --version; vim --version | head -1; vi --version | head -1; \
      git --version; gh --version | head -1; } > /etc/qqbox-toolchain.txt 2>&1 || true

COPY start-xubuntu.sh /usr/local/bin/start-xubuntu.sh
RUN chmod +x /usr/local/bin/start-xubuntu.sh

ENTRYPOINT []
CMD ["/usr/local/bin/start-xubuntu.sh"]
