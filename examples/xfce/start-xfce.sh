#!/bin/sh
# Boots the fixed XFCE desktop inside a laboon sandbox.
#
# Fixes vs the first version:
#  * xfce4-terminal / xterm could not allocate a PTY ("Failed to execute child:
#    Failed to open PTY: Not a tty"). Root cause was Linux TIOCPKT (0x5420)
#    missing from the supervisor's ioctl switch. The Go runtime now forwards it
#    to the host pty master with the correct pointer-form ioctl, so the native
#    VTE terminal spawns a real shell and paints text. Colors must be set via
#    xfconf: xfce4-terminal ignores terminalrc ("settings have been migrated").
#  * Chromium is installed and launched multi-process (no --single-process /
#    --no-zygote, which caused a PulseAudio pa_mutex_new() abort, exit 134).
#  * x11vnc starts immediately after Xvfb, before the XFCE session, so the VNC
#    port is available in ~6s rather than ~40s during a cutover.
#  * Exact Alpine X diagnostic tools, system D-Bus directory, missing panel
#    plugins and fonts are installed in the image.
#  * Compositing is disabled in xfconf so the X root is screencapturable.
#  * The desktop stays alive until the outer gtimeout stops it, not a fixed
#    sleep.
set -x
export DISPLAY=:99
export HOME=/root
export XDG_RUNTIME_DIR=/tmp/xdg
export SHELL=/bin/sh
VNC_PORT=${VNC_PORT:-5905}
CONSOLE_PORT=${CONSOLE_PORT:-8765}

mkdir -p "$XDG_RUNTIME_DIR" /out /var/run/dbus /root/.config/xfce4/xfconf/xfce-perchannel-xml
chmod 700 "$XDG_RUNTIME_DIR"
rm -f /out/vnc.sock /out/ready.txt /out/stages.log /out/result.txt
stage() { printf '%s %s\n' "$(date -u +%H:%M:%S 2>/dev/null || echo t)" "$*" >>/out/stages.log; }

cat > /root/.config/xfce4/xfconf/xfce-perchannel-xml/xfwm4.xml <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfwm4" version="1.0">
  <property name="general" type="empty">
    <property name="use_compositing" type="bool" value="false"/>
  </property>
</channel>
XML
# xfce4-terminal settings live in Xfconf, not terminalrc. The stock default
# uses a transparent background, which renders as black-on-black under Xvfb
# with compositing off, so the terminal looks empty even when it works.
cat > /root/.config/xfce4/xfconf/xfce-perchannel-xml/xfce4-terminal.xml <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-terminal" version="1.0">
  <property name="background-mode" type="string" value="TERMINAL_BACKGROUND_MODE_SOLID"/>
  <property name="color-background" type="string" value="#000000000000"/>
  <property name="color-foreground" type="string" value="#ffffffffffff"/>
  <property name="font-name" type="string" value="DejaVu Sans Mono 12"/>
  <property name="font-use-system" type="bool" value="false"/>
  <property name="misc-default-geometry" type="string" value="80x24"/>
  <property name="scrolling-lines" type="uint" value="1000"/>
  <property name="misc-bell" type="bool" value="false"/>
</channel>
XML
# A panel config without the pulseaudio / notification plugins: they load
# libpulse, whose pa_mutex_new() assertion aborts under laboon and produces a
# blocking "Plugin Restart" dialog. The default config also references an
# undefined plugin-7. This one lists only plugins that actually work.
cat > /root/.config/xfce4/xfconf/xfce-perchannel-xml/xfce4-panel.xml <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-panel" version="1.0">
  <property name="configver" type="int" value="2"/>
  <property name="panels" type="array">
    <value type="int" value="1"/>
    <value type="int" value="2"/>
    <property name="dark-mode" type="bool" value="true"/>
    <property name="panel-1" type="empty">
      <property name="position" type="string" value="p=6;x=0;y=0"/>
      <property name="length" type="uint" value="100"/>
      <property name="position-locked" type="bool" value="true"/>
      <property name="icon-size" type="uint" value="16"/>
      <property name="size" type="uint" value="26"/>
      <property name="plugin-ids" type="array">
        <value type="int" value="1"/><value type="int" value="2"/>
        <value type="int" value="3"/><value type="int" value="4"/>
        <value type="int" value="5"/><value type="int" value="6"/>
        <value type="int" value="11"/><value type="int" value="12"/>
        <value type="int" value="14"/>
      </property>
    </property>
    <property name="panel-2" type="empty">
      <property name="autohide-behavior" type="uint" value="1"/>
      <property name="position" type="string" value="p=10;x=0;y=0"/>
      <property name="length" type="uint" value="1"/>
      <property name="position-locked" type="bool" value="true"/>
      <property name="size" type="uint" value="48"/>
      <property name="plugin-ids" type="array">
        <value type="int" value="15"/><value type="int" value="16"/>
        <value type="int" value="17"/><value type="int" value="18"/>
        <value type="int" value="19"/><value type="int" value="20"/>
        <value type="int" value="21"/><value type="int" value="22"/>
      </property>
    </property>
  </property>
  <property name="plugins" type="empty">
    <property name="plugin-1" type="string" value="applicationsmenu"/>
    <property name="plugin-2" type="string" value="tasklist">
      <property name="grouping" type="uint" value="1"/>
    </property>
    <property name="plugin-3" type="string" value="separator">
      <property name="expand" type="bool" value="true"/>
    </property>
    <property name="plugin-4" type="string" value="pager"/>
    <property name="plugin-5" type="string" value="separator"/>
    <property name="plugin-6" type="string" value="systray">
      <property name="square-icons" type="bool" value="true"/>
    </property>
    <property name="plugin-11" type="string" value="separator"/>
    <property name="plugin-12" type="string" value="clock"/>
    <property name="plugin-14" type="string" value="actions"/>
    <property name="plugin-15" type="string" value="showdesktop"/>
    <property name="plugin-16" type="string" value="separator"/>
    <property name="plugin-17" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="laboon-terminal.desktop"/>
      </property>
    </property>
    <property name="plugin-18" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="xfce4-file-manager.desktop"/>
      </property>
    </property>
    <property name="plugin-19" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="xfce4-web-browser.desktop"/>
      </property>
    </property>
    <property name="plugin-20" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="mousepad.desktop"/>
      </property>
    </property>
    <property name="plugin-21" type="string" value="separator"/>
    <property name="plugin-22" type="string" value="directorymenu"/>
  </property>
</channel>
XML
# The dock and the Applications menu open the real VTE terminal. The browser
# console remains available as a fallback, but is not the default terminal.
mkdir -p /root/.local/share/applications
cat > /root/.local/share/applications/laboon-terminal.desktop <<'DESKTOP'
[Desktop Entry]
Version=1.0
Type=Application
Name=Terminal
Comment=Native XFCE terminal (VTE)
Exec=xfce4-terminal --disable-server
Icon=utilities-terminal
Terminal=false
Categories=System;TerminalEmulator;
DESKTOP
cp /root/.local/share/applications/laboon-terminal.desktop \
   /root/.local/share/applications/xfce4-terminal-emulator.desktop
update-desktop-database /root/.local/share/applications 2>/dev/null || true
stage "compositing_and_panel_configured"

stage "starting Xvfb"
Xvfb :99 -screen 0 1280x800x24 -nolisten tcp -ac >/out/xvfb.log 2>&1 &
XVFB_PID=$!
sleep 4
if ! kill -0 "$XVFB_PID" 2>/dev/null; then
    echo XVFB_FAILED >/out/result.txt
    exit 1
fi
xsetroot -display :99 -solid '#204a87' 2>/out/xsetroot.log || true
stage "xvfb_alive pid=$XVFB_PID"

# VNC first: the endpoint is live before the desktop finishes loading.
stage "starting x11vnc tcp=$VNC_PORT"
x11vnc -display :99 -nopw -forever -shared -rfbport "$VNC_PORT" \
  -o /out/x11vnc.log >/out/x11vnc.stdout 2>&1 &
VNC_PID=$!
sleep 3
stage "vnc_alive pid=$VNC_PID"

stage "starting dbus"
dbus-daemon --system --fork >/out/dbus.log 2>&1 || true

stage "starting pipe console fallback on $CONSOLE_PORT"
CONSOLE_PORT="$CONSOLE_PORT" python3 /usr/local/bin/terminal-console.py \
  >/out/console.log 2>&1 &
CONSOLE_PID=$!
sleep 2
stage "console pid=$CONSOLE_PID"

stage "starting startxfce4"
dbus-run-session -- startxfce4 >/out/xfce.log 2>&1 &
XFCE_PID=$!
sleep 22

stage "starting native xfce4-terminal"
# The real terminal is the primary console. It needs the TIOCPKT fix in the Go
# runtime; without it VTE reports "Failed to open PTY: Not a tty".
xfce4-terminal --disable-server --title laboon-terminal \
  --geometry 90x28+120+110 >/out/terminal.log 2>&1 &
TERMINAL_PID=$!
sleep 6
stage "terminal pid=$TERMINAL_PID"

# Chromium is a browser, not the terminal. Multi-process flags are required
# (see the header comment). Its start page is the hardened fallback console,
# kept available on guest loopback; the native terminal is the default.
stage "starting chromium"
(chromium --no-sandbox --disable-gpu --disable-dev-shm-usage \
  --disable-crash-reporter --disable-breakpad --mute-audio --disable-infobars \
  --no-first-run --no-default-browser-check \
  --window-size=1000,660 --window-position=180,190 \
  --user-data-dir=/tmp/chrome-laboon \
  "http://127.0.0.1:$CONSOLE_PORT/" >/out/chromium.log 2>&1 &) || true
sleep 14

stage "capturing evidence"
ps aux >/out/processes.txt 2>&1 || ps >/out/processes.txt
xprop -root _NET_SUPPORTING_WM_CHECK _NET_WM_NAME _NET_CLIENT_LIST >/out/wm.txt 2>&1 || true
xwininfo -root -tree >/out/windows.txt 2>&1 || true
xdpyinfo -display :99 >/out/xdpyinfo.txt 2>&1 || true
scrot -o /out/xfce-desktop.png 2>/out/scrot.log || true
chromium --version >/out/chromium-version.txt 2>&1 || true
python3 -c '
import http.client, json
port = '"$CONSOLE_PORT"'
c = http.client.HTTPConnection("127.0.0.1", port, timeout=8)
c.request("GET", "/healthz")
r = c.getresponse()
health = r.read().decode()
c2 = http.client.HTTPConnection("127.0.0.1", port, timeout=15)
c2.request("POST", "/run", body=json.dumps({"cmd": "echo LABOON_CONSOLE_OK; id; uname -a"}),
           headers={"Content-Type": "application/json"})
r2 = c2.getresponse()
run = r2.read().decode()
print("HEALTH", r.status, health)
print("RUN", r2.status, run)
' >/out/console-probe.txt 2>&1 || true

if [ -s /out/xfce-desktop.png ]; then
    echo XFCE_SCREENSHOT_OK >/out/result.txt
else
    echo XFCE_SCREENSHOT_MISSING >/out/result.txt
fi
cat /out/result.txt
cat /out/console-probe.txt
cat /out/stages.log
echo ready >/out/ready.txt

# Stay live until the outer gtimeout stops us (no fixed 2h sleep).
while :; do sleep 3600; done
