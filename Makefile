.PHONY: help dev start build test seed clean lint

help:
	@echo "Internet Map Developer Commands:"
	@echo "  make dev      - Launch local Next.js dev server with Turbopack & API"
	@echo "  make start    - Launch local production build server & API"
	@echo "  make build    - Build Next.js frontend assets"
	@echo "  make test     - Run full pytest test suite"
	@echo "  make seed     - Seed development catalog with realistic domains & intel"
	@echo "  make clean    - Remove build artifacts, caches, and temporary files"

dev:
	python3 run.py --dev

start:
	python3 run.py

build:
	cd web && npm run build

test:
	pytest -v

seed:
	python3 main.py seed --count 250

clean:
	rm -rf web/.next __pycache__ tests/__pycache__ infra/__pycache__ .pytest_cache data/scanner-state.json
