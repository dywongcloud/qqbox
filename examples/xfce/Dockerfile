FROM alpine:3.20
# XFCE 4.18 + Xvfb + VNC + Chromium + a PTY-free command console + X diagnostics.
# All packages are linux/arm64 (Alpine aarch64); 370 packages, ~1.1 GiB installed.
RUN apk add --no-cache \
      xfce4 xfce4-session xfce4-terminal xfwm4 xfdesktop xfce4-panel \
      xfce4-settings thunar mousepad xfce4-notifyd \
      xfce4-clipman-plugin pm-utils \
      xvfb x11vnc scrot imagemagick dbus dbus-x11 \
      font-dejavu font-noto font-noto-cjk adwaita-icon-theme hicolor-icon-theme \
      xauth xrandr xset xprop xwininfo xdpyinfo xsetroot xrdb xwd xdotool \
      chromium chromium-swiftshader python3 bash procps
RUN mkdir -p /out /var/run/dbus /root/.config/xfce4/xfconf/xfce-perchannel-xml
COPY start-xfce.sh /usr/local/bin/start-xfce.sh
COPY terminal-console.py /usr/local/bin/terminal-console.py
RUN chmod +x /usr/local/bin/start-xfce.sh /usr/local/bin/terminal-console.py
CMD ["/usr/local/bin/start-xfce.sh"]
