#!/usr/bin/env bash
set -euo pipefail
task_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
: "${ANDROID_HOME:=${ANDROID_SDK_ROOT:-$HOME/Android/Sdk}}"
export ANDROID_HOME
if [[ ! -d "$ANDROID_HOME/ndk" ]]; then
    echo 'Install Android NDK 28.2.13676358 through sdkmanager before building.' >&2
    exit 1
fi
mkdir -p "$task_root/android/app/libs"
cd "$task_root/engine"
# gomobile locates gobind through PATH even when gomobile itself uses Go's tool directive.
task_tools="$(mktemp -d)"
trap 'rm -rf -- "$task_tools"' EXIT
go build -o "$task_tools/gobind" golang.org/x/mobile/cmd/gobind
export PATH="$task_tools:$PATH"
go tool gomobile init
go tool gomobile bind -target=android/arm64,android/amd64 -androidapi=26 \
    -javapkg=chat.localmessenger.engine -ldflags='-s -w' \
    -o "$task_root/android/app/libs/messenger.aar" ./mobile
