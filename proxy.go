package main

/*
 * TODO: Make server listener an http server
 * TODO: Allow get requests fora list of the current servers connected
 * Possibly remove some logs of errors that naturally occur (like EOF on conns)
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

var (
  ip string
  port string
  internalIP string = "localhost"
  internalPort string = "9999"
  conns sync.Map
  logger *log.Logger
)

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

  go listenServer()

  go ping()

  ln, err := net.Listen("tcp", ip + ":" + port)
  if err != nil {
    panic(err)
  }
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
  defer serverConn.Close()
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
    route := string(u.Path[:l])
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

func listenServer() {
  ln, err := net.Listen("tcp", internalIP + ":" + internalPort)
  if err != nil {
    panic(err)
  }
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

// ping sends GET requests to each conn every minute to make sure they're alive
func ping() {
  timer := time.AfterFunc(time.Minute, func() {
    // Iterate over each connection
    conns.Range(func(iRoute, iHost interface{}) bool {
      _, err := http.Get(fmt.Sprintf("http://%s", iHost.(string)))
      // An error will be thrown if the request fails
      if err != nil {
        // If the connection was refused, the connection is closed
        if strings.Contains(err.Error(), "refused") {
          conns.Delete(iRoute)
        }
      }
      return true
    })
  })
  for {
    <-timer.C
    timer.Reset(time.Minute)
  }
}
