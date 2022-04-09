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
  flag.StringVar(&srvr.Name, "name", "Main2", "Name of the server")
  flag.StringVar(&srvr.Path, "path", "main2", "Path of the server")
  flag.StringVar(&srvr.Addr, "addr", "http://127.0.0.1:8012", "Addr of the server")
  flag.Parse()

  if srvr.Name == "" || srvr.Path == "" || srvr.Addr == "" {
    log.Fatal("must provide name, path, and addr")
  }
  b := bytes.NewBuffer(nil)
  json.NewEncoder(b).Encode(srvr)
  resp, err := http.Post("http://127.0.0.1:8080", "application/json", b)
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
