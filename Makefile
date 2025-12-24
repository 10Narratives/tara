build-faas-agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o build/faas-agent  ./cmd/faas-agent/

build: build-faas-agent

start:
	docker compose up --build --detach

stop:
	docker compose down -v --remove-orphans