package main

import (
  "flag"
  "net/http"
  webs "golang.org/x/net/websocket"
)

func main() {
  var addr string
  flag.StringVar(&addr, "addr", ":8000", "Address to run on")
  flag.Parse()
  println("running on " + addr)
  panic(http.ListenAndServe(addr, webs.Handler(handler)))
}

var (
  recv = webs.Message.Receive
  send = webs.Message.Send
)

func handler(ws *webs.Conn) {
  defer ws.Close()
  var msg string
  for err := recv(ws, &msg); err == nil; err = recv(ws, &msg) {
    send(ws, reverse(msg))
  }
}

func reverse(s string) string {
  b := []byte(s)
  for i, l := 0, len(b); i < l / 2; i++ {
    b[i], b[l-i-1] = b[l-i-1], b[i]
  }
  return string(b)
}
