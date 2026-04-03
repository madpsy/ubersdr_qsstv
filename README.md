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
| `UBERSDR_URL` | `http://172.20.0.1:8080` | UberSDR base URL |
| `UBERSDR_CHANNELS` | `14230000:usb` | Comma-separated `freq:mode` pairs, e.g. `14230000:usb,21335000:usb` |
| `UBERSDR_PASS` | _(empty)_ | UberSDR bypass password |
| `OUTPUT_DIR` | `/data` | Output directory for images inside the container |
| `WEB_PORT` | `6091` | Web gallery port (set to `0` to disable) |
| `WEB_TLS` | `0` | Set to `1` to enable HTTPS with a self-signed cert |
| `RECEIVER_LAT` | `0.0` | Receiver latitude for the origin map |
| `RECEIVER_LON` | `0.0` | Receiver longitude for the origin map |
| `CTY_FILE` | _(embedded)_ | Path to a custom `CTY.DAT` for callsign geo-lookup |

### Supported modes

Any mode supported by UberSDR's audio demodulator: `usb`, `lsb`, `am`, `fm`, etc.

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
