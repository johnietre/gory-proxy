package main

import (
  "net/http"
)

func startProxy(config *Config) {
  s := &http.Server{
    Addr: config.ProxyAddr,
    Handler: routes(),
    ErrorLog: serverLogger,
  }
  serverLogger.Fatal(s.ListenAndServe())
}

func routes() *http.ServeMux {
  r := http.NewServeMux()
  r.HandleFunc("/", handler)
  r.HandleFunc("/admin", adminHandler)
  return r
}

var pathRegex = regexp.MustCompile(`/([\w\.]+)`)

func handler(w http.ResponseWriter, r *http.Request) {
  path := "/"
  if subs := pathRegex.FindStringSubmatch(r.URL.Path); len(subs) != 0 {
    path = subs[0]
  }
  if sc := serverConns.Load(path); sc != nil {
    u, _ := "", ""
    
  } else if path != "/" {
    
  }
}

func adminHandler(w http.ResponseWriter, r *http.Request) {
  w.Write(byte[]("admin"))
}
