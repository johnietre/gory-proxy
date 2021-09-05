package main

import (
  "net/http"
  "sync"
)

type ServerMap struct {
  m sync.Map
}

func ServeHTTP(w http.ResponseWriter, r *http.Request) {
  
}
