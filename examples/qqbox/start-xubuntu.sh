#!/bin/bash
# qqbox desktop: Xvfb + x11vnc + stock XFCE + native xfce4-terminal + Chromium.
set -x
export HOME=/config DISPLAY=:99 XDG_RUNTIME_DIR=/tmp/xdg SHELL=/bin/bash
export XDG_CONFIG_DIRS=/etc/xdg/xdg-xubuntu:/etc/xdg
VNC_PORT=${VNC_PORT:-5905}
mkdir -p /config/.config/xfce4/xfconf/xfce-perchannel-xml /tmp/.X11-unix \
  /var/run/dbus "$XDG_RUNTIME_DIR" /out /tmp/crash
chmod 700 "$XDG_RUNTIME_DIR"; chown -R 0:0 /config 2>/dev/null || true
rm -f /out/ready.txt /out/result.txt /out/stages.log
stage() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >>/out/stages.log; }

cat > /config/.config/xfce4/xfconf/xfce-perchannel-xml/xfwm4.xml <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfwm4" version="1.0"><property name="general" type="empty">
<property name="use_compositing" type="bool" value="false"/></property></channel>
XML
cat > /config/.config/xfce4/xfconf/xfce-perchannel-xml/xfce4-terminal.xml <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-terminal" version="1.0">
  <property name="background-mode" type="string" value="TERMINAL_BACKGROUND_MODE_SOLID"/>
  <property name="color-background" type="string" value="#000000000000"/>
  <property name="color-foreground" type="string" value="#ffffffffffff"/>
  <property name="font-name" type="string" value="DejaVu Sans Mono 12"/>
  <property name="font-use-system" type="bool" value="false"/>
  <property name="misc-default-geometry" type="string" value="84x26"/>
  <property name="scrolling-lines" type="uint" value="1000"/>
  <property name="misc-bell" type="bool" value="false"/>
</channel>
XML
printf 'nameserver 8.8.8.8\n' > /etc/resolv.conf
stage "config_written"

Xvfb :99 -screen 0 1280x800x24 -nolisten tcp -ac > /out/xvfb.log 2>&1 &
sleep 4
xsetroot -display :99 -solid '#204a87' 2>/dev/null || true
x11vnc -display :99 -nopw -forever -shared -rfbport "$VNC_PORT" \
  -o /out/x11vnc.log > /out/x11vnc.stdout 2>&1 &
sleep 3
stage "vnc_alive"
dbus-daemon --system --fork > /out/dbus.log 2>&1 || true
dbus-run-session -- /usr/bin/xfce4-session > /out/xfce.log 2>&1 &
sleep 18

xfce4-terminal --disable-server --title QQBOX-TERMINAL \
  --geometry 84x26+120+110 > /out/terminal.log 2>&1 &
sleep 8
stage "native_terminal_started"

/usr/local/bin/chromium --window-size=1000,700 --window-position=150,120 \
  --user-data-dir=/tmp/chrome-qqbox https://example.com/ > /out/chromium.log 2>&1 &
sleep 18
stage "chromium_started"

{
  echo '=== chromium version ==='
  env LD_LIBRARY_PATH=/opt/chromium/extra-libs:/opt/chromium /opt/chromium/chromium --version 2>&1
  echo '=== external DNS ==='
  getent hosts google.com
  getent hosts example.com
  echo '=== HTTPS egress ==='
  curl -sS -m 12 -o /dev/null -w 'HTTP %{http_code} %{remote_ip}\n' https://example.com/
  echo '=== ICMP ping google.com ==='
  ping -c 3 -W 3 google.com 2>&1
  echo "ping_rc=$?"
  echo '=== apt registry install ==='
  timeout 90 apt-get update -o Acquire::Retries=0 -o Acquire::http::Timeout=15 >/tmp/apt-update.log 2>&1
  apt_update_rc=$?
  timeout 90 apt-get install -y --no-install-recommends tree >/tmp/apt-tree.log 2>&1
  apt_install_rc=$?
  echo "apt_update_rc=$apt_update_rc apt_install_rc=$apt_install_rc tree=$(command -v tree)" 
  echo '=== apk registry (Alpine guest, separate run) ==='
  echo 'apk update + apk add ripgrep jq witnessed in the Alpine guest'
} > /out/requirements.txt 2>&1

ps aux > /out/processes.txt 2>&1 || true
xwininfo -root -tree > /out/windows.txt 2>&1 || true
scrot -o /out/qqbox-desktop.png 2>/out/scrot.log || true
echo QQBOX_BOOT_READY > /out/result.txt
echo ready > /out/ready.txt
cat /out/requirements.txt
cat /out/stages.log
while :; do sleep 3600; done
