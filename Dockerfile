# syntax=docker/dockerfile:1
# ---------------------------------------------------------------------------
# Stage 1: build QSSTV headless binary
# ---------------------------------------------------------------------------
FROM ubuntu:24.04 AS qsstv-builder

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        qt5-qmake \
        qtbase5-dev \
        qtmultimedia5-dev \
        libqt5multimedia5-plugins \
        libfftw3-dev \
        libopenjp2-7-dev \
        libzstd-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY src/ ./src/
COPY build.sh .

RUN bash build.sh

# ---------------------------------------------------------------------------
# Stage 2: build ubersdr_qsstv Go binary
# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS go-builder

WORKDIR /src
COPY ubersdr_qsstv/go.mod ubersdr_qsstv/go.sum ./
RUN go mod download

COPY ubersdr_qsstv/ .
RUN go build -o /out/ubersdr_qsstv ./...

# ---------------------------------------------------------------------------
# Stage 3: runtime image
# ---------------------------------------------------------------------------
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
        libqt5core5a \
        libqt5gui5 \
        libqt5widgets5 \
        libqt5network5 \
        libqt5xml5 \
        libqt5multimedia5 \
        libfftw3-single3 \
        libfftw3-double3 \
        libopenjp2-7 \
        libzstd1 \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -s /bin/false sstv

COPY --from=qsstv-builder /src/build-headless/qsstv-headless /usr/local/bin/qsstv
COPY --from=go-builder    /out/ubersdr_qsstv                  /usr/local/bin/ubersdr_qsstv

# Copy entrypoint script (translates env vars to ubersdr_qsstv flags)
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

# Create the default output directory and ensure the sstv user owns it.
# Users can volume-mount a host directory over /data to persist images on the host.
RUN chmod +x /usr/local/bin/entrypoint.sh \
    && mkdir -p /data \
    && chown sstv:sstv /data

USER sstv

VOLUME ["/data"]

# Expose the web gallery port (default; override with WEB_PORT env var)
EXPOSE 6091

# Verify the binary can print help
HEALTHCHECK --interval=60s --timeout=5s --retries=3 \
    CMD ["/usr/local/bin/ubersdr_qsstv", "-help"] || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
