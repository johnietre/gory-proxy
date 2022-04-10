package main

import (
  "bytes"
  "encoding/json"
  "io/ioutil"
  "flag"
  "log"
  "net/http"
)

func main() {
  srvr := Server{}
  log.SetFlags(0)
  flag.StringVar(&srvr.Name, "name", "Main", "Name of the server")
  flag.StringVar(&srvr.Path, "path", "main", "Path of the server")
  flag.StringVar(&srvr.Addr, "addr", "http://127.0.0.1:8001", "Addr of the server")
  flag.StringVar(&proxyAddr, "proxy", "http://127.0.0.1:8000", "Address of the proxy server")
  flag.Parse()

  if srvr.Name == "" || srvr.Path == "" || srvr.Addr == "" {
    log.Fatal("must provide name, path, and addr")
  }
  b := bytes.NewBuffer(nil)
  json.NewEncoder(b).Encode(srvr)
  resp, err := http.Post(proxyAddr, "application/json", b)
  if err != nil {
    log.Fatal(err)
  }
  if resp.StatusCode != http.StatusOK {
    log.Printf("received non-OK status: %s", resp.Status)
    b, err := ioutil.ReadAll(resp.Body)
    if err == nil {
      log.Fatalf("%s", b)
    }
    resp.Body.Close()
  }
}

type Server struct {
  Name string `json:"name"`
  Path string `json:"path"`
  Addr string `json:"addr"`
}
