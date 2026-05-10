#!/usr/bin/env bash
# docker.sh — build the ubersdr_qsstv Docker image
#
# All binaries (qsstv-headless, ubersdr_qsstv) are built from source inside
# the Docker image.  No host binaries are required.
#
# Usage:
#   ./docker.sh [build|push|run|arm64|multiarch]
#
#   build      — build the image for linux/amd64 (default, uses buildx)
#   arm64      — build the image for linux/arm64 only (uses buildx)
#   multiarch  — build & load a multi-arch manifest (amd64 + arm64) locally
#   push       — build multi-arch manifest for amd64+arm64 and push to registry
#   run        — run the image (set env vars below)
#
# Environment variables (build):
#   IMAGE      Docker image name/tag   (default: madpsy/ubersdr_qsstv:latest)
#   PLATFORM   Docker --platform flag  (default: linux/amd64)
#   BUILDER    buildx builder name     (default: ubersdr-builder, created if absent)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

IMAGE="${IMAGE:-madpsy/ubersdr_qsstv:latest}"
PLATFORM="${PLATFORM:-linux/amd64}"
BUILDER="${BUILDER:-ubersdr-builder}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

die() { echo "error: $*" >&2; exit 1; }

check_deps() {
    command -v docker >/dev/null || die "docker not found in PATH"
    docker buildx version >/dev/null 2>&1 || die "docker buildx not available (Docker >= 19.03 required)"
}

# Ensure a buildx builder that supports multi-platform builds exists.
ensure_builder() {
    if ! docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
        echo "Creating buildx builder '$BUILDER'..."
        docker buildx create --name "$BUILDER" --driver docker-container --bootstrap
    else
        # Make sure it is running
        docker buildx inspect "$BUILDER" --bootstrap >/dev/null
    fi
}

# Stage the build context into a temp directory, stripping build artefacts.
stage_context() {
    TMPCTX="$(mktemp -d)"
    trap 'rm -rf "$TMPCTX"' EXIT
    echo "Staging build context in $TMPCTX..."
    rsync -a --exclude='/build-headless' \
              --exclude='.git' \
              --exclude='sstv-images' \
              "$SCRIPT_DIR/" "$TMPCTX/"
}

# ---------------------------------------------------------------------------
# Build targets
# ---------------------------------------------------------------------------

# build [platform] [extra buildx flags...]
#   Builds for a single platform and loads the result into the local daemon.
build() {
    local platform="${1:-$PLATFORM}"
    shift || true          # remaining args forwarded to buildx build
    check_deps
    ensure_builder
    stage_context

    echo "Building image $IMAGE (platform=$platform)..."
    docker buildx build \
        --builder "$BUILDER" \
        --platform "$platform" \
        --tag "$IMAGE" \
        --load \
        "$@" \
        "$TMPCTX"

    echo "Built and loaded: $IMAGE"
}

# multiarch — build amd64+arm64 and load a combined manifest into the local daemon.
# NOTE: --load with multiple platforms requires containerd image store
# (Docker Desktop or daemon with containerd snapshotter enabled).
# If your daemon does not support it, use 'push' instead.
multiarch() {
    check_deps
    ensure_builder
    stage_context

    echo "Building multi-arch image $IMAGE (linux/amd64,linux/arm64)..."
    docker buildx build \
        --builder "$BUILDER" \
        --platform linux/amd64,linux/arm64 \
        --tag "$IMAGE" \
        --load \
        "$TMPCTX"

    echo "Built and loaded multi-arch: $IMAGE"
}

# push — build amd64+arm64 and push a multi-arch manifest to the registry,
#        then commit & push the git repository.
push() {
    check_deps
    ensure_builder
    stage_context

    echo "Building and pushing multi-arch image $IMAGE (linux/amd64,linux/arm64)..."
    docker buildx build \
        --builder "$BUILDER" \
        --platform linux/amd64,linux/arm64 \
        --tag "$IMAGE" \
        --push \
        "$TMPCTX"

    echo "Pushed multi-arch manifest: $IMAGE"

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
    build)     build "$PLATFORM" ;;
    arm64)     build linux/arm64 ;;
    multiarch) multiarch ;;
    push)      push ;;
    run)       shift; run_image "$@" ;;
    *)
        echo "Usage: $0 [build|arm64|multiarch|push|run [ubersdr_qsstv-args...]]" >&2
        exit 1
        ;;
esac
