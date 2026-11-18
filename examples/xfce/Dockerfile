FROM alpine:3.20
RUN apk add --no-cache xfce4 xfce4-session xfce4-terminal xfwm4 xfdesktop xfce4-panel xfce4-settings thunar xvfb x11vnc scrot imagemagick dbus dbus-x11 font-dejavu adwaita-icon-theme xauth xrandr xset bash procps
RUN mkdir -p /out /root/.config/xfce4
COPY start-xfce.sh /start-xfce.sh
RUN chmod +x /start-xfce.sh
CMD ["/start-xfce.sh"]
