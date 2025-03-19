bin/client: client/main.go
	go build -o bin/client ./client

.PHONY: bin/gory-proxy
bin/gory-proxy:
	go build -o bin/gory-proxy ./cmd/gory-proxy

client: bin/client
gory-proxy: bin/gory-proxy
