#!/usr/bin/env bash
# start.sh — start the ubersdr_qsstv service
#
# Usage:
#   ./start.sh

set -euo pipefail

INSTALL_DIR="${HOME}/ubersdr/sstv"

cd "${INSTALL_DIR}"
echo "Starting ubersdr_qsstv..."
docker compose up -d --remove-orphans
echo "Done."
echo "  View logs : docker compose logs -f"
