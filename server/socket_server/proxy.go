package main

import (
	"bufio"
  "bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func init() {
	reader := bufio.NewReader(strings.NewReader(homepageHTML))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		pageResponseTemplate += strings.TrimSpace(line)
	}
}

func startProxy() error {
	ln, err := net.Listen("tcp", config.ProxyAddr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			logger.Println(err)
			continue
		}
		go handle(conn)
	}
	return nil
}

// Doesn't handle EVERY path
var pathRegex = regexp.MustCompile(`^(/\w*)`)

func handle(webConn net.Conn) {
  defer closeConn(webConn)
  // Parse request from the conn
  if err := webConn.SetReadDeadline(time.Now().Add(time.Second * 1)); err != nil {
    webConn.Write([]byte(createResponseFromCode(500, "")))
    logger.Println(err)
    return
  }
  contents, err := io.ReadAll(webConn)
  if err != nil {
    /* TODO: Handle specific errors */
    webConn.Write([]byte(createResponseFromCode(500, "")))
    logger.Println(err)
    return
  }
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(contents)))
	if err != nil {
		/* TODO: Handle specific errors */
		webConn.Write([]byte(createResponseFromCode(500, "")))
		logger.Println(err)
		return
	}
  if req.ProtoMajor == 1 {
    handleHTTP1(webConn, req)
  } else if req.ProtoMajor == 2 {
    handleHTTP2(webConn, contents)
  } else {
    /* TODO: Handle better */
    webConn.Write([]byte(createResponseFromCode(http.StatusBadRequest, "")))
  }
}

func handleHTTP1(webConn net.Conn, req *http.Request) {
  var serverConn net.Conn
  defer closeConn(serverConn)
	{
    // Get the path from the URL of the request
		var path string
		if matches := pathRegex.FindStringSubmatch(req.URL.Path); matches != nil {
      path = matches[1]
    } else {
			webConn.Write([]byte(createResponseFromCode(400, "")))
			return
    }
    // Handle for various paths
    isWebsocket := false
    fmt.Printf("Got req for %s\n", path)
		if path == "/" {
      fmt.Println("Serving page")
			servePage(webConn, (req.URL.Query().Get("all") == "1"))
		} else if path == "/favicon.ico" {
      fmt.Println("serving favicon")
      serveFavicon(webConn)
		} else if sc := serverConns.Load(path); sc == nil {
      // Send error if the path/server doesn't exist
      fmt.Printf("No path %s\n", path)
			webConn.Write([]byte(createResponseFromCode(404, "")))
		} else if serverConn, err := net.Dial("tcp", sc.addr.Host); err != nil {
      // Handle for error in connecting to server
			webConn.Write([]byte(createResponseFromCode(500, "")))
			logger.Println(err)
		} else if err := req.Write(serverConn); err != nil {
      // Handle for error in sending request to server
			webConn.Write([]byte(createResponseFromCode(500, "")))
			logger.Println(err)
		} else if req.Header["Upgrade"] == nil || req.Header["Upgrade"][0] != "websocket" {
      // Send request to the server and send the response to the clinet
      fmt.Printf("Serving for %s\n", path)
			if resp, err := http.ReadResponse(bufio.NewReader(serverConn), req); err != nil {
				webConn.Write([]byte(createResponseFromCode(500, "")))
				logger.Println(err)
			} else {
				if err = resp.Write(webConn); err != nil {
					webConn.Write([]byte(createResponseFromCode(500, "")))
					logger.Println(err)
				}
			}
		} else {
      isWebsocket = true
    }
    if !isWebsocket {
      return
    }
	}
	// Handle websocket message passing
	/* TODO: Figure out how to send error messages properly */
	for {
		if err := serverConn.SetReadDeadline(time.Now().Add(time.Microsecond)); err != nil {
			logger.Println(err)
			return
		} else if l, err := io.Copy(webConn, serverConn); err != nil {
			if !strings.HasSuffix(err.Error(), "timeout") {
				logger.Println(err)
				return
			}
		} else if l == 0 {
			// Length of 0 used here to mean EOF (socket closed)
			// Does not accept 0 length messages
			return
		}
		if err := webConn.SetReadDeadline(time.Now().Add(time.Microsecond)); err != nil {
			logger.Println(err)
			return
		} else if l, err := io.Copy(serverConn, webConn); err != nil {
			if !strings.HasSuffix(err.Error(), "timeout") {
				logger.Println(err)
				return
			}
		} else if l == 0 {
			return
		}
	}
}

func handleHTTP2(webConn net.Conn, reqBytes []byte) {
  return
}

/*
const (
	pageFileName = "index.html"
	// Responder needs to supply date, pageChecker supplies the content length and content
	pageResponseTemplate = "HTTP/1.1 200 OK\r\nDate:%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\n\r\n%s"
)

var (
	pageMut      sync.RWMutex
	pageResponse string
	gmtLoc       = time.FixedZone("GMT", 0)
)

// pageChecker updates the pageResponse if the page file is changed
// TODO: Handle errors better
func pageChecker() {
	f, err := os.Open(pageFileName)
	if err != nil {
		logger.Panic(err)
	}
	if fileBytes, err := ioutil.ReadFile(pageFileName); err != nil {
		logger.Panic(err)
	} else {
		pageResponse = fmt.Sprintf(pageResponseTemplate, "%s", len(fileBytes), fileBytes)
	}
	stat, err := f.Stat()
	if err != nil {
		logger.Panic(err)
	}
	// Last mod time
	l := stat.ModTime().Unix()
	for {
		if stat, err = f.Stat(); err != nil {
			// Possibly close and reopen file
			logger.Println(err)
		} else if t := stat.ModTime().Unix(); t != l {
			pageMut.Lock()
			if fileBytes, err := ioutil.ReadFile(pageFileName); err != nil {
				logger.Println(err)
			} else {
				pageResponse = fmt.Sprintf(pageResponseTemplate, "%s", len(fileBytes), fileBytes)
			}
			pageMut.Unlock()
		}
	}
}
*/

/* IDEA: Allow path (and add arg to function) that allows ALL servers to be printed (even those without a site) */
func servePage(conn net.Conn, all bool) {
  conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: plaintext; charset=utf-8\r\nContent-Length: 17\r\n\r\nServed from Proxy"))
  return
/*
	// There will always be only one "%s" in the pageResponseFile
	linksString := ""
	serverConns.loopThru(func(iPath, iConn interface{}) bool {
		path := iPath.(string)
		server := iConn.(*ServerConn)
		name := server.Name
		if name == "" {
			name = path
		}
		if server.Website || all {
			linksString += fmt.Sprintf("<a href=%s>%s</a><br>", path, name)
		}
		return true
	})
	pageMut.RLock()
	defer pageMut.RUnlock()
	if _, err := fmt.Fprintf(conn, pageResponse, time.Now().In(gmtLoc).Format(time.RFC1123), linksString); err != nil {
		logger.Println(err)
	}
*/
}

/* TODO: Serve favicon */
func serveFavicon(conn net.Conn) {
	if _, err := fmt.Fprintf(conn, createResponseFromCode(404, "")); err != nil {
		logger.Println(err)
	}
}

func createResponseFromCode(code int, msg string) string {
  if msg != "" {
    return fmt.Sprintf("HTTP/1.1 %d %s\r\n\r\n", code, msg)
  }
  return fmt.Sprintf("HTTP/1.1 %d %s\r\n\r\n", code, http.StatusText(code))
}

var (
	// Responder needs to supply date, pageChecker supplies the content length and content
	pageResponseTemplate = "HTTP/1.1 200 OK\r\nDate:%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\n\r\n"
)

func closeConn(conn net.Conn) {
  if conn != nil {
    if err := conn.Close(); err != nil {
      logger.Println(err)
    }
  }
}
