package main

/* TODO
 * Make server listener an http server
 * Allow get requests fora list of the current servers connected
 * Possibly remove some logs of errors that naturally occur (like EOF on conns)
 */

/* Notes
 * Servers connecting to proxy must have their base route as the route the send
 */

import (
  "bufio"
  "bytes"
  "fmt"
  "log"
  "net"
  "net/http"
  "os"
  "strings"
  "sync"
  "time"
)

type Conn struct {
  host string
  // Disconnect is set to true if the server fails to respond the first time
  // If disconnect is already true, the next failure results in removal
  disconnect bool
}

type ConnMap struct {
  conns map[string]Conn
  sync.RWMutex
}

func (cm *ConnMap) Load(k string) (v string, ok bool) {
  cm.RLock()
  defer cm.RUnlock()
  v, ok = cm.conns[k]
  return
}

func (cm *ConnMap) Store(k, v string) bool {
  cm.Lock()
  defer cm.Unlock()
  if _, ok := cm.conns[k]; ok {
    return false
  }
  cm.conns[k] = v
  return true
}

func (cm *ConnMap) Delete(k string) {
  cm.Lock()
  defer cm.Unlock()
  delete(cm.conns[k])
}

var (
  ip string = "192.168.1.137"
  port string = "443"
  internalIP string = "localhost"
  internalPort string = "9999"
  conns ConnMap
  logger *log.Logger
)

var debug bool = true

func debugLog(msg string) {
  if debug {
    log.Println(msg)
  }
}

func main() {
  logger = log.New(os.Stdout, "Proxy: ", log.LstdFlags)

  if ip == "" {
    ip = os.Getenv("IP")
    if ip == "" {
      ip = "localhost"
    }
  }
  if port == "" {
    port = os.Getenv("PORT")
    if port == "" {
      port = "8000"
    }
  }
  conns = ConnMap{
    conns: make(map[string]Conn),
  }

  go listenServer()

  go ping()

  ln, err := net.Listen("tcp", ip + ":" + port)
  if err != nil {
    panic(err)
  }
  logger.Printf("Listening to web on %s:%s\n", ip, port)
  for {
    conn, err := ln.Accept()
    if err != nil {
      logger.Println(err)
      continue
    }
    go handle(conn)
  }
}

func handle(webConn net.Conn) {
  defer webConn.Close()
  var bmsg [5000]byte
  l, err := webConn.Read(bmsg[:])
  if err != nil {
    logger.Println(err)
    return
  }
  var serverConn net.Conn
  // defer serverConn.Close()
  // Use block so that everything below isn't kept if the conn is kept alive
  {
    // Parse the request
    reader := bytes.NewReader(bmsg[:l])
    req, err := http.ReadRequest(bufio.NewReader(reader))
    if err != nil {
      logger.Println(err)
      return
    }
    // Get the first slug
    // Example: google.com/images/image yields "images"
    u := req.URL
    lp, i := len(u.Path), 0
    for firstSlash := u.Path[0] == '/'; i < lp; i++ {
      if u.Path[i] == '/' {
        if !firstSlash {
          break
        }
        firstSlash = false
      }
    }
    route := string(u.Path[:i])
    if route[0] != '/' {
      route = "/" + route
    }
    if route[len(route)-1] != '/' {
      route += "/"
    }
    // Find the host that matches the route, if any
    iHost, ok := conns.Load(route)
    if !ok {
      return
    }
    host := iHost.(string)
    serverConn, err = net.Dial("tcp", host)
    if err != nil {
      logger.Println(err)
      return
    }
    defer serverConn.Close()
    if _, err := serverConn.Write(bmsg[:l]); err != nil {
      logger.Println(err)
      return
    }
    if req.Header["Upgrade"] == nil || req.Header["Upgrade"][0] != "websocket" {
      if l, err = serverConn.Read(bmsg[:]); err != nil {
        logger.Println(err)
      }
      webConn.Write(bmsg[:l])
      return
    }
  }
  // Take messages from both the server and the web connections
  for {
    // Set deadlines so the read won't block forever
    if err = serverConn.SetReadDeadline(time.Now().Add(time.Microsecond)); err != nil {
      logger.Println(err)
      return
    } else if l, err = serverConn.Read(bmsg[:]); err != nil {
      // If the error was from the deadline, ignore it
      if !strings.HasSuffix(err.Error(), "timeout") {
        logger.Println(err)
        return
      }
    } else {
      // There were no errors, this block will be reached and the message sent
      if _, err = webConn.Write(bmsg[:l]); err != nil {
        logger.Println(err)
        return
      }
    }
    // Do the same thing for the web conn
    if err = webConn.SetReadDeadline(time.Now().Add(time.Microsecond)); err != nil {
      logger.Println(err)
      return
    } else if l, err = webConn.Read(bmsg[:]); err != nil {
      if !strings.HasSuffix(err.Error(), "timeout") {
        logger.Println(err)
        return
      }
    } else {
      if _, err = serverConn.Write(bmsg[:l]); err != nil {
        logger.Println(err)
        return
      }
    }
  }
}

/*
func listenServer() {
  ln, err := net.Listen("tcp", internalIP + ":" + internalPort)
  if err != nil {
    panic(err)
  }
  logger.Printf("Listening to servers on %s:%s\n", internalIP, internalPort)
  var bmsg [64]byte
  // convert to http server
  for {
    conn, err := ln.Accept()
    if err != nil {
      logger.Println(err)
      continue
    }
    if l, err := conn.Read(bmsg[:]); err != nil {
      logger.Println(err)
      conn.Write([]byte("error"))
    } else {
      parts := strings.Split(string(bmsg[:l]), "\t")
      // Host received should be just the host, ex., localhost:8000
      // Route received should be the slug enclosed in "/", ex., /home/
      host, route := parts[0], parts[1]
      // TODO
      println(host, route)
    }
    conn.Close()
  }
}
*/

func listenServer() {
  server := &http.Server{
    Addr: internalIP + ":" + internalPort,
    Handler: http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
      if r.Method == http.MethodGet {
        //
        return
      }
      host, route := r.FormValue("host"), r.FormValue("route")
    }),
  }
}

// ping sends GET requests to each conn every minute to make sure they're alive
func ping() {
  timer := time.AfterFunc(time.Minute, func() {
    for route, conn := range conns.conns {
      _, err := http.Get(host)
      if err != nil {
        if strings.Contains(err.Error(), "refused") {
          if conn.disconnect {
            conns.Delete(route)
          } else {
            conn.disconnect = true
          }
        } else {
          conn.disconnect = false
        }
      } else {
        conn.disconnect = false
      }
    }
  })
  <-timer.C
  timer.Reset(time.Minute)
}

