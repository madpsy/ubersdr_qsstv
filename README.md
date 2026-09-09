# ubersdr_qsstv

Automated SSTV receiver for [UberSDR](https://ubersdr.org) — connects to a remote UberSDR instance, tunes one or more HF channels, pipes the demodulated audio through a headless build of QSSTV, and serves decoded images in a live web gallery.

## Credits

The SSTV decoding engine is built on **QSSTV** by Jean-Paul Roubelat, ON4QZ.  
Original source: [https://github.com/ON4QZ/QSSTV](https://github.com/ON4QZ/QSSTV)

---

## How it works

```
UberSDR (remote SDR) ──► ubersdr_qsstv (Go) ──► qsstv-headless (C++) ──► decoded images
                                │
                                └──► web gallery  http://<host>:6091
```

- **`ubersdr_qsstv`** — Go service that connects to UberSDR via WebSocket, streams demodulated audio, and spawns a `qsstv-headless` process per channel
- **`qsstv-headless`** — headless build of QSSTV that reads raw PCM audio from stdin and writes decoded SSTV images to disk
- **Web gallery** — live image gallery with SNR metadata, decode metrics, and an origin map; served on port 6091

---

## Quick start (Docker — recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/madpsy/ubersdr_qsstv/master/install.sh | bash
```

This will:
1. Create `~/ubersdr/qsstv/` and download `docker-compose.yml` + helper scripts
2. Create the `sstv-images/` output directory
3. Pull the latest `madpsy/ubersdr_qsstv` image
4. Start the service

Then edit `~/ubersdr/qsstv/docker-compose.yml` to set your UberSDR URL and channels, and run `./restart.sh`.

---

## Configuration

All configuration is via environment variables in `docker-compose.yml`:

| Variable | Default | Description |
|----------|---------|-------------|
| `UBERSDR_URL` | `http://ubersdr:8080` | UberSDR base URL |
| `UBERSDR_CHANNELS` | `14230000:usb` | Comma-separated `freq:mode` pairs, e.g. `14230000:usb,21335000:usb`. Each is decoded concurrently — see [Multiple channels](#multiple-channels) |
| `UBERSDR_PASS` | _(empty)_ | UberSDR bypass password |
| `OUTPUT_DIR` | `/data` | Output directory for images inside the container |
| `WEB_PORT` | `6091` | Web gallery port (set to `0` to disable) |
| `WEB_TLS` | `0` | Set to `1` to enable HTTPS with a self-signed cert |
| `RECEIVER_LAT` | `0.0` | Receiver latitude for the origin map |
| `RECEIVER_LON` | `0.0` | Receiver longitude for the origin map |
| `CTY_FILE` | _(embedded)_ | Path to a custom `CTY.DAT` for callsign geo-lookup |
| `UI_PASSWORD` | _(empty)_ | Password for write actions in the web UI; empty disables them |
| `CLEANUP_PARTIAL_DAYS` | `1` | Delete images with <95% of lines decoded after N days; `0` disables |
| `CLEANUP_SNR_DAYS` | `7` | Delete low-SNR images after N days; `0` disables |
| `CLEANUP_ALL_DAYS` | `30` | Delete all images after N days regardless of quality; `0` disables |
| `RAIL_THUMB_INTERVAL_MS` | `2000` | Min interval between channel-rail thumbnails, per channel |
| `RAIL_THUMB_WIDTH` | `160` | Channel-rail thumbnail width in pixels |
| `RAIL_THUMB_QUALITY` | `60` | Channel-rail thumbnail JPEG quality (1–100) |

### Supported modes

Any mode supported by UberSDR's audio demodulator: `usb`, `lsb`, `am`, `fm`, etc.

---

## Multiple channels

List as many `freq:mode` pairs as you want and each is received and decoded
concurrently, by its own decoder process:

```yaml
UBERSDR_CHANNELS: "14230000:usb,21335000:usb,7171000:lsb"
```

Frequencies are fixed at startup — change them by editing the config and running
`./restart.sh`. Duplicate `freq:mode` pairs are rejected at startup, since a
channel is identified by that pair.

With two or more channels the web UI shows a **channel rail**: one row per
channel with a live thumbnail of whatever it is decoding right now, signal
quality, and reception progress. Click a row to focus that channel — the live
preview, waterfall and audio follow your selection. Decoded images from every
channel land in the same gallery, tagged with the frequency they came from.

Audio is single-select: you can only listen to one channel at a time, and the
rail shows which one is audible.

### What each channel costs

Every channel is a separate audio session on the UberSDR receiver plus its own
decoder process, so a handful of channels is comfortable on typical hardware.
The practical ceiling is usually upstream rather than local: a public UberSDR
may cap concurrent sessions per client, in which case the extra channels will
report a connection failure with the receiver's reason. `UBERSDR_PASS` exists to
bypass that where you are permitted to.

Rail thumbnails are generated only while a browser is actually watching, and are
skipped entirely when nobody is — an unattended receiver does no thumbnail work
at all.

---

## Helper scripts

After running `install.sh`, the following scripts are available in `~/ubersdr/qsstv/`:

| Script | Action |
|--------|--------|
| `./start.sh` | Start the service |
| `./stop.sh` | Stop the service |
| `./restart.sh` | Restart the service (apply config changes) |
| `./update.sh` | Pull the latest image and restart |

---

## Building from source

### Docker image

```bash
./docker.sh build          # build madpsy/ubersdr_qsstv:latest
./docker.sh push           # build and push to Docker Hub
./docker.sh run            # run locally (uses env vars)
```

Override the image name:
```bash
IMAGE=myrepo/ubersdr_qsstv:dev ./docker.sh build
```

### Local headless build (no Docker)

Requires: `build-essential`, `qt5-qmake`, `qtbase5-dev`, `libfftw3-dev`, `libopenjp2-7-dev`

```bash
./build.sh
# Binary: ./build-headless/qsstv-headless
```

### Go service

Requires Go 1.25+

```bash
cd ubersdr_qsstv
go build -o ubersdr_qsstv ./...
```

---

## Web gallery

Open `http://<host>:6091` in a browser to view:

- Live decoded SSTV images with frequency, mode, and SNR
- Decode metrics (hourly / daily / weekly / monthly)
- Origin map showing transmitter locations (requires `RECEIVER_LAT`/`RECEIVER_LON`)

---

## Volumes

| Path (container) | Description |
|-----------------|-------------|
| `/data` | Decoded images and JSON sidecar metadata |

Mapped to `./sstv-images` on the host by default (created by `install.sh`).

---

## Ports

| Port | Description |
|------|-------------|
| `6091` | Web gallery (HTTP, or HTTPS if `WEB_TLS=1`) |

---

## License

The QSSTV source code is licensed under the GNU General Public License v3.  
See [COPYING](COPYING) and [LICENSE](LICENSE) for details.
