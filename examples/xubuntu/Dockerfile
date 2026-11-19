# Stock Xubuntu/XFCE desktop for qqbox.
#
# The user's "Xubuntu" is linuxserver/webtop's ubuntu-xfce image, pinned to the
# exact linux/arm64 manifest they linked (Docker Hub layer page
# sha256-5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55),
# NOT plain ubuntu:24.04. That image is Ubuntu 24.04 + XFCE + the Xubuntu
# default settings and icon theme; it is not the Xubuntu distribution itself.
#
# qqbox has no init system, so the image's s6-overlay /init is replaced. The
# stock KasmVNC Xvnc serves HTTP/websocket on 3000 in normal use and its
# traditional -rfbport path sends no framebuffer updates under qqbox's
# synthetic X, so add x11vnc + Xvfb (the pair already proven in this repo) and
# keep everything else from the stock rootfs.
FROM docker.io/linuxserver/webtop@sha256:5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55

# apt's libc-bin/mandb trigger cannot write /var/cache/man in qqbox's
# unprivileged unpack, so let dpkg report that trigger failure but require the
# binaries themselves to exist. Without this the RUN step would falsely fail.
RUN export DEBIAN_FRONTEND=noninteractive; \
    apt-get update && \
    (apt-get install -y --no-install-recommends \
       x11vnc xvfb scrot xdotool netsurf-gtk || true) && \
    test -x /usr/bin/x11vnc && test -x /usr/bin/Xvfb && \
    test -x /usr/bin/netsurf-gtk && \
    rm -rf /var/lib/apt/lists/*

COPY start-xubuntu.sh /usr/local/bin/start-xubuntu.sh
RUN chmod +x /usr/local/bin/start-xubuntu.sh

# The image's ENTRYPOINT is /init (s6-overlay), which qqbox cannot supervise.
ENTRYPOINT []
CMD ["/usr/local/bin/start-xubuntu.sh"]
