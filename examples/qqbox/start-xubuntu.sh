#!/bin/bash
# Tencent qqbox Xubuntu 26 desktop: working Xubuntu wallpaper, bottom dock,
# root-user system bus (apt works), PulseAudio, root@qbox prompt, real Chromium
# with a preloaded extension, and live witness evidence.
set -x
export HOME=/config DISPLAY=:99 XDG_RUNTIME_DIR=/tmp/xdg SHELL=/bin/bash
export XDG_CONFIG_HOME=/config/.config XDG_CONFIG_DIRS=/etc/xdg/xdg-xubuntu:/etc/xdg
VNC_PORT=${VNC_PORT:-5905}
mkdir -p "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml" \
  /tmp/.X11-unix /run/dbus /var/run/dbus "$XDG_RUNTIME_DIR" /out /tmp/crash \
  /tmp/qbox-pa /tmp/qbox-home/.config/pulse /tmp/qbox-home/.local/share
chmod 700 "$XDG_RUNTIME_DIR"; chown -R 0:0 /config 2>/dev/null || true
rm -f /out/ready.txt /out/result.txt /out/stages.log /run/dbus/pid /run/dbus/system_bus_socket
stage() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >>/out/stages.log; }

# Ubuntu 26.04 routes scalable icons through glycin, whose loaders are spawned
# by bwrap; bwrap's mount(MS_SLAVE) fails under qqbox and xfce4-panel aborts.
# Exec the real glycin loader without that unusable mount sandbox. Idempotent:
# the image's Dockerfile also installs this shim, so preserve the original once.
if [ ! -x /usr/bin/bwrap.qqbox-orig ]; then
  cp /usr/bin/bwrap /usr/bin/bwrap.qqbox-orig 2>/dev/null || true
fi
cat > /usr/bin/bwrap <<'SHIM'
#!/bin/sh
i=1
for a in "$@"; do
  case "$a" in
    /usr/libexec/glycin-loaders/*)
      shift "$((i-1))"; exec "$@" ;;
  esac
  i=$((i+1))
done
exec /usr/bin/bwrap.qqbox-orig "$@"
SHIM
chmod 755 /usr/bin/bwrap
echo "bwrap_shim_installed=$(test -x /usr/bin/bwrap.qqbox-orig && echo yes || echo no)" > /out/bwrap-shim.txt

# Install helpers missing on earlier builds; the rebuilt image bakes them.
if ! command -v xdg-user-dir >/dev/null 2>&1 || ! command -v gnome-keyring-daemon >/dev/null 2>&1; then
  timeout 200 apt-get update -o Acquire::Retries=2 -o Acquire::http::Timeout=25 >/out/hygiene-update.log 2>&1 || true
  timeout 250 apt-get install -y --no-install-recommends xdg-user-dirs gnome-keyring >/out/hygiene-install.log 2>&1 || true
fi
echo "xdg_user_dir=$(command -v xdg-user-dir || echo MISSING) gnome_keyring=$(command -v gnome-keyring-daemon || echo MISSING)" > /out/hygiene.txt
# The stock chromium.desktop points at the Ubuntu snap stub, which segfaults
# when clicked from the dock. Override it with the working Debian wrapper.
if [ -f /usr/share/applications/chromium.desktop ]; then
  sed -i 's|^Exec=.*|Exec=/usr/local/bin/chromium|' /usr/share/applications/chromium.desktop
fi
ln -sf /opt/chromium/chromium-wrapper.sh /usr/local/bin/wrapped-chromium
echo "chromium_override_rc=$? exec=$(grep '^Exec=' /usr/share/applications/chromium.desktop 2>/dev/null)" >> /out/hygiene.txt

# 20packagekit's gdbus hook and PackageKit's system-service file ask the bus
# to activate a User=root service, which needs the setuid helper qqbox cannot
# privilege-escalate. Remove both triggers; the root-user bus below handles
# everything else. (Also removed in the image Dockerfile.)
rm -f /etc/apt/apt.conf.d/20packagekit \
      /usr/share/dbus-1/system-services/org.freedesktop.PackageKit.service
echo "packagekit_trigger_removal_rc=$?" > /out/packagekit-removal.txt

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
# The image's /root/.bashrc may carry a literal \x27 from an older build;
# sourcing it emits "x27: command not found" in EVERY interactive child PTY.
if [ -f /root/.bashrc ]; then
  python3 - <<'PY'
p = '/root/.bashrc'
lines = [l for l in open(p) if '\\x27' not in l]
open(p, 'w').writelines(lines)
PY
fi
# A child bash inherits SHELLOPTS from the wrapper below. Setting it empty
# disables `monitor` before bash's job-control init, so it never calls
# tcsetpgrp on a PTY qqbox cannot fully emulate ("no job control in background:
# Bad file descriptor").

cat > /out/qbox-bashrc <<'RC'
[ -f /root/.bashrc ] && . /root/.bashrc
RC
stage "hostname_qbox=$(hostname)"

# ---- root-user system bus: fixes GDBus Spawn.PermissionsInvalid ----
# dbus-daemon --system drops to messagebus and needs the setuid-root launch
# helper, which qqbox's execve cannot privilege-escalate. This config keeps the
# bus as root so apt's D-Bus activations (PackageKit, systemd1, ...) work.
dbus-daemon --config-file=/etc/dbus-1/qqbox-system.conf --fork > /out/dbus.log 2>&1
echo "system_bus_rc=$?" > /out/dbus-rc.txt
gdbus call --system --dest org.freedesktop.DBus --object-path /org/freedesktop/DBus \
  --method org.freedesktop.DBus.ListNames > /out/dbus-ping.txt 2>&1
echo "dbus_list_rc=$?" >> /out/dbus-ping.txt
gdbus call --system --dest org.freedesktop.PackageKit --object-path / \
  --method org.freedesktop.DBus.Peer.Ping > /out/dbus-packagekit.txt 2>&1
echo "packagekit_ping_rc=$? (ServiceUnknown is expected; PermissionsInvalid is not)" >> /out/dbus-packagekit.txt
stage "system_bus_started $(cat /out/dbus-rc.txt)"

# ---- xfconf: disable GNOME compat and desktop-icon plugins ----
# LaunchGNOME=true makes xfce4-session spawn gnome-keyring-daemon, which was
# missing => "Failed to execute child process". Desktop icons trigger
# xfdesktop_regular_file_icon_new / thunarx GLib CRITICAL assertions on every
# right-click. Both are disabled; the wallpaper is painted by xwallpaper.
mkdir -p "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml"
cat > "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml/xfce4-session.xml" <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-session" version="1.0">
  <property name="compat" type="empty">
    <property name="LaunchGNOME" type="bool" value="false"/>
  </property>
</channel>
XML

# ---- xfconf: wallpaper + solid-color native terminal with a qbox prompt ----
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
# The stock Xubuntu 26 wallpapers live in /usr/share/xfce4/backdrops/.
# Ubuntu 26.04's gdk-pixbuf/glycin loaders require bwrap, which fails under
# qqbox ('bwrap: Failed to make / slave: Bad address'), so xfdesktop cannot
# decode a PNG: it paints a solid white/black root. xwallpaper uses libpng
# directly and bypasses glycin entirely. Disable xfdesktop's own backdrop
# painting and paint the root pixmap with xwallpaper after the WM is up.
# XFCE 4.20 uses monitorscreen/workspaceN/last-image; Xubuntu 26's default
# xfce4-desktop.xml has no backdrop keys at all.
if [ -f /usr/share/xfce4/backdrops/xubuntu-wallpaper.png ]; then
  WALL=/usr/share/xfce4/backdrops/xubuntu-wallpaper.png
elif [ -f /usr/share/xfce4/backdrops/xubuntu-resolute-plucky-remix.png ]; then
  WALL=/usr/share/xfce4/backdrops/xubuntu-resolute-plucky-remix.png
else
  WALL=$(ls /usr/share/xfce4/backdrops/*.png 2>/dev/null | head -1)
fi
echo "wallpaper=$WALL" > /out/wallpaper.txt
cat > "$XDG_CONFIG_HOME/xfce4/xfconf/xfce-perchannel-xml/xfce4-desktop.xml" <<XML
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-desktop" version="1.0">
  <property name="backdrop" type="empty">
    <property name="screen0" type="empty">
      <property name="monitorscreen" type="empty">
        <property name="workspace0" type="empty">
          <property name="last-image" type="string" value="$WALL"/>
          <property name="image-style" type="int" value="5"/>
          <property name="color-style" type="int" value="0"/>
          <property name="image-show" type="bool" value="false"/>
        </property>
      </property>
    </property>
  </property>
  <property name="desktop-icons" type="empty">
    <property name="style" type="int" value="0"/>
  </property>
</channel>
XML

# ---- bottom dock/taskbar: p=10 is bottom on XFCE 4.18+ ----
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
      <property name="items" type="array"><value type="string" value="qqbox-terminal.desktop"/></property>
    </property>
    <property name="plugin-3" type="string" value="launcher">
      <property name="items" type="array"><value type="string" value="qqbox-files.desktop"/></property>
    </property>
    <property name="plugin-4" type="string" value="launcher">
      <property name="items" type="array"><value type="string" value="qqbox-browser.desktop"/></property>
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
stage "config_written wallpaper=$WALL"

# ---- audio: PulseAudio null sink (no /dev/snd in qqbox) ----
cat > /tmp/qbox-pa/default.pa <<'PA'
load-module module-null-sink sink_name=qqbox sink_properties=device.description=QQBox
load-module module-native-protocol-unix socket=/tmp/qbox-pa/native auth-anonymous=1
set-default-sink qqbox
PA
env -u PULSE_SERVER HOME=/tmp/qbox-home PULSE_RUNTIME_PATH=/tmp/qbox-pa \
  PULSE_CONFIG_PATH=/tmp/qbox-home/.config/pulse PULSE_NO_AUTOSPAWN=1 \
  pulseaudio -n --file=/tmp/qbox-pa/default.pa --start --exit-idle-time=-1 \
  --disallow-module-loading=0 --log-target=file:/out/pulseaudio.log \
  --log-level=info > /out/pulseaudio.stdout 2>&1; echo "pulseaudio_start_rc=$?" > /out/pulseaudio-rc.txt
export PULSE_SERVER=unix:/tmp/qbox-pa/native
stage "pulseaudio_started $(cat /out/pulseaudio-rc.txt)"

# ---- X server + VNC ----
Xvfb :99 -screen 0 1280x800x24 -nolisten tcp -ac > /out/xvfb.log 2>&1 &
sleep 4
x11vnc -display :99 -nopw -forever -shared -rfbport "$VNC_PORT" \
  -o /out/x11vnc.log > /out/x11vnc.stdout 2>&1 &
sleep 3
stage "vnc_alive"

# ---- terminal launcher, started BY the session (no SM warning) ----
mkdir -p "$XDG_CONFIG_HOME/autostart"
cat > /usr/local/bin/qqbox-shell <<'LAUNCH'
#!/bin/sh
export SHELLOPTS=
exec bash --rcfile /out/qbox-bashrc -i
LAUNCH
chmod 755 /usr/local/bin/qqbox-shell
cat > /usr/local/bin/qqbox-terminal <<'LAUNCH'
#!/bin/sh
exec xfce4-terminal --disable-server --title QQBOX-TERMINAL \
  --dynamic-title-mode=none --geometry 84x26+120+110 \
  --working-directory=/out --command /usr/local/bin/qqbox-shell
LAUNCH
chmod 755 /usr/local/bin/qqbox-terminal
cat > "$XDG_CONFIG_HOME/autostart/qqbox-terminal.desktop" <<'DESK'
[Desktop Entry]
Type=Application
Name=QQBOX Terminal
Exec=/usr/local/bin/qqbox-terminal
OnlyShowIn=XFCE;
DESK

# ---- XFCE session (provides the session bus for xfconfd/xfdesktop) ----
# xfce4-session owns ONE xfwm4/xfce4-panel/xfdesktop/xfconfd via autostart.
# Manually starting duplicates produced "Another Window Manager is already
# running", "Another desktop manager is already running", two panels and two
# xfconfd processes. Only paint the backdrop ourselves.
dbus-run-session -- /usr/bin/xfce4-session > /out/xfce.log 2>&1 &
sleep 18
xwallpaper --zoom "$WALL" > /out/xwallpaper.log 2>&1; echo "xwallpaper_rc=$?" > /out/xwallpaper-rc.txt
sleep 3
scrot -o /out/01-wallpaper.png 2>/dev/null || true
stage "wallpaper_and_bottom_panel_started $(cat /out/xwallpaper-rc.txt)"

# The session starts the terminal from autostart/qqbox-terminal.desktop.
stage "native_terminal_started_by_session"

# ---- Chromium with the preloaded qqbox + Tencent BrowserSkill extensions ----
/usr/local/bin/chromium --window-size=1000,700 --window-position=150,120 \
  --user-data-dir=/tmp/chrome-qqbox https://example.com/ > /out/chromium.log 2>&1 &
sleep 18
stage "chromium_started"

# ---- evidence ----
{
  echo '=== os-release ==='; grep -E 'PRETTY_NAME|VERSION_ID|VERSION_CODENAME|ID=' /etc/os-release
  echo '=== toolchain ==='
  for b in python3 pip3 pip node npm git gh claude chromium vi vim vim.tiny \
           nano less man find xargs grep awk sed diff patch make gcc g++ \
           tree ifconfig ip ss dig wget curl rsync zip unzip tar ps htop jq \
           strace lsof tmux gpg xfdesktop xfconf-query pactl Xvfb x11vnc \
           scrot xdotool ping fastfetch; do
    printf '%-12s ' "$b"; command -v "$b" || echo MISSING
  done
  echo '--- versions ---'
  python3 --version; pip3 --version; node --version; npm --version
  git --version; gh --version | head -1; claude --version
  vim --version | head -1; vi --version | head -1
  env LD_LIBRARY_PATH=/opt/chromium/extra-libs:/opt/chromium /opt/chromium/chromium --version
  echo '--- vi/vim edit witness ---'
  printf 'hello\n' > /out/vi-edit.txt
  vim -Es -u NONE -c '1s/hello/qqbox-vim/' -c 'wq' /out/vi-edit.txt; echo "vim_rc=$?"; cat /out/vi-edit.txt
  vi -es -u NONE -c '1s/qqbox-vim/qqbox-vi/' -c 'wq' /out/vi-edit.txt; echo "vi_rc=$?"; cat /out/vi-edit.txt
  echo '=== hostname / prompt ==='
  hostname; cat /etc/hostname
  grep -F 'PS1=' /out/qbox-bashrc /etc/profile.d/qqbox-prompt.sh /root/.bashrc | tail -3
  echo '=== audio ==='
  pactl info 2>&1 | head -12
  pactl list short sinks 2>&1
  python3 -c "import wave,struct,math;w=wave.open('/out/tone.wav','wb');w.setnchannels(1);w.setsampwidth(2);w.setframerate(8000);w.writeframes(b''.join(struct.pack('<h',int(12000*math.sin(2*math.pi*440*t/8000))) for t in range(2400)));w.close()"
  paplay /out/tone.wav 2>&1; echo "paplay_rc=$?"
  echo '=== network ==='
  getent hosts example.com | head -2
  curl -4 -sS -m 12 -o /dev/null -w 'HTTP %{http_code} %{remote_ip}\n' https://example.com/
  ping -c 3 -W 3 google.com 2>&1 | tail -3
  echo "ping_rc=$?"
  echo '=== D-Bus setuid-helper fix ==='
  cat /out/dbus-rc.txt; cat /out/dbus-ping.txt; cat /out/dbus-packagekit.txt
  echo '--- repeat apt installs (the user failing case) ---'
  apt-get update -o Acquire::Retries=2 -o Acquire::http::Timeout=25 >/out/apt-final-update.log 2>&1; echo "apt_update_rc=$?"
  for p in fastfetch tree htop; do
    apt-get install -y --no-install-recommends "$p" >"/out/apt-final-$p.log" 2>&1
    echo "$p rc=$? dpkg=$(dpkg-query -W -f='${Status}' "$p" 2>/dev/null)"
  done
  echo "npm_payload=$(npm --version 2>&1) (apt npm is unavailable on 26.04; Node 18 payload is used)"
  grep -rin 'PermissionsInvalid\|setuid helper' /out/apt-final-*.log /out/dbus*.log || echo NO_DBUS_ERROR
  echo '=== dock / taskbar ==='
  echo "bwrap_shim=$(head -1 /usr/bin/bwrap) fallback=$(test -x /usr/bin/bwrap.qqbox-orig && echo present || echo missing)"
  echo "xfce4_panel_procs=$(pgrep -c xfce4-panel || true)"
  xwininfo -root -tree 2>/dev/null | grep -i xfce4-panel || echo NO_PANEL_WINDOW
  echo '=== wallpaper ==='
  cat /out/wallpaper.txt
  xfconf-query -c xfce4-desktop -p /backdrop/screen0/monitorscreen/workspace0/last-image 2>&1
  cat /out/xwallpaper-rc.txt; cat /out/xwallpaper-final-rc.txt
  echo "xfdesktop_procs=$(pgrep -c xfdesktop || true)"
  echo '=== extension / skill ==='
  ls -la /opt/qqbox-extension/ /opt/tencent-browserskill/ | head -20
  ls -la /root/.claude/skills/
} > /out/requirements.txt 2>&1

ps aux > /out/processes.txt 2>&1 || true
xwininfo -root -tree > /out/windows.txt 2>&1 || true
# Repaint after every window is mapped: Xvfb does not always flush the root
# pixmap on the first paint, so capture a deterministic final frame.
xwallpaper --zoom "$WALL" >/out/xwallpaper-final.log 2>&1; echo "xwallpaper_final_rc=$?" > /out/xwallpaper-final-rc.txt
sleep 3
scrot -o /out/01-wallpaper-final.png 2>/dev/null || true
scrot -o /out/qqbox-desktop.png 2>/out/scrot.log || true
echo QQBOX_BOOT_READY > /out/result.txt
echo ready > /out/ready.txt
cat /out/requirements.txt
cat /out/stages.log
while :; do sleep 3600; done
