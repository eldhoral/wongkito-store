.PHONY: dev-api dev-web test build

dev-api:
	cd api && go run ./cmd/server

dev-web:
	npm --prefix web run dev

test:
	cd api && go test ./...
	npm --prefix web test -- --run

build:
	npm --prefix web run build
	rm -rf api/internal/web/dist
	cp -R web/dist api/internal/web/dist
	cd api && go build -o ../bin/wongkito ./cmd/server
