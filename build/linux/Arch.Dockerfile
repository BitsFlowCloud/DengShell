FROM archlinux:base
RUN pacman -Syu --noconfirm --needed base-devel gtk3 webkit2gtk-4.1 iputils libayatana-appindicator xorg-server-xvfb dbus polkit \
    && useradd --create-home --uid 1000 builder \
    && pacman -Scc --noconfirm
ENV LANG=C.UTF-8
WORKDIR /work
