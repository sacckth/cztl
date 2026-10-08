NODE_EXPORTER_VERSION ?= 1.12.1
UPSTREAM_IMAGE ?= quay.io/prometheus/node-exporter:v$(NODE_EXPORTER_VERSION)
IMAGE ?= node-exporter
INSTANCE ?= node-exporter
BUILD_DIR := build
IMAGE_TAR := $(BUILD_DIR)/$(IMAGE).tar
CZTL := $(BUILD_DIR)/cztl
VOLUME_PREFIX ?= node-exporter
PROC_VOLUME := $(VOLUME_PREFIX)-proc
SYS_VOLUME := $(VOLUME_PREFIX)-sys
ROOT_VOLUME := $(VOLUME_PREFIX)-root
CHECK_VOLUME := $(VOLUME_PREFIX)-compat-check
NODE_EXPORTER_PORT ?= 9100
NODE_EXPORTER_FLAGS ?= --path.procfs=/host/proc --path.sysfs=/host/sys --path.rootfs=/host/root --web.listen-address=:$(NODE_EXPORTER_PORT)
GORELEASER ?= goreleaser

.PHONY: all test build demo demo-image demo-verify-image demo-volume-check \
	demo-volumes demo-volumes-clean demo-deploy demo-start demo-list \
	demo-logs demo-cleanup release-check release-snapshot clean

all: test build

test:
	go test ./...

build:
	mkdir -p $(BUILD_DIR)
	go build -trimpath -o $(CZTL) ./cmd/containerzctl

demo: all demo-image demo-verify-image

demo-image:
	mkdir -p $(BUILD_DIR)
	go run github.com/google/go-containerregistry/cmd/crane@v0.22.1 \
		pull $(UPSTREAM_IMAGE) $(IMAGE_TAR) --platform linux/amd64

demo-verify-image: demo-image
	go run github.com/google/go-containerregistry/cmd/crane@v0.22.1 \
		validate --tarball $(IMAGE_TAR)

demo-volume-check: build
	-$(CZTL) remove-volume --name $(CHECK_VOLUME) --force
	$(CZTL) create-volume --name $(CHECK_VOLUME) --mountpoint /proc
	$(CZTL) remove-volume --name $(CHECK_VOLUME)

demo-volumes: build
	$(CZTL) create-volume --name $(PROC_VOLUME) --mountpoint /proc
	$(CZTL) create-volume --name $(SYS_VOLUME) --mountpoint /sys
	$(CZTL) create-volume --name $(ROOT_VOLUME) --mountpoint /

demo-volumes-clean: build
	-$(CZTL) remove-volume --name $(PROC_VOLUME) --force
	-$(CZTL) remove-volume --name $(SYS_VOLUME) --force
	-$(CZTL) remove-volume --name $(ROOT_VOLUME) --force
	-$(CZTL) remove-volume --name $(CHECK_VOLUME) --force

demo-deploy: build demo-image
	$(CZTL) deploy --file $(IMAGE_TAR) --image $(IMAGE) --tag $(NODE_EXPORTER_VERSION)

demo-start: build
	$(CZTL) start --image $(IMAGE) --tag $(NODE_EXPORTER_VERSION) --instance $(INSTANCE) \
		--network host --restart always --run-as 0:0 \
		--cpus 0.25 --soft-memory 134217728 --hard-memory 268435456 \
		--label example=node-exporter \
		--volume $(PROC_VOLUME):/host/proc:ro \
		--volume $(SYS_VOLUME):/host/sys:ro \
		--volume $(ROOT_VOLUME):/host/root:ro \
		--command '$(NODE_EXPORTER_FLAGS)'

demo-list: build
	$(CZTL) list --all

demo-logs: build
	$(CZTL) logs --instance $(INSTANCE)

demo-cleanup: build
	-$(CZTL) cleanup --instance $(INSTANCE) --image $(IMAGE) --tag $(NODE_EXPORTER_VERSION)
	$(MAKE) demo-volumes-clean

release-check:
	$(GORELEASER) check

release-snapshot:
	$(GORELEASER) release --snapshot --clean

clean:
	rm -rf $(BUILD_DIR)
