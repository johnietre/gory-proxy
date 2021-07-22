package main

import (
	"bufio"
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
	ln, err := net.Listen("tcp", ip+":"+port)
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
	defer webConn.Close()
	/* TODO: Figure out how best to handle requests over 5000 bytes */
	var serverConn net.Conn
	{
		/* TODO: Set read deadline for webConn */
		/* IDEA: Add date to responses */
		/* IDEA: Create templates for responses */
		req, err := http.ReadRequest(bufio.NewReader(webConn))
		if err != nil {
			/* TODO: Handle specific errors */
			webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
			logger.Println(err)
			return
		}
		var path string
		if matches := pathRegex.FindStringSubmatch(req.URL.Path); matches == nil {
			webConn.Write([]byte("HTTP/1.1 400 Bad Request\r\n"))
			return
		} else {
			path = matches[1]
		}
		if path == "/" {
			servePage(webConn, (req.URL.Query().Get("all") == "1"))
			return
		} else if path == "/favicon.ico" {
			/* TODO: Favicon */
			if _, err = fmt.Fprintf(webConn, "HTTP/1.1 404 Not Found\r\n"); err != nil {
				logger.Println(err)
			}
			return
		} else if sc := serverConns.load(path); sc == nil {
			webConn.Write([]byte("HTTP/1.1 404 Not Found\r\n"))
			return
		} else if serverConn, err = net.Dial("tcp", sc.addr); err != nil {
			webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
			logger.Println(err)
			return
		} else if err = req.Write(serverConn); err != nil {
			webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
			logger.Println(err)
			return
		} else if req.Header["Upgrade"] == nil || req.Header["Upgrade"][0] != "websocket" {
			if resp, err := http.ReadResponse(bufio.NewReader(serverConn), req); err != nil {
				webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
				logger.Println(err)
			} else {
				if err = resp.Write(webConn); err != nil {
					webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
					logger.Println(err)
				}
			}
			serverConn.Close()
			return
		}
	}
	// Handle websocket message passing
	/* TODO: Figure out how to send error messages properly */
	defer serverConn.Close()
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
}

var (
	// Responder needs to supply date, pageChecker supplies the content length and content
	pageResponseTemplate = "HTTP/1.1 200 OK\r\nDate:%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\n\r\n"
)
