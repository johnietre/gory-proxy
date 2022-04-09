package main

import (
  "fmt"
  "flag"
  "net/http"
)

func main() {
  addr := flag.String("addr", "", "Address to run on")
  flag.Parse()
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    fmt.Println(r.URL)
    w.Write([]byte("Hello, from " + *addr))
  })
  panic(http.ListenAndServe(*addr, nil))
}
