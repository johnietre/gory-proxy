package main

import (
  "flag"
  "net/http"
  "os"
)

func main() {
  addr := flag.String("addr", "localhost:8000", "Address to run server on")
  flag.Parse()
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    r.Write(os.Stdout)
    println()
    w.Write([]byte("Served from " + *addr))
  })
  panic(http.ListenAndServe(*addr, nil))
}
