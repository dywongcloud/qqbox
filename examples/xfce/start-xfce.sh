#!/bin/sh
# XFCE-in-laboon run #2: disable xfwm4 compositing so the root window is
# screencapturable, put a real window on screen, and keep VNC live.
set -x
export DISPLAY=:99
export XDG_RUNTIME_DIR=/tmp/xdg
export HOME=/root
mkdir -p "$XDG_RUNTIME_DIR" /out /root/.config/xfce4/xfconf/xfce-perchannel-xml
chmod 700 "$XDG_RUNTIME_DIR"
rm -f /out/vnc.sock /out/vnc-ready.txt /out/xfce*.png /out/result.txt /out/stages.log

stage() { printf '%s %s\n' "$(date -u +%H:%M:%S 2>/dev/null || echo t)" "$*" >>/out/stages.log; }

# Turn off xfwm4 compositing before the session starts: with compositing the
# root window stays black in an XGetImage/scrot capture.
cat > /root/.config/xfce4/xfconf/xfce-perchannel-xml/xfwm4.xml <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfwm4" version="1.0">
  <property name="general" type="empty">
    <property name="use_compositing" type="bool" value="false"/>
  </property>
</channel>
XML
stage "compositing_disabled_in_xfconf"

# Small X diagnostic tools that were missing from the image.
apk add --no-cache xorg-xprop xorg-xwininfo xorg-xdpyinfo xwd xrdb xorg-xsetroot >/out/apk.log 2>&1 || true
stage "apk_x_tools_done"

stage "starting Xvfb"
Xvfb :99 -screen 0 1280x800x24 -nolisten tcp -ac >/out/xvfb.log 2>&1 &
XVFB_PID=$!
sleep 4
kill -0 "$XVFB_PID" 2>/dev/null || { echo XVFB_FAILED >/out/result.txt; exit 1; }
stage "xvfb_alive pid=$XVFB_PID"
xdpyinfo -display :99 >/out/xdpyinfo.txt 2>&1 || true
xrandr -display :99 >/out/xrandr.txt 2>&1 || true
xsetroot -display :99 -solid '#204a87' 2>/out/xsetroot.log || true

stage "starting dbus"
mkdir -p /var/run/dbus
dbus-daemon --system --fork >/out/dbus.log 2>&1 || true

stage "starting startxfce4"
dbus-run-session -- startxfce4 >/out/xfce.log 2>&1 &
XFCE_PID=$!
sleep 25

# A real, unmistakable window on the desktop.
stage "launching xfce4-terminal"
(xfce4-terminal --title=LABOON-XFCE-WITNESS --geometry=80x24+140+140 \
    -e 'sh -c "echo LABOON_XFCE_DESKTOP_RUNNING; uname -a; sleep 300"' \
    >/out/terminal.log 2>&1 &) || true
sleep 8
xsetroot -display :99 -solid '#204a87' 2>>/out/xsetroot.log || true

stage "capturing evidence"
ps aux >/out/processes.txt 2>&1 || ps >/out/processes.txt
xprop -root _NET_SUPPORTING_WM_CHECK _NET_WM_NAME _NET_CLIENT_LIST >/out/wm.txt 2>&1 || true
xwininfo -root -tree >/out/windows.txt 2>&1 || true
xprop -root _NET_ACTIVE_WINDOW >/out/active.txt 2>&1 || true
scrot -o /out/xfce-early.png 2>/out/scrot.log || true
sleep 6
scrot -o /out/xfce.png 2>>/out/scrot.log || import -display :99 -window root /out/xfce.png 2>>/out/scrot.log || true
xwd -root -display :99 -out /out/xfce.xwd 2>/dev/null || true

stage "starting x11vnc"
(x11vnc -display :99 -nopw -forever -shared -rfbport 5900 -o /out/x11vnc.log >/out/x11vnc.stdout 2>&1 &) || true
(x11vnc -display :99 -nopw -forever -shared -unixsock /out/vnc.sock -o /out/x11vnc-unix.log >/out/x11vnc-unix.stdout 2>&1 &) || true
sleep 5
printf 'ready %s\n' "$(date -u +%H:%M:%S 2>/dev/null || echo t)" >/out/vnc-ready.txt
stage "vnc_ready"

if [ -s /out/xfce.png ]; then echo XFCE_SCREENSHOT_OK >/out/result.txt; else echo XFCE_SCREENSHOT_MISSING >/out/result.txt; fi
printf 'xvfb_pid=%s xfce_pid=%s\n' "$XVFB_PID" "$XFCE_PID" >>/out/result.txt
cat /out/result.txt
cat /out/wm.txt 2>/dev/null || true
cat /out/stages.log

# Stay live so a host VNC client can attach to the RUNNING desktop.
sleep 420
stage "desktop_exiting"
