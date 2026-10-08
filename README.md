# cztl

`cztl` is a small CLI for deploying and managing OCI images through the gNOI
ContainerZ service. It supports image upload, container lifecycle, logs,
runtime options, and volume lifecycle. The node-exporter walkthrough below is
an example workload; the CLI is not tied to it.

ContainerZ requires a supported physical SR Linux platform and release. It is
not available in SRL-SIM or the public containerlab SR Linux image.

## Prerequisites

- Go 1.25 or later
- a physical SR Linux node with a TLS gRPC server serving `gnoi.containerz`
- SR Linux AAA credentials and the server CA certificate
- network and CPM-filter policy permitting the workload's traffic
- for the example: ContainerZ local bind-volume support and an external
  Prometheus instance configured to scrape the SR Linux address on TCP/9100

## Install

GitHub releases provide standalone Linux binaries and `.deb`, `.rpm`, and
`.apk` packages for amd64 and arm64. Install the package for your system, or
place the standalone `cztl` binary somewhere in `PATH`.

For development:

```bash
go build -trimpath -o build/cztl ./cmd/containerzctl
install -m 0755 build/cztl ~/.local/bin/cztl
```

Development binaries, downloaded images, and other generated objects live
under `build/`. Release snapshots are written to `dist/`.

Configure the connection:

```bash
cp env.example .env
$EDITOR .env
source .env
```

Connection settings come from `CONTAINERZ_*` environment variables or global
flags. Run `cztl --help` and `cztl <command> --help` for the full interface.

## Node-exporter demo

Download and validate the pinned upstream image without modifying it:

```bash
mkdir -p build
go run github.com/google/go-containerregistry/cmd/crane@v0.22.1 \
  pull quay.io/prometheus/node-exporter:v1.12.1 \
  build/node-exporter.tar --platform linux/amd64
go run github.com/google/go-containerregistry/cmd/crane@v0.22.1 \
  validate --tarball build/node-exporter.tar
```

First verify that the target implements bind-backed volumes:

```bash
cztl create-volume \
  --name node-exporter-compat-check --mountpoint /proc
cztl remove-volume --name node-exporter-compat-check
```

Stop if that check fails. Create read-only host-data sources for the exporter:

```bash
cztl create-volume --name node-exporter-proc --mountpoint /proc
cztl create-volume --name node-exporter-sys --mountpoint /sys
cztl create-volume --name node-exporter-root --mountpoint /
```

Upload and start the image:

```bash
cztl deploy \
  --file build/node-exporter.tar \
  --image node-exporter \
  --tag 1.12.1

cztl start \
  --image node-exporter \
  --tag 1.12.1 \
  --instance node-exporter \
  --network host \
  --restart always \
  --run-as 0:0 \
  --cpus 0.25 \
  --soft-memory 134217728 \
  --hard-memory 268435456 \
  --label example=node-exporter \
  --volume node-exporter-proc:/host/proc:ro \
  --volume node-exporter-sys:/host/sys:ro \
  --volume node-exporter-root:/host/root:ro \
  --command '--path.procfs=/host/proc --path.sysfs=/host/sys --path.rootfs=/host/root --web.listen-address=:9100'
```

Node-exporter configures collectors with CLI flags rather than a general
configuration file. The example keeps upstream default collectors enabled.
ContainerZ does not expose host PID mode, so the example does not enable the
systemd collector.

Permit inbound TCP/9100 if the CPM filter requires an explicit rule:

```bash
scripts/setup-cpm-acl.sh --target "$CONTAINERZ_ADDRESS"
```

Point the external Prometheus scrape configuration at the SR Linux address on
port 9100, then verify:

```bash
curl -fsS http://SRL_SCRAPE_ADDRESS:9100/metrics | grep -m 10 '^node_'
cztl list
cztl logs --instance node-exporter
```

Cleanup:

```bash
cztl cleanup \
  --instance node-exporter --image node-exporter --tag 1.12.1
cztl remove-volume --name node-exporter-proc --force
cztl remove-volume --name node-exporter-sys --force
cztl remove-volume --name node-exporter-root --force
```

## Using another image

Pull any compatible `linux/amd64` image into a Docker-compatible archive, then
use explicit image and instance names:

```bash
cztl deploy --file build/app.tar --image app --tag 1.0.0
cztl start \
  --image app --tag 1.0.0 --instance app \
  --restart always --env KEY=value --command '--flag value'
```

Volumes, ports, environment values, labels, devices, and capabilities are
repeatable flags. The tool applies no workload-specific labels or resource
limits.

## Make shortcuts

The Makefile is optional. `make all` tests and builds `build/cztl`.
`make demo` also downloads and validates node-exporter. The remaining example
targets are `demo-volume-check`, `demo-volumes`, `demo-deploy`, `demo-start`,
`demo-list`, `demo-logs`, and `demo-cleanup`. Run `make release-check` or
`make release-snapshot` for release work; pushing a `v*` tag publishes the
release through GitHub Actions.
