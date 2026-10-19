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
  exit 1
fi

KEYSTORE="${XBT_KEYSTORE:-$HOME/.xbt-keystore/xbt-release.jks}"
KEYSTORE_PROPS="${XBT_KEYSTORE_PROPS:-$HOME/.xbt-keystore/keystore.properties}"
KEY_ALIAS="${XBT_KEY_ALIAS:-xbt}"
if [[ ! -f "$KEYSTORE" || ! -f "$KEYSTORE_PROPS" ]]; then
  echo "Release keystore or properties not found:" >&2
  echo "  $KEYSTORE" >&2
  echo "  $KEYSTORE_PROPS" >&2
  exit 1
fi
# shellcheck disable=SC1090
source "$KEYSTORE_PROPS"
: "${STORE_PASS:?STORE_PASS missing in keystore.properties}"
: "${KEY_PASS:?KEY_PASS missing in keystore.properties}"

# 1. Rebuild the web app and sync it into APK assets.
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
  exit 1
fi

# 3. Build the release APK (R8 + resource shrinking).
(cd "$ANDROID_DIR" && ANDROID_HOME="$SDK_DIR" ANDROID_SDK_ROOT="$SDK_DIR" ./gradlew assembleRelease --no-daemon)

RELEASE_DIR="$ANDROID_DIR/app/build/outputs/apk/release"
UNSIGNED="$RELEASE_DIR/app-release-unsigned.apk"
if [[ ! -f "$UNSIGNED" ]]; then
  echo "Unsigned release APK not found: $UNSIGNED" >&2
  exit 1
fi

BUILD_TOOLS_DIR="$(ls -d "$SDK_DIR"/build-tools/*/ 2>/dev/null | sort -V | tail -n 1)"
ZIPALIGN="${BUILD_TOOLS_DIR}zipalign"
APKSIGNER="${BUILD_TOOLS_DIR}apksigner"
if [[ ! -x "$ZIPALIGN" || ! -x "$APKSIGNER" ]]; then
  echo "zipalign/apksigner not found under $BUILD_TOOLS_DIR" >&2
  exit 1
fi

# 4. Align and sign with the release keystore.
ALIGNED="$RELEASE_DIR/app-release-aligned.apk"
"$ZIPALIGN" -p -f 4 "$UNSIGNED" "$ALIGNED"
mkdir -p "$ROOT/dist"
OUT="$ROOT/dist/xbt-release.apk"
"$APKSIGNER" sign \
  --ks "$KEYSTORE" \
  --ks-key-alias "$KEY_ALIAS" \
  --ks-pass "pass:$STORE_PASS" \
  --key-pass "pass:$KEY_PASS" \
  --out "$OUT" \
  "$ALIGNED"
rm -f "$ALIGNED"
"$APKSIGNER" verify --print-certs "$OUT" >/tmp/xbt-apksigner-verify.log
echo "APK: $OUT"
tail -n 2 /tmp/xbt-apksigner-verify.log
