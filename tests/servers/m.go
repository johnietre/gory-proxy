package main

import (
  "flag"
  "net/http"
)

func main() {
  addr := flag.String("addr", "127.0.0.1:8010", "Address to run on")
  flag.Parse()
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    println(r.URL.String())
    w.Write([]byte("Hello, from Tunnel"))
  })
  panic(http.ListenAndServe(*addr, nil))
}
