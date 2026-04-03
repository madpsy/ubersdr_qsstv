#!/bin/sh
# entrypoint.sh — translate environment variables into ubersdr_qsstv flags
#
# Environment variables:
#   UBERSDR_URL       UberSDR base URL (default: http://172.20.0.1:8080)
#   UBERSDR_CHANNELS  Comma-separated freq:mode pairs  e.g. 14230000:usb,21335000:usb
#   UBERSDR_PASS      UberSDR bypass password
#   OUTPUT_DIR        Output directory for images and metadata (default: /data)
#   QSSTV_BIN         Path to qsstv binary (default: /usr/local/bin/qsstv)
#   CTY_FILE          Path to CTY.DAT override (default: embedded)
#   WEB_PORT          Port for the web gallery server (default: 6091, 0 = disabled)
#   WEB_TLS           Set to 1 to enable HTTPS with auto-generated self-signed cert
#   RECEIVER_LAT      Receiver latitude for origin map (default: 0.0)
#   RECEIVER_LON      Receiver longitude for origin map (default: 0.0)

set -e

args=""
[ -n "$UBERSDR_URL"   ] && args="$args -url $UBERSDR_URL"
[ -n "$UBERSDR_PASS"  ] && args="$args -pass $UBERSDR_PASS"
[ -n "$OUTPUT_DIR"    ] && args="$args -output-dir $OUTPUT_DIR"
[ -n "$QSSTV_BIN"     ] && args="$args -qsstv $QSSTV_BIN"
[ -n "$CTY_FILE"      ] && args="$args -cty-file $CTY_FILE"
[ -n "$WEB_PORT"      ] && args="$args -web-port $WEB_PORT"
[ -n "$RECEIVER_LAT"  ] && args="$args -receiver-lat $RECEIVER_LAT"
[ -n "$RECEIVER_LON"  ] && args="$args -receiver-lon $RECEIVER_LON"
[ "$WEB_TLS" = "1"    ] && args="$args -tls"

# UBERSDR_CHANNELS is a comma-separated list; expand each entry as a -channel flag
if [ -n "$UBERSDR_CHANNELS" ]; then
    # shellcheck disable=SC2086
    old_ifs="$IFS"
    IFS=","
    for ch in $UBERSDR_CHANNELS; do
        ch="$(echo "$ch" | tr -d ' ')"
        [ -n "$ch" ] && args="$args -channel $ch"
    done
    IFS="$old_ifs"
fi

# Append any CLI args passed directly to the container
# shellcheck disable=SC2086
exec /usr/local/bin/ubersdr_qsstv $args "$@"
