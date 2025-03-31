package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"log"
	"net/http"
)

var server string
var del bool

func main() {
	srvr := Server{}
	log.SetFlags(0)
	flag.StringVar(&srvr.Name, "name", "", "Name of the server")
	flag.StringVar(&srvr.Path, "path", "", "Path of the server")
	flag.StringVar(&srvr.Addr, "addr", "", "Addr of the server")
	flag.StringVar(&server, "server", "127.0.01:8000", "Addr of the server to send to")
	flag.BoolVar(&del, "del", false, "Send delete request")
	flag.Parse()

	if srvr.Name == "" || srvr.Path == "" || srvr.Addr == "" {
		log.Fatal("must provide name, path, and addr")
	}
	b := bytes.NewBuffer(nil)
	json.NewEncoder(b).Encode(srvr)
	var method string
	if !del {
		method = http.MethodPost
	} else {
		method = http.MethodDelete
	}
	req, err := http.NewRequest(method, "http://"+server, b)
	if err != nil {
		log.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("received non-OK status: %s", resp.Status)
	}
}

type Server struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Addr string `json:"addr"`
}
