# CNC — Distributed Shell Task Cluster
# ──────────────────────────────────────
# Targets:
#   build         Build server, worker, and CLI for the current OS
#   build-linux   Cross-compile for Linux amd64 (CGO disabled)
#   clean         Remove built binaries

LDFLAGS := -s -w
GO      := go

.PHONY: build build-linux clean

# ── Local build ──────────────────────────────────────────────────────────────
build:
	$(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o cnc-server ./cmd/server
	$(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o cnc-worker ./cmd/worker
	$(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o cnc       ./cmd/cnc
	@echo "Built: cnc-server  cnc-worker  cnc"

# ── Linux cross-compile ──────────────────────────────────────────────────────
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build -ldflags "$(LDFLAGS)" -o cnc-server-linux ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build -ldflags "$(LDFLAGS)" -o cnc-worker-linux ./cmd/worker
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build -ldflags "$(LDFLAGS)" -o cnc-linux        ./cmd/cnc
	@echo "Built: cnc-server-linux  cnc-worker-linux  cnc-linux"

# ── Clean ────────────────────────────────────────────────────────────────────
clean:
	rm -f cnc-server cnc-worker cnc
	rm -f cnc-server-linux cnc-worker-linux cnc-linux
	@echo "Cleaned"
