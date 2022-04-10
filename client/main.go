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
  flag.StringVar(&srvr.Addr, "addr", "", "Addr of the server (include proto)")
  flag.BoolVar(&srvr.Hidden, "hidden", false, "Whether the server is hidden or not")
  flag.StringVar(&server, "server", "127.0.01:8000", "Addr of the server to send to")
  flag.BoolVar(&del, "del", false, "Send delete request")
  flag.Parse()

  if srvr.Name == "" || srvr.Path == "" || srvr.Addr == "" {
    log.Fatal("must provide name, path, and addr")
  }
  // Encode the server
  b := bytes.NewBuffer(nil)
  json.NewEncoder(b).Encode(srvr)
  // Create the request
  var method string
  if !del {
    method = http.MethodPost
  } else {
    method = http.MethodDelete
  }
  req, err := http.NewRequest(method, server, b)
  if err != nil {
    log.Fatal(err)
  }
  // Sendn the request and get the response
  resp, err := http.DefaultClient.Do(req)
  if err != nil {
    log.Fatal(err)
  }
  // Check the response
  if resp.StatusCode != http.StatusOK {
    log.Fatalf("received non-OK status: %s", resp.Status)
  }
}

type Server struct {
  Name string `json:"name"`
  Path string `json:"path"`
  Addr string `json:"addr"`
}
