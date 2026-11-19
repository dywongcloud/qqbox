#!/bin/bash
# Tencent qqbox desktop: bottom dock, audio, root@qbox, real Chromium with a
# preloaded extension, and live witness evidence.
set -x
export HOME=/config DISPLAY=:99 XDG_RUNTIME_DIR=/tmp/xdg SHELL=/bin/bash
export XDG_CONFIG_HOME=/config/.config XDG_CONFIG_DIRS=/etc/xdg
VNC_PORT=${VNC_PORT:-5905}
mkdir -p "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml" \
  /tmp/.X11-unix /var/run/dbus "$XDG_RUNTIME_DIR" /out /tmp/crash \
  /tmp/qbox-pa /tmp/qbox-home/.config/pulse /tmp/qbox-home/.local/share
chmod 700 "$XDG_RUNTIME_DIR"; chown -R 0:0 /config 2>/dev/null || true
rm -f /out/ready.txt /out/result.txt /out/stages.log
stage() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >>/out/stages.log; }

# ---- hostname: qbox (sethostname works inside qqbox) ----
hostname qbox 2>/dev/null || true
printf 'qbox\n' > /etc/hostname
printf '127.0.0.1 qbox localhost\n::1 qbox localhost ip6-localhost ip6-loopback\n' > /etc/hosts
cat > /etc/gai.conf <<'GAI'
precedence ::ffff:0:0/96 100
GAI
cat > /etc/profile.d/qqbox-prompt.sh <<'PROMPT'
export HOSTNAME=qbox
PS1='\u@qbox:\w\$ '
PROMPT
cat >> /root/.bashrc <<'BASH'
export HOSTNAME=qbox
PS1='\u@qbox:\w\$ '
BASH
cat > /out/qbox-bashrc <<'RC'
[ -f /root/.bashrc ] && . /root/.bashrc
RC
stage "hostname_qbox=$(hostname)"

# ---- xfconf: solid-color native terminal with a qbox prompt ----
cat > "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml/xfwm4.xml" <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfwm4" version="1.0"><property name="general" type="empty">
<property name="use_compositing" type="bool" value="false"/></property></channel>
XML
cat > "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml/xfce4-terminal.xml" <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-terminal" version="1.0">
  <property name="background-mode" type="string" value="TERMINAL_BACKGROUND_MODE_SOLID"/>
  <property name="color-background" type="string" value="#000000000000"/>
  <property name="color-foreground" type="string" value="#ffffffffffff"/>
  <property name="font-name" type="string" value="DejaVu Sans Mono 12"/>
  <property name="font-use-system" type="bool" value="false"/>
  <property name="misc-default-geometry" type="string" value="84x26"/>
  <property name="misc-login-shell" type="bool" value="true"/>
  <property name="scrolling-lines" type="uint" value="1000"/>
  <property name="misc-bell" type="bool" value="false"/>
</channel>
XML

# ---- bottom dock/taskbar: p=10 is bottom on XFCE 4.18 ----
mkdir -p "$HOME/.local/share/applications"
cat > "$HOME/.local/share/applications/qqbox-terminal.desktop" <<'DESK'
[Desktop Entry]
Type=Application
Name=Terminal
Exec=xfce4-terminal --disable-server
Icon=utilities-terminal
Categories=System;TerminalEmulator;
DESK
cat > "$HOME/.local/share/applications/qqbox-files.desktop" <<'DESK'
[Desktop Entry]
Type=Application
Name=Files
Exec=thunar
Icon=system-file-manager
Categories=System;FileManager;
DESK
cat > "$HOME/.local/share/applications/qqbox-browser.desktop" <<'DESK'
[Desktop Entry]
Type=Application
Name=Chromium
Exec=/usr/local/bin/chromium
Icon=web-browser
Categories=Network;WebBrowser;
DESK
cat > "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml/xfce4-panel.xml" <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-panel" version="1.0">
  <property name="configver" type="int" value="2"/>
  <property name="panels" type="array">
    <value type="int" value="1"/>
    <property name="panel-1" type="empty">
      <property name="autohide-behavior" type="uint" value="0"/>
      <property name="position" type="string" value="p=10;x=0;y=0"/>
      <property name="length" type="uint" value="100"/>
      <property name="position-locked" type="bool" value="true"/>
      <property name="icon-size" type="uint" value="24"/>
      <property name="size" type="uint" value="40"/>
      <property name="plugin-ids" type="array">
        <value type="int" value="1"/>
        <value type="int" value="2"/>
        <value type="int" value="3"/>
        <value type="int" value="4"/>
        <value type="int" value="5"/>
        <value type="int" value="6"/>
      </property>
    </property>
  </property>
  <property name="plugins" type="empty">
    <property name="plugin-1" type="string" value="applicationsmenu"/>
    <property name="plugin-2" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="qqbox-terminal.desktop"/>
      </property>
    </property>
    <property name="plugin-3" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="qqbox-files.desktop"/>
      </property>
    </property>
    <property name="plugin-4" type="string" value="launcher">
      <property name="items" type="array">
        <value type="string" value="qqbox-browser.desktop"/>
      </property>
    </property>
    <property name="plugin-5" type="string" value="separator">
      <property name="expand" type="bool" value="true"/>
    </property>
    <property name="plugin-6" type="string" value="tasklist">
      <property name="grouping" type="uint" value="1"/>
    </property>
  </property>
</channel>
XML
printf 'nameserver 8.8.8.8\n' > /etc/resolv.conf
stage "config_written"

# ---- audio: PulseAudio null sink (no /dev/snd in qqbox) ----
cat > /tmp/qbox-pa/default.pa <<'PA'
load-module module-null-sink sink_name=qqbox sink_properties=device.description=QQBox
load-module module-native-protocol-unix socket=/tmp/qbox-pa/native auth-anonymous=1
set-default-sink qqbox
PA
# PULSE_SERVER must NOT be exported to the daemon: pulseaudio refuses to
# autostart when a user-configured server is set. Clients get it below.
env -u PULSE_SERVER HOME=/tmp/qbox-home PULSE_RUNTIME_PATH=/tmp/qbox-pa \
  PULSE_CONFIG_PATH=/tmp/qbox-home/.config/pulse PULSE_NO_AUTOSPAWN=1 \
  pulseaudio -n --file=/tmp/qbox-pa/default.pa --start --exit-idle-time=-1 \
  --disallow-module-loading=0 --log-target=file:/out/pulseaudio.log \
  --log-level=info > /out/pulseaudio.stdout 2>&1; echo "pulseaudio_start_rc=$?" > /out/pulseaudio-rc.txt
export PULSE_SERVER=unix:/tmp/qbox-pa/native
stage "pulseaudio_started $(cat /out/pulseaudio-rc.txt)"

# ---- X server + VNC first ----
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
xfce4-panel > /out/panel.log 2>&1 &
sleep 5
stage "bottom_panel_started"

xfce4-terminal --disable-server --title QQBOX-TERMINAL \
  --dynamic-title-mode=none --geometry 84x26+120+110 \
  --working-directory=/out --command 'bash --rcfile /out/qbox-bashrc -i' \
  > /out/terminal.log 2>&1 &
sleep 8
stage "native_terminal_started"

# ---- Chromium with the preloaded qqbox extension ----
/usr/local/bin/chromium --load-extension=/opt/qqbox-extension \
  --window-size=1000,700 --window-position=150,120 \
  --user-data-dir=/tmp/chrome-qqbox https://example.com/ > /out/chromium.log 2>&1 &
sleep 18
stage "chromium_started"

# ---- evidence ----
{
  echo '=== toolchain ==='
  for b in python3 pip3 pip node npm git gh claude chromium; do
    printf '%-10s ' "$b"; command -v "$b" || echo MISSING
  done
  echo '--- versions ---'
  python3 --version; pip3 --version; node --version; npm --version
  git --version; gh --version | head -1; claude --version
  env LD_LIBRARY_PATH=/opt/chromium/extra-libs:/opt/chromium /opt/chromium/chromium --version
  echo '=== hostname / prompt ==='
  hostname; cat /etc/hostname; echo "PS1=${PS1-<unset>}"
  echo '=== audio ==='
  pactl info 2>&1 | head -12
  pactl list short sinks 2>&1
  python3 - <<'PY'
import wave, struct, math
w = wave.open('/out/tone.wav', 'wb')
w.setnchannels(1); w.setsampwidth(2); w.setframerate(8000)
w.writeframes(b''.join(struct.pack('<h', int(12000*math.sin(2*math.pi*440*t/8000)) for t in range(2400)))
w.close()
PY
  paplay /out/tone.wav 2>&1; echo "paplay_rc=$?"
  echo '=== network ==='
  getent hosts example.com | head -2
  curl -4 -sS -m 12 -o /dev/null -w 'HTTP %{http_code} %{remote_ip}\n' https://example.com/
  ping -c 3 -W 3 google.com 2>&1 | tail -3
  echo "ping_rc=$?"
  echo '=== extension ==='
  ls -la /opt/qqbox-extension/
  echo '=== skill ==='
  ls -la /root/.claude/skills/tencent-browser/
} > /out/requirements.txt 2>&1

ps aux > /out/processes.txt 2>&1 || true
xwininfo -root -tree > /out/windows.txt 2>&1 || true
scrot -o /out/qqbox-desktop.png 2>/out/scrot.log || true
echo QQBOX_BOOT_READY > /out/result.txt
echo ready > /out/ready.txt
cat /out/requirements.txt
cat /out/stages.log
while :; do sleep 3600; done
