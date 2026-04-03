#!/usr/bin/env bash
# restart.sh — restart the ubersdr_qsstv service
#
# Usage:
#   ./restart.sh

set -euo pipefail

INSTALL_DIR="${HOME}/ubersdr/qsstv"

cd "${INSTALL_DIR}"
echo "Stopping ubersdr_qsstv..."
docker compose down
echo "Starting ubersdr_qsstv..."
docker compose up -d --remove-orphans
echo "Done."
echo "  View logs : docker compose logs -f"
