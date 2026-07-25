#!/usr/bin/env bash
# Build the Cordova Android app, sign it with a persistent local development
# certificate, and install it onto the connected Android device.

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
readonly ANDROID_IMAGE="ghcr.io/cirruslabs/android-sdk:35"
readonly BUILD_TAG="sub2api-cordova-artifact:local"
readonly PACKAGE_NAME="com.sub2api.mobile"

usage() {
  cat <<'EOF'
Usage:
  bash mobile/cordova/build-and-install.sh <server-url>

Examples:
  bash mobile/cordova/build-and-install.sh https://gpt001.iotalking.top
  bash mobile/cordova/build-and-install.sh https://gpt001.iotalking.top/api/v1

The server URL must use HTTPS. A root URL is automatically normalized to
<server-url>/api/v1. The signed APK is written to mobile/cordova/build/.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" || $# -ne 1 ]]; then
  usage
  [[ $# -eq 1 ]] && exit 0
  exit 1
fi

api_url="${1%/}"
if [[ "$api_url" != https://* ]]; then
  printf 'Error: the server URL must start with https://\n' >&2
  exit 1
fi
if [[ "$api_url" != */api/v1 ]]; then
  api_url="$api_url/api/v1"
fi

for command in docker adb; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'Error: %s is required but was not found in PATH.\n' "$command" >&2
    exit 1
  fi
done

if ! adb get-state >/dev/null 2>&1; then
  printf 'Error: connect and authorize exactly one Android device before running this script.\n' >&2
  adb devices -l >&2 || true
  exit 1
fi

readonly OUTPUT_DIR="$SCRIPT_DIR/build"
readonly KEYSTORE_DIR="$SCRIPT_DIR/.dev"
readonly KEY_ALIAS="${SUB2API_KEY_ALIAS:-sub2api-dev}"
readonly KEYSTORE_PASSWORD="${SUB2API_KEYSTORE_PASSWORD:-android}"
readonly KEY_PASSWORD="${SUB2API_KEY_PASSWORD:-$KEYSTORE_PASSWORD}"
readonly SIGNED_APK="$OUTPUT_DIR/sub2api-debug-signed.apk"

mkdir -p "$OUTPUT_DIR" "$KEYSTORE_DIR"

temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-android.XXXXXX")"
container_id=""

cleanup() {
  if [[ -n "$container_id" ]]; then
    docker rm -f "$container_id" >/dev/null 2>&1 || true
  fi
  rm -rf "$temp_dir"
}
trap cleanup EXIT

printf 'Building Android package for %s ...\n' "$api_url"
docker build \
  --file "$SCRIPT_DIR/Dockerfile" \
  --build-arg "VITE_API_BASE_URL=$api_url" \
  --build-arg "SUB2API_BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --target artifact \
  --tag "$BUILD_TAG" \
  "$REPO_ROOT"

container_id="$(docker create "$BUILD_TAG" /placeholder)"
unsigned_apk="$temp_dir/sub2api-release-unsigned.apk"
docker cp "$container_id:/sub2api-release-unsigned.apk" "$unsigned_apk"
docker rm "$container_id" >/dev/null
container_id=""

printf 'Signing APK with the local development certificate ...\n'
docker run --rm --user root \
  -e HOME=/tmp \
  -e KEY_ALIAS="$KEY_ALIAS" \
  -e KEYSTORE_PASSWORD="$KEYSTORE_PASSWORD" \
  -e KEY_PASSWORD="$KEY_PASSWORD" \
  -v "$SCRIPT_DIR:/work" \
  -v "$temp_dir:/artifacts:ro" \
  -w /work \
  --entrypoint /bin/bash \
  "$ANDROID_IMAGE" \
  -lc '
    set -euo pipefail
    if [[ ! -f .dev/sub2api-install.jks ]]; then
      keytool -genkeypair -noprompt -storetype JKS \
        -keystore .dev/sub2api-install.jks \
        -storepass "$KEYSTORE_PASSWORD" \
        -keypass "$KEY_PASSWORD" \
        -alias "$KEY_ALIAS" \
        -dname "CN=Sub2API Development,O=Sub2API,C=CN" \
        -keyalg RSA -keysize 2048 -validity 10000
    fi

    /opt/android-sdk-linux/build-tools/35.0.0/zipalign -p -f 4 \
      /artifacts/sub2api-release-unsigned.apk /tmp/sub2api-aligned.apk
    /opt/android-sdk-linux/build-tools/35.0.0/apksigner sign \
      --ks .dev/sub2api-install.jks \
      --ks-key-alias "$KEY_ALIAS" \
      --ks-pass "pass:$KEYSTORE_PASSWORD" \
      --key-pass "pass:$KEY_PASSWORD" \
      --v1-signing-enabled true \
      --v2-signing-enabled true \
      --v3-signing-enabled true \
      --out build/sub2api-debug-signed.apk /tmp/sub2api-aligned.apk
    /opt/android-sdk-linux/build-tools/35.0.0/apksigner verify --verbose \
      build/sub2api-debug-signed.apk
  '

printf 'Installing %s ...\n' "$SIGNED_APK"
adb install -r "$SIGNED_APK"
adb shell dumpsys package "$PACKAGE_NAME" | grep -m 1 'versionName=' || true

printf 'Installed successfully: %s\n' "$SIGNED_APK"
