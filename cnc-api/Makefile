# CNC — Distributed Shell Task Cluster
# ──────────────────────────────────────
# Targets:
#   build         Build server, worker, and CLI for the current OS
#   build-linux   Cross-compile for Linux amd64 (CGO disabled)
#   run           Build and run server with auto-restart enabled
#   clean         Remove built binaries

LDFLAGS := -s -w
GO      := go

.PHONY: build build-linux run clean

# ── Local build ──────────────────────────────────────────────────────────────
build:
	@mkdir -p tools
	$(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o cnc-server ./cmd/server
	$(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o cnc-worker ./cmd/worker
	$(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o cnc       ./cmd/cnc
	(cd ../cnc-backend/cms-scan && $(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o $(CURDIR)/tools/cms-scan .)
	(cd ../cnc-backend/reverseip/src1 && $(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o $(CURDIR)/tools/reverseip-domain .)
	(cd ../cnc-backend/reverseip/src2 && $(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o $(CURDIR)/tools/reverseip-thc .)
	(cd ../cnc-backend/wp-bruter && $(GO) build $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o $(CURDIR)/tools/wp-bruter .)
	@chmod +x $(CURDIR)/tools/dork2 2>/dev/null || true
	@echo "Built: cnc-server  cnc-worker  cnc  tools/cms-scan  tools/reverseip-domain  tools/reverseip-thc  tools/wp-bruter  tools/dork2"

# ── Linux cross-compile ──────────────────────────────────────────────────────
build-linux:
	@mkdir -p tools
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build -ldflags "$(LDFLAGS)" -o cnc-server-linux ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build -ldflags "$(LDFLAGS)" -o cnc-worker-linux ./cmd/worker
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build -ldflags "$(LDFLAGS)" -o cnc-linux        ./cmd/cnc
	(cd ../cnc-backend/cms-scan && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(CURDIR)/tools/cms-scan-linux .)
	(cd ../cnc-backend/reverseip/src1 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(CURDIR)/tools/reverseip-domain-linux .)
	(cd ../cnc-backend/reverseip/src2 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(CURDIR)/tools/reverseip-thc-linux .)
	(cd ../cnc-backend/wp-bruter && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(CURDIR)/tools/wp-bruter-linux .)
	@chmod +x $(CURDIR)/tools/dork2 2>/dev/null || true
	@echo "Built: cnc-server-linux  cnc-worker-linux  cnc-linux  tools/cms-scan-linux  tools/reverseip-domain-linux  tools/reverseip-thc-linux  tools/wp-bruter-linux  tools/dork2"

# ── Run with auto-restart ────────────────────────────────────────────────────
run: build
	@echo "Starting server with auto-restart..."
	@./run-server.sh

# ── Clean ────────────────────────────────────────────────────────────────────
clean:
	rm -f cnc-server cnc-worker cnc tools/cms-scan tools/reverseip-domain tools/reverseip-thc tools/wp-bruter
	rm -f cnc-server-linux cnc-worker-linux cnc-linux tools/cms-scan-linux tools/reverseip-domain-linux tools/reverseip-thc-linux tools/wp-bruter-linux
	@echo "Cleaned"
