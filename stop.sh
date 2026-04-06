#!/usr/bin/env bash
# stop.sh — stop the ubersdr_qsstv service
#
# Usage:
#   ./stop.sh

set -euo pipefail

INSTALL_DIR="${HOME}/ubersdr/sstv"

cd "${INSTALL_DIR}"
echo "Stopping ubersdr_qsstv..."
docker compose down
echo "Done."
