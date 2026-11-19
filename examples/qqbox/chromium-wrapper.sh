#!/bin/sh
# Debian Chromium 150 linux/arm64, with the two bookworm-only libs bundled.
# SwiftShader software GL works under Xvfb; /dev/dri is not passed through.
# --no-sandbox is REQUIRED: laboon's execve ignores setuid, its PID namespace
# emulation is a no-op and installing a seccomp-bpf filter kills the stub, so
# Chromium's own SUID/namespace sandbox cannot be enabled (27 live variants
# tried, rc=1/134). See examples/xubuntu/README.md.
# PulseAudio null sink is the guest's audio backend; do not pass --mute-audio.
export LD_LIBRARY_PATH=/opt/chromium/extra-libs:/opt/chromium
export PULSE_SERVER=${PULSE_SERVER:-unix:/tmp/qbox-pa/native}
exec /opt/chromium/chromium \
  --no-sandbox --single-process --no-zygote --js-flags=--jitless \
  --use-gl=angle --use-angle=swiftshader --enable-unsafe-swiftshader \
  --disable-dev-shm-usage --disable-crash-reporter \
  --disable-breakpad --crash-dumps-dir=/tmp/crash \
  --load-extension=/opt/tencent-browserskill,/opt/qqbox-extension \
  --disable-extensions-except=/opt/tencent-browserskill,/opt/qqbox-extension \
  --no-first-run --no-default-browser-check "$@"
