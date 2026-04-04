.PHONY: all build build-frontend build-backend clean dev

all: build

build: build-frontend build-backend

build-frontend:
	cd web/channel && npm install && npm run build

build-backend:
	go build -o boxd ./cmd/boxd
	go build -o boxctl ./cmd/boxctl

clean:
	rm -f boxd boxctl
	rm -rf web/channel/dist cmd/boxd/static

dev-frontend:
	cd web/channel && npm run dev

dev-backend:
	go run ./cmd/boxd --config configs/config.yaml

init-db:
	go run ./cmd/boxctl init-db
