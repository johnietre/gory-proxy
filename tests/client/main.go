package main

import (
  "bytes"
  "encoding/json"
  "flag"
  "log"
  "net/http"
)

func main() {
  srvr := Server{}
  log.SetFlags(0)
  flag.StringVar(&srvr.Name, "name", "", "Name of the server")
  flag.StringVar(&srvr.Path, "path", "", "Path of the server")
  flag.StringVar(&srvr.Addr, "addr", "", "Addr of the server")
  flag.Parse()

  if srvr.Name == "" || srvr.Path == "" || srvr.Addr == "" {
    log.Fatal("must provide name, path, and addr")
  }
  b := bytes.NewBuffer(nil)
  json.NewEncoder(b).Encode(srvr)
  resp, err := http.Post("http://127.0.0.1:8000/tunnel", "application/json", b)
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
