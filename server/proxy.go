package main

import (
  "bytes"
  "context"
  "errors"
  "log"
  "net"
  "net/http"
  "os"
  "regexp"
  "sync"
  "syscall"
  "time"

  "golang.org/x/sys/unix"
)

type Route interface {
  Serve(conn net.Conn, req []byte)
}

type Proxy struct {
  Addr string
  ErrorLog *log.Logger
  routes sync.Map
  proxyLn net.Listener
}

func (px *Proxy) ListenAndServe() error {
  if px.ErrorLog == nil {
    px.ErrorLog = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)
  }
  // Create ListenConfig to be used for the listener
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
  px.proxyLn, err = lnConfig.Listen(context.Background(), "tcp", px.Addr)
  if err != nil {
    return err
  }
  go px.run()
  return nil
}

func (px *Proxy) run() {
  for {
    clientConn, err := px.proxyLn.Accept()
    if err != nil {
      if errors.Is(err, net.ErrClosed) {
        return
      }
      /* IDEA: add formatting/extra info to log message */
      px.ErrorLog.Println(err)
      continue
    }
    go px.route(clientConn)
  }
}

func (px *Proxy) Shutdown() error {
  /* TODO: Look at Go stdlib closing functions */
  return px.proxyLn.Close()
}

const (
  bufSize = 1024
  deadline = time.Millisecond
)

var reqRegex = regexp.MustCompile(`^\w+ (/[\w\.-]*)`)

func (px *Proxy) route(clientConn net.Conn) {
  defer clientConn.Close()
  var (
    buf [bufSize]byte
    reqBytes = make([]byte, 0, 1024)
  )
  t := time.Now().Add(deadline)
  // Read the request from the socket in batches
  for {
    clientConn.SetReadDeadline(t)
    l, err := clientConn.Read(buf[:])
    if err != nil {
      if os.IsTimeout(err) {
        break
      }
      px.ErrorLog.Println(err)
      return
    }
    reqBytes = append(reqBytes, buf[:l]...)
    if l < bufSize {
      return
    }
    t = time.Now().Add(deadline)
  }
  // Parse the first line of the reqeust and get the path
  i := bytes.IndexByte(reqBytes, '\r')
  // Invalid request
  if i == -1 {
    /* TODO: Send HTTP error (just cause) */
    return
  }
  var rPath string
  if matches := reqRegex.FindSubmatch(reqBytes[:i]); len(matches) == 0 {
    // Invalid request
    /* TODO: Send HTTP error */
    return
  } else {
    rPath = string(matches[1])
  }

  // Get the associate route and serve the conn
  route, loaded := px.GetRoute(rPath)
  if !loaded {
    /* TODO: Send HTTP error */
    return
  }
  route.Serve(clientConn, reqBytes)
}

func (px *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(200)
}

func (px *Proxy) AddRoute(path string, route Route) (stored bool) {
  _, loaded := px.routes.LoadOrStore(path, route)
  return !loaded
}

func (px *Proxy) GetRoute(path string) (Route, bool) {
  iRoute, loaded := px.routes.Load(path)
  return iRoute.(Route), loaded
}

func (px *Proxy) DeleteRoute(path string) {
  px.routes.Delete(path)
}

type ServerRoute struct {
  ServerAddr string
  //
}

func (sr ServerRoute) Serve(clientConn net.Conn, req []byte) {
  // Connect to the server
  serverConn, err := net.Dial("tcp", sr.ServerAddr)
  if err != nil {
    /* TODO: Log error and send HTTP error */
    return
  }
  defer serverConn.Close()
  // Send the request to the server
  if _, err = serverConn.Write(req); err != nil {
    /* TODO: Log error and send HTTP error */
    return
  }
  req = nil
  var buf [bufSize]byte
  // Read the response from the server
  /* IDEA: Set read deadline */
  l, err := serverConn.Read(buf[:])
  if err != nil {
    /* TODO: Log error and send HTTP error */
    return
  }
  // Send the response from the server in batches
  if _, err = clientConn.Write(buf[:l]); err != nil {
    /* TODO: Log error */
    return
  }
  for l == bufSize {
    serverConn.SetReadDeadline(time.Now().Add(deadline))
    if l, err = serverConn.Read(buf[:]); err != nil {
      /* TODO: Check for EOF? */
      if os.IsTimeout(err) {
        break
      }
      /* TODO: Log error and send HTTP error? or just don't sent anything else */
      return
    }
    if _, err = clientConn.Write(buf[:l]); err != nil {
      /* TODO: Log error */
      return
    }
  }
  // Continue reading from the sockets
  // If it is not intended to be a websocket connection, it will be closed
  readFromConn, writeToConn := clientConn, serverConn
  for {
    for {
      readFromConn.SetReadDeadline(time.Now().Add(deadline))
      if l, err = readFromConn.Read(buf[:]); err != nil {
        if os.IsTimeout(err) {
          break
        } else if !errors.Is(err, net.ErrClosed) {
          /* TODO: Log error and send HTTP error */
        }
        return
      }
      if _, err := writeToConn.Write(buf[:l]); err != nil {
        if !errors.Is(err, net.ErrClosed) {
          /* TODO: Log error and send HTTP error */
        }
        return
      }
      if l != bufSize {
        break
      }
    }
    readFromConn, writeToConn = writeToConn, readFromConn
  }
}
