#!/bin/bash
# Boots the stock linuxserver/webtop ubuntu-xfce (Xubuntu) desktop in qqbox.
# Only the X server/VNC adapter is qqbox-specific; the XFCE session, theme,
# Firefox, Thunar and xfce4-terminal are the image's own.
set -x
export HOME=/config DISPLAY=:99 XDG_RUNTIME_DIR=/tmp/xdg SHELL=/bin/bash
export XDG_CONFIG_DIRS=/etc/xdg/xdg-xubuntu:/etc/xdg
export DEBIAN_FRONTEND=noninteractive
VNC_PORT=${VNC_PORT:-5905}
mkdir -p /config /tmp/.X11-unix /var/run/dbus "$XDG_RUNTIME_DIR" /out \
  /config/.config/xfce4/xfconf/xfce-perchannel-xml
chmod 700 "$XDG_RUNTIME_DIR"
chown -R 0:0 /config 2>/dev/null || true
rm -f /out/ready.txt /out/result.txt /out/stages.log
stage() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >>/out/stages.log; }

# xfce4-terminal reads Xfconf, not terminalrc. Its stock transparent background
# paints black-on-black with compositing off, so set solid colors here.
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
# qqbox's supervisor intercepts every :53 query; an explicit nameserver keeps
# glibc clients (Firefox, curl, apt) deterministic on the stock rootfs.
printf 'nameserver 8.8.8.8\n' > /etc/resolv.conf
stage "config_written"

stage "starting Xvfb"
Xvfb :99 -screen 0 1280x800x24 -nolisten tcp -ac > /out/xvfb.log 2>&1 &
XPID=$!
sleep 4
if ! kill -0 "$XPID" 2>/dev/null; then
    echo XVFB_FAILED > /out/result.txt; tail -30 /out/xvfb.log; exit 1
fi
xsetroot -display :99 -solid '#204a87' 2>/dev/null || true
stage "xvfb_alive pid=$XPID"

# VNC before the session, so the endpoint is reachable in seconds.
stage "starting x11vnc tcp=$VNC_PORT"
x11vnc -display :99 -nopw -forever -shared -rfbport "$VNC_PORT" \
  -o /out/x11vnc.log > /out/x11vnc.stdout 2>&1 &
sleep 3
stage "vnc_alive"

stage "starting dbus"
dbus-daemon --system --fork > /out/dbus.log 2>&1 || true

stage "starting stock xfce4-session"
dbus-run-session -- /usr/bin/xfce4-session > /out/xfce.log 2>&1 &
sleep 18

stage "starting native xfce4-terminal"
xfce4-terminal --disable-server --title QQBOX-XUBUNTU-TERMINAL \
  --geometry 84x26+120+110 > /out/terminal.log 2>&1 &
sleep 8
stage "terminal_started"

# A real browser on a public page: visible proof of guest egress. The stock
# Firefox cannot be used: its content sandbox calls chroot(), which qqbox's
# vfs-only namespace refuses (EPERM), and its renderers crash (signal 11).
# NetSurf is a small GTK browser that renders HTTPS under qqbox; witness
# `netsurf-http.png` shows the real Example Domain page.
stage "starting netsurf browser"
(netsurf-gtk https://example.com/ > /out/netsurf.log 2>&1 &) || true
sleep 12
# Focus the browser for one screenshot, then leave the native terminal on top
# so the VNC user can type into a real shell immediately.
BWID=$(xdotool search --onlyvisible --name 'NetSurf' 2>/dev/null | tail -1)
[ -n "$BWID" ] && xdotool windowactivate --sync "$BWID" 2>/dev/null || true
sleep 2
scrot -o /out/xubuntu-browser.png 2>/out/scrot-browser.log || true
TWID=$(xdotool search --onlyvisible --name 'QQBOX-XUBUNTU-TERMINAL' 2>/dev/null | tail -1)
[ -n "$TWID" ] && xdotool windowactivate --sync "$TWID" 2>/dev/null || true
stage "windows ordered browser=$BWID terminal=$TWID"

stage "capturing network evidence"
{
  echo '=== resolv.conf ==='; cat /etc/resolv.conf
  echo '=== getent ==='; getent hosts example.com
  echo '=== curl example.com ==='
  curl -sS -m 12 -o /dev/null -w 'HTTP %{http_code} %{remote_ip}\n' https://example.com/ 2>&1
  echo '=== curl google ==='
  curl -sS -m 12 -o /dev/null -w 'HTTP %{http_code} %{remote_ip}\n' https://www.google.com/ 2>&1
} > /out/network.txt 2>&1

sleep 10
stage "capturing evidence"
ps aux > /out/processes.txt 2>&1 || true
xprop -root _NET_SUPPORTING_WM_CHECK _NET_WM_NAME > /out/wm.txt 2>&1 || true
xwininfo -root -tree > /out/windows.txt 2>&1 || true
xdpyinfo -display :99 > /out/xdpyinfo.txt 2>&1 || true
scrot -o /out/xubuntu-desktop.png 2>/out/scrot.log || true
xwd -root -display :99 -out /out/xubuntu-desktop.xwd > /out/xwd.log 2>&1 || true
xfconf-query -c xfce4-terminal -l -v > /out/xfconf-terminal.txt 2>&1 || true
echo XUBUNTU_DESKTOP_READY > /out/result.txt
echo ready > /out/ready.txt
cat /out/network.txt
cat /out/stages.log
while :; do sleep 3600; done
