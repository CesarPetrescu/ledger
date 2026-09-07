FROM archlinux:base@sha256:82b1b08faae9d61e3e7e13d562f4d09114d939105b0d59ff34140f3bd418593a
# Wine 10 stubs CreateSymbolicLinkW; use current Wine to exercise reparse points.
RUN pacman -Syu --noconfirm wine xorg-server-xvfb xorg-xauth ca-certificates && pacman -Scc --noconfirm
ENV WINEDEBUG=-all WINEPREFIX=/tmp/wine WINEARCH=win64 WINEDLLOVERRIDES="mscoree,mshtml="
