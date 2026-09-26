#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
: "${ANDROID_HOME:=$HOME/Android/Sdk}"
: "${ANDROID_NDK_HOME:=$ANDROID_HOME/ndk/28.2.13676358}"
export ANDROID_HOME ANDROID_NDK_HOME
export GOMAXPROCS="${GOMAXPROCS:-4}"
export GOFLAGS="${GOFLAGS:--buildvcs=false -p=4}"
: "${GOMOBILE:=gomobile}"
"$GOMOBILE" bind -androidapi=24 -target=android/arm64,android/arm,android/amd64,android/386 -o "$PWD/DengShellCore.aar" ./mobile
