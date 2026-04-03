#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/build-headless"

CLEAN=0
for arg in "$@"; do
  case "$arg" in
    --clean|-c) CLEAN=1 ;;
    *) echo "Unknown option: $arg"; echo "Usage: $0 [--clean|-c]"; exit 1 ;;
  esac
done

echo "=== QSSTV Headless Build ==="
echo "Source: ${SCRIPT_DIR}/src"
echo "Build:  ${BUILD_DIR}"
echo ""

if [[ "${CLEAN}" -eq 1 ]]; then
  echo "--- Cleaning build directory ---"
  rm -rf "${BUILD_DIR}"
  echo ""
fi

mkdir -p "${BUILD_DIR}"
cd "${BUILD_DIR}"

echo "--- Running qmake ---"
qmake CONFIG+=headless "${SCRIPT_DIR}/src/qsstv.pro"

echo ""
echo "--- Running make ---"
make -j"$(nproc)"

echo ""
echo "=== Build complete ==="
echo "Binary: ${BUILD_DIR}/qsstv-headless"
echo ""
echo "Usage example:"
echo "  cat audio.raw | ${BUILD_DIR}/qsstv-headless --output-dir /tmp/sstv-images"
