bin/client: client/main.go
	go build -o bin/client ./client

bin/gory-proxy: main.go sync_map.go
	go build -o bin/gory-proxy .

client: bin/client
gory-proxy: bin/gory-proxy
