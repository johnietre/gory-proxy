package main

import (
  "flag"
  "log"
  "net/http"
  "os"
  "sync"

  webs "golang.org/x/net/websocket"
)

var (
  logger = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)
  addr string
)

func init() {
  flag.StringVar(&addr, "addr", "localhost:10000", "Address to run proxy on")
  flag.Parse()
}

func main() {
  s := &http.Server{
    Addr: addr,
    Handler: routes(),
    ErrorLog: logger,
  }
  logger.Printf("running server on %s", addr)
  logger.Fatal(s.ListenAndServe())
}

func routes() *http.ServeMux {
  r := http.NewServeMux()
  r.HandleFunc("/", handler)
  r.Handle("/ws", webs.Handler(wsHandler))
  return r
}

func handler(w http.ResponseWriter, r *http.Request) {
  logger.Println("home => " + r.URL.String())
  w.WriteHeader(200)
}

func wsHandler(ws *webs.Conn) {
  defer ws.Close()
  logger.Println("ws")
}

type Server struct {
  Name string `json:"name"`
  addr string
  connectFailed bool
  proxy bool
}

type Servers sync.Map

func (s *Servers) ToMap() map[string]Server {
  servers := make(map[string]Server)
  s.Range(func(iName, iServer interface{}) bool {
    servers[iName.(string)] = *(iServer.(*Server))
    return true
  })
  return servers
}
