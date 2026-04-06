#!/usr/bin/env bash
# docker.sh — build the ubersdr_qsstv Docker image
#
# All binaries (qsstv-headless, ubersdr_qsstv) are built from source inside
# the Docker image.  No host binaries are required.
#
# Usage:
#   ./docker.sh [build|push|run]
#
#   build  — build the image (default)
#   push   — build then push to registry (set IMAGE env var)
#   run    — run the image (set env vars below)
#
# Environment variables (build):
#   IMAGE      Docker image name/tag   (default: madpsy/ubersdr_qsstv:latest)
#   PLATFORM   Docker --platform flag  (default: linux/amd64)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

IMAGE="${IMAGE:-madpsy/ubersdr_qsstv:latest}"
PLATFORM="${PLATFORM:-linux/amd64}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

die() { echo "error: $*" >&2; exit 1; }

check_deps() {
    command -v docker >/dev/null || die "docker not found in PATH"
}

build() {
    check_deps

    # Create a temporary build context from the source tree only
    TMPCTX="$(mktemp -d)"
    trap 'rm -rf "$TMPCTX"' EXIT

    echo "Staging build context in $TMPCTX..."

    # Copy source tree (excluding build artefacts and git history)
    rsync -a --exclude='/build-headless' \
              --exclude='.git' \
              --exclude='sstv-images' \
              "$SCRIPT_DIR/" "$TMPCTX/"

    echo "Building image $IMAGE (platform=$PLATFORM)..."
    docker build \
        --platform "$PLATFORM" \
        --tag "$IMAGE" \
        "$TMPCTX"

    echo "Built: $IMAGE"
}

push() {
    build
    echo "Pushing $IMAGE..."
    docker push "$IMAGE"
    echo "Committing and pushing git repository..."
    git add -A
    git diff --cached --quiet || git commit -m "Release $IMAGE"
    git push
}

run_image() {
    args=()
    [[ -n "${UBERSDR_URL:-}"      ]] && args+=(-url          "$UBERSDR_URL")
    [[ -n "${UBERSDR_PASS:-}"     ]] && args+=(-pass         "$UBERSDR_PASS")
    [[ -n "${OUTPUT_DIR:-}"       ]] && args+=(-output-dir   "$OUTPUT_DIR")
    [[ -n "${QSSTV_BIN:-}"        ]] && args+=(-qsstv        "$QSSTV_BIN")
    [[ -n "${CTY_FILE:-}"         ]] && args+=(-cty-file     "$CTY_FILE")
    [[ -n "${WEB_PORT:-}"         ]] && args+=(-web-port     "$WEB_PORT")
    [[ -n "${RECEIVER_LAT:-}"     ]] && args+=(-receiver-lat "$RECEIVER_LAT")
    [[ -n "${RECEIVER_LON:-}"     ]] && args+=(-receiver-lon "$RECEIVER_LON")
    [[ "${WEB_TLS:-}" == "1"      ]] && args+=(-tls)

    # UBERSDR_CHANNELS is comma-separated; expand each as a -channel flag
    if [[ -n "${UBERSDR_CHANNELS:-}" ]]; then
        IFS=',' read -ra _chs <<< "$UBERSDR_CHANNELS"
        for ch in "${_chs[@]}"; do
            ch="${ch// /}"
            [[ -n "$ch" ]] && args+=(-channel "$ch")
        done
    fi

    # Any positional args to docker.sh run are appended verbatim
    args+=("${@}")

    docker run --rm -it \
        --platform "$PLATFORM" \
        "$IMAGE" \
        "${args[@]}"
}

# ---------------------------------------------------------------------------
# Environment variable reference (for docker run -e ...)
# ---------------------------------------------------------------------------
#
#   UBERSDR_URL       UberSDR base URL (default: http://ubersdr:8080)
#   UBERSDR_CHANNELS  Comma-separated freq:mode pairs  e.g. 14230000:usb,21335000:usb
#   UBERSDR_PASS      UberSDR bypass password
#   OUTPUT_DIR        Output directory for images (default: /data)
#   QSSTV_BIN         Path to qsstv binary (default: /usr/local/bin/qsstv)
#   CTY_FILE          Path to CTY.DAT override (default: embedded)
#   WEB_PORT          Web gallery port (default: 6091, 0 = disabled)
#   WEB_TLS           Set to 1 to enable HTTPS with auto-generated self-signed cert
#   RECEIVER_LAT      Receiver latitude for origin map
#   RECEIVER_LON      Receiver longitude for origin map

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

case "${1:-build}" in
    build) build ;;
    push)  push  ;;
    run)   shift; run_image "$@" ;;
    *)
        echo "Usage: $0 [build|push|run [ubersdr_qsstv-args...]]" >&2
        exit 1
        ;;
esac
