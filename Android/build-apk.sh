#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ANDROID_DIR="$ROOT/Android"
WEB_DIR="$ROOT/Web"

SDK_DIR="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
if [[ -z "$SDK_DIR" && -f "$ANDROID_DIR/local.properties" ]]; then
  SDK_DIR="$(sed -n 's/^sdk.dir=//p' "$ANDROID_DIR/local.properties" | tail -n 1)"
fi
if [[ -z "$SDK_DIR" ]]; then
  SDK_DIR="$HOME/Android/Sdk"
fi
if [[ ! -d "$SDK_DIR/platforms" ]]; then
  echo "Android SDK not found: $SDK_DIR" >&2
  echo "Set ANDROID_HOME or write sdk.dir=<path> into Android/local.properties" >&2
  exit 1
fi

# 1. Rebuild the web app and sync it into APK assets.
#    If npm is unavailable, the checked-in assets are used unchanged.
if command -v npm >/dev/null 2>&1; then
  if [[ ! -d "$WEB_DIR/node_modules" ]]; then
    (cd "$WEB_DIR" && npm ci --include=dev)
  fi
  (cd "$WEB_DIR" && npm run build)
  rm -rf "$ANDROID_DIR/app/src/main/assets/web"
  mkdir -p "$ANDROID_DIR/app/src/main/assets/web"
  cp -a "$WEB_DIR/dist/." "$ANDROID_DIR/app/src/main/assets/web/"
else
  echo "npm not found; using checked-in assets under Android/app/src/main/assets/web" >&2
fi

# 2. Ensure the local (gitignored) strings.xml exists.
if [[ ! -f "$ANDROID_DIR/app/src/main/res/values/strings.xml" ]]; then
  cp "$ANDROID_DIR/strings_example.xml" "$ANDROID_DIR/app/src/main/res/values/strings.xml"
  echo "Created Android/app/src/main/res/values/strings.xml from strings_example.xml" >&2
  echo "Edit target_url there and rerun this script." >&2
fi

# 3. Build the debug APK.
(cd "$ANDROID_DIR" && ANDROID_HOME="$SDK_DIR" ANDROID_SDK_ROOT="$SDK_DIR" ./gradlew assembleDebug --no-daemon)

# 4. Publish a stable copy outside the Gradle build directory.
mkdir -p "$ROOT/dist"
cp "$ANDROID_DIR/app/build/outputs/apk/debug/app-debug.apk" "$ROOT/dist/xbt-debug.apk"
echo "APK: $ROOT/dist/xbt-debug.apk"
