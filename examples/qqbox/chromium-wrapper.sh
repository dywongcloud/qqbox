#!/bin/sh
# Debian Chromium 150 linux/arm64, with the four bookworm-only SONAMEs bundled.
# SwiftShader software GL works under Xvfb; /dev/dri is not passed through.
#
# Chromium's own sandbox CANNOT be enabled under qqbox. 42 live variants
# (27 earlier + 15 re-tested) fail for two independent reasons:
#   * as root: "Running as root without --no-sandbox is not supported"
#     (zygote_host_impl_linux.cc:101, crbug.com/638180);
#   * as non-root with the sandbox on: FATAL CHECK at
#     zygote_host_impl_linux.cc:221 "Check failed: . : No such file or
#     directory (2)" -- the zygote dies before its hello message because
#     qqbox's execve ignores setuid, its PID-namespace emulation is a no-op
#     and installing a seccomp-bpf filter kills the stub.
# --disable-setuid-sandbox, user namespaces (unshare -U -r works, rc=0),
# --no-zygote and --single-process were all tried; none help.
#
# So --no-sandbox stays, but Chromium NO LONGER RUNS AS ROOT. It drops to the
# image's unprivileged user abc (uid 911 / gid 1001). That is the strongest
# isolation this substrate permits: a compromised renderer is no longer root.
# The host-level qqbox backstop (seccomp RET_TRAP / Seatbelt / LPAC) remains
# the outer boundary. --single-process --no-zygote are retained because
# multi-process headless Chromium hangs under qqbox (witnessed).
export LD_LIBRARY_PATH=/opt/chromium/extra-libs:/opt/chromium
export PULSE_SERVER=${PULSE_SERVER:-unix:/tmp/qbox-pa/native}

CHROME_HOME=/config/.config/qqbox-chromium
USER_DATA_DIR=/tmp/chrome-qqbox
for a in "$@"; do
  case "$a" in
    --user-data-dir=*) USER_DATA_DIR="${a#--user-data-dir=}" ;;
  esac
done

# Only root can prepare ownership; a second invocation as abc is a no-op.
if [ "$(id -u)" = 0 ]; then
  mkdir -p "$CHROME_HOME" "$USER_DATA_DIR" /tmp/crash /tmp/qbox-chrome-runtime 2>/dev/null || true
  chown -R 911:1001 "$CHROME_HOME" "$USER_DATA_DIR" /tmp/qbox-chrome-runtime 2>/dev/null || true
  chmod 700 /tmp/qbox-chrome-runtime 2>/dev/null || true
  chmod 1777 /tmp/crash 2>/dev/null || true
  chmod 777 /tmp/qbox-pa 2>/dev/null || true
  chmod 777 /tmp/qbox-pa/native 2>/dev/null || true
fi

CHROME_FLAGS="--no-sandbox --single-process --no-zygote --js-flags=--jitless
  --use-gl=angle --use-angle=swiftshader --enable-unsafe-swiftshader
  --disable-dev-shm-usage --disable-crash-reporter
  --disable-breakpad --crash-dumps-dir=/tmp/crash
  --load-extension=/opt/tencent-browserskill,/opt/qqbox-extension
  --disable-extensions-except=/opt/tencent-browserskill,/opt/qqbox-extension
  --no-first-run --no-default-browser-check --password-store=basic"

# PulseAudio null sink is the guest's audio backend; do not pass --mute-audio.
if [ "$(id -u)" = 0 ] && [ -x /bin/setpriv ] && id abc >/dev/null 2>&1; then
  # Durable witness: the same privilege drop actually yields uid 911.
  printf 'chromium_uid=%s chromium_gid=%s\n' \
    "$(setpriv --reuid=911 --regid=1001 --clear-groups id -u)" \
    "$(setpriv --reuid=911 --regid=1001 --clear-groups id -g)" \
    > /out/chromium-uid.txt 2>/dev/null || true
  exec setpriv --reuid=911 --regid=1001 --clear-groups env \
    HOME="$CHROME_HOME" XDG_CONFIG_HOME="$CHROME_HOME/.config" \
    XDG_CACHE_HOME="$CHROME_HOME/.cache" XDG_RUNTIME_DIR=/tmp/qbox-chrome-runtime \
    DISPLAY="${DISPLAY:-:99}" LD_LIBRARY_PATH="$LD_LIBRARY_PATH" \
    PULSE_SERVER="$PULSE_SERVER" \
    /opt/chromium/chromium $CHROME_FLAGS "$@"
fi
exec /opt/chromium/chromium $CHROME_FLAGS "$@"
