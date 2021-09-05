package main

import (
  "log"
  "os"
)

func main() {
  logger := log.New(os.Stderr, "", 0)
  proxy := &Proxy{
    Addr: "127.0.0.1:8000",
    ErrorLog: logger,
  }
  log.Fatal(proxy.ListenAndServe())
}
