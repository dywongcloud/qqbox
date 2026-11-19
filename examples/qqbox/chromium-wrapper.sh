#!/bin/sh
# Debian Chromium 150 linux/arm64, with the two bookworm-only libs bundled.
# SwiftShader (software GL) works under laboon's Xvfb; /dev/dri is not passed
# through, so hardware GPU passthrough is not available. --no-sandbox is
# required: laboon's execve ignores setuid and its seccomp-bpf filter install
# kills the stub, so Chromium's own SUID/namespace sandbox cannot be enabled.
export LD_LIBRARY_PATH=/opt/chromium/extra-libs:/opt/chromium
exec /opt/chromium/chromium \
  --no-sandbox --single-process --no-zygote --js-flags=--jitless \
  --use-gl=angle --use-angle=swiftshader --enable-unsafe-swiftshader \
  --disable-dev-shm-usage --disable-crash-reporter \
  --disable-breakpad --crash-dumps-dir=/tmp/crash --mute-audio \
  --no-first-run --no-default-browser-check "$@"
