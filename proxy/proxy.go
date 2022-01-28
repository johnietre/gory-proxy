package main

import (
  "context"
  "errors"
  "log"
  "net"
  "sync"
  "syscall"

  "golang.org/x/sys/unix"
)

// Proxy is a reverse proxy
type Proxy struct {
  Addr net.Addr
  Servers sync.Map
  Proxies sync.Map
  ErrorLog *log.Logger
}

func NewProxy(addr string) (*Proxy, error) {
  return nil, nil
}

func (px *Proxy) Run() error {
  // Create the listener config allowing reuse socket to be used for the
  // listener
  lnConfig := net.ListenConfig{
    Control: func(network, addr string, c syscall.RawConn) error {
      var opErr error
      err := c.Control(func(fd uintptr) {
        opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
      })
      if err != nil {
        return err
      }
      return opErr
    },
  }
  var err error
  // Create the listener
  ln, err := lnConfig.Listen(context.Background(), "tcp", px.Addr.String())
  if err != nil {
    return err
  }
  // Accept and handle client connections
  for {
    conn, err := ln.Accept()
    if err != nil {
      if errors.Is(err, net.ErrClosed) {
        return err
      }
      px.ErrorLog.Println(err)
      continue
    }
    go px.route(conn)
  }
  return nil
}

func (px *Proxy) Shutdown() error {
  return nil
}

func (px *Proxy) route(clientConn net.Conn) {
}
