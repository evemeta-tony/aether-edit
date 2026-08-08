#!/usr/bin/env bash
set -euo pipefail
BUILD=/opt/aether-edit/ffmpeg-build; sudo mkdir -p "$BUILD"; sudo chown "$(id -u)":"$(id -g)" "$BUILD"
log="$BUILD/build.log"; exec > >(tee "$log") 2>&1
echo "START $(date -u +%FT%TZ)"
export DEBIAN_FRONTEND=noninteractive
sudo apt-get install -y nasm pkg-config >/dev/null 2>&1 || { echo "FATAL: build deps failed"; exit 1; }
cd "$BUILD"
[ -d nv-codec-headers ] || git clone -q --depth 1 https://github.com/FFmpeg/nv-codec-headers.git
make -C nv-codec-headers >/dev/null && sudo make -C nv-codec-headers install >/dev/null
echo "nv-codec-headers installed"
[ -d ffmpeg-src ] || git clone -q --depth 1 -b n6.1 https://github.com/FFmpeg/FFmpeg.git ffmpeg-src
cd ffmpeg-src && make distclean >/dev/null 2>&1 || true
export PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:${PKG_CONFIG_PATH:-}
./configure --prefix=/opt/aether-edit/ffmpeg --disable-doc \
  --enable-nvenc --enable-nvdec --enable-cuvid \
  --extra-cflags=-I/usr/local/cuda/include --extra-ldflags=-L/usr/local/cuda/lib64 2>&1 | tail -8
echo "=== gpl/nonfree in config (must be NO) ==="; grep -iE 'CONFIG_GPL |CONFIG_NONFREE ' config.h 2>/dev/null | head
make -j"$(nproc)" 2>&1 | tail -4
sudo make install 2>&1 | tail -2
echo "=== installed buildconf ==="; /opt/aether-edit/ffmpeg/bin/ffmpeg -hide_banner -buildconf 2>&1 | grep -iE 'gpl|nonfree' || echo "echo NO-GPL-NONFREE-OK"
/opt/aether-edit/ffmpeg/bin/ffmpeg -hide_banner -encoders 2>&1 | grep -iE 'nvenc' | head
echo "DONE $(date -u +%FT%TZ)"
