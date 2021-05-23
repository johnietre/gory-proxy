package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	// "io"
)

type smap struct {
	sync.Map
}

func (s *smap) store(conn *ServerConn) {
	s.Store(conn.addr, conn)
}

func (s *smap) load(addr string) *ServerConn {
	ic, exists := s.Load(addr)
	if exists {
		return ic.(*ServerConn)
	}
	return nil
}

func (s *smap) loadOrStore(conn *ServerConn) (*ServerConn, bool) {
	ic, loaded := s.LoadOrStore(conn.addr, conn)
	return ic.(*ServerConn), loaded
}

func (s *smap) loopThru(f func(k, v interface{}) bool) {
	s.Range(f)
}

func (s *smap) delete(addr string) {
	s.Delete(addr)
}

func (s *smap) deleteByPath(path string) {
	s.Range(func(k, v interface{}) bool {
		if v.(*ServerConn).Path == path {
			s.delete(k.(string))
			return false
		}
		return true
	})
}

// ServerConn holds information about servers connected to the proxy
type ServerConn struct {
	// The path (on the proxy) which points to the server
	// Ex) http://localhost:8000/hello => path = hello
	// Ex) http://localhost:8000/hello/world => path = hello
	Path string `json:"path"`
	// The name/title of the connection (website)
	Name string `json:"name,omitempty"`
	// Tells whether the server is for a website or not
	// If not, it won't show on the proxy web page
	Website bool `json:"website"`
	// The network address of the server (no trailing "/")
	// Ex: http://localhost:8000
	addr string
	// Failed tells whether the connection has failed once before
	// If so, the next fail (consecutive fails) will result in the server being
	// disconnected
	failed bool
}

var (
	ip           string = "localhost"
	port         string = "8080"
	internalIP   string = "localhost"
	internalPort string = "8888"
	serverConns  smap
	logger       *log.Logger
)

func main() {
	logger = log.New(os.Stdout, "Proxy: ", log.LstdFlags)

	go listenForServers()
	go pinger()
	ln, err := net.Listen("tcp", ip+":"+port)
	if err != nil {
		logger.Panic(err)
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

// Doesn't handle EVERY path
var pathRegex = regexp.MustCompile(`^(/\w*)`)

func handle(webConn net.Conn) {
	defer webConn.Close()
	/* TODO: Figure out how best to handle requests over 5000 bytes */
	var serverConn net.Conn
	{
		// Manually pass the request and response
		// bmsg, err := io.ReadAll(webConn)
		// // Will not return EOF error
		// if err != nil {
		// 	webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
		// 	logger.Println(err)
		// 	return
		// }
		// req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(bmsg[:])))
		// if err != nil {
		// 	webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
		// 	logger.Println(err)
		// 	return
		// }
		// var path string
		// if matches := pathRegex.FindStringSubmatch(req.URL.Path); matches == nil {
		// 	webConn.Write([]byte("HTTP/1.1 400 Bad Request\r\n"))
		// 	return
		// } else {
		// 	path = matches[1]
		// }
		// if sc := serverConns.load(path); sc == nil {
		// 	webConn.Write([]byte("HTTP/1.1 404 Not Found\r\n"))
		// 	return
		// }
		// if serverConn, err = net.Dial("tcp", ""); err != nil {
		// 	webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
		// 	logger.Println(err)
		// 	return
		// }
		// defer serverConn.Close()
		// if _, err := serverConn.Write(bmsg[:]); err != nil {
		// 	webConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n"))
		// 	logger.Println(err)
		// 	return
		// }
		// if req.Header["Upgrage"] == nil || req.Header["Upgrade"][0] != "websocket" {
		// 	if l, err := serverConn.Read(bmsg[:]); err != nil {
		// 		webConn.Write(byte[]("HTTP/1.1 500 Internal Server Error\r\n"))
		// 		logger.Println(err)
		// 	} else {
		// 		webConn.Write(bmsg[:l])
		// 	}
		// 	return
		// }

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
			servePage(webConn)
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
	/* TODO: Figure out how to send error messages properly */
	// Handle websocket message passing
	defer serverConn.Close()
	const buffCap = 256
	var buff [buffCap]byte
	for {
		for {
			if err := serverConn.SetReadDeadline(time.Now().Add(time.Microsecond)); err != nil {
				logger.Println(err)
				return
			} else if l, err := serverConn.Read(buff[:]); err != nil {
				if strings.HasSuffix(err.Error(), "timeout") {
					break
				} else if !strings.Contains(err.Error(), "EOF") {
					logger.Println(err)
				}
				return
			} else if _, err = webConn.Write(buff[:l]); err != nil {
				logger.Println(err)
				return
			} else if l < buffCap {
				break
			}
		}
		for {
			if err := webConn.SetReadDeadline(time.Now().Add(time.Microsecond)); err != nil {
				logger.Println(err)
				return
			} else if l, err := webConn.Read(buff[:]); err != nil {
				if strings.HasSuffix(err.Error(), "timeout") {
					break
				} else if !strings.Contains(err.Error(), "EOF") {
					logger.Println(err)
				}
				return
			} else if _, err = serverConn.Write(buff[:l]); err != nil {
				logger.Println(err)
				return
			} else if l < buffCap {
				break
			}
		}
	}
}

const pageResponse = `HTTP/1.1 200 OK` + "\r" + `
Content-Type: text/html;` + "\r" + `
<!DOCTYPE HTML>
<html>
<head>
</head>
<body>
	<ul>
		{{range .}}
			{{if .Website}}
				//
			{{end}}
		{{end}}
	</ul>
</body>
</html>
`

func servePage(conn net.Conn) {
	//
}

func listenForServers() {
	/* TODO: Possilby send different codes for failures */
	serverLogger := log.New(os.Stdout, "Proxy Server: ", log.LstdFlags)
	server := &http.Server{
		Addr: internalIP + ":" + internalPort,
		Handler: func() *http.ServeMux {
			r := http.NewServeMux()
			r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				path, name, addr, website := r.FormValue("path"), r.FormValue("name"), r.FormValue("addr"), false
				if r.Method == http.MethodGet {
					// Get list of servers
					var servers []*ServerConn
					serverConns.loopThru(func(k, iConn interface{}) bool {
						servers = append(servers, iConn.(*ServerConn))
						return true
					})
					e := json.NewEncoder(w)
					if err := e.Encode(servers); err != nil {
						serverLogger.Println(err)
					}
				} else if r.Method == http.MethodDelete {
					// Delete server
					if path != "" {
						serverConns.deleteByPath(path)
					} else if addr != "" {
						serverConns.delete(addr)
					} else {
						http.Error(w, "Must provide 'addr' or 'path'", http.StatusBadRequest)
						return
					}
					w.Write([]byte("success"))
				} else if r.Method == http.MethodPost {
					if addr == "" || path == "" {
						http.Error(w, "Must provide 'addr' and 'path'", http.StatusBadRequest)
						return
					}
					if r.FormValue("website") == "1" {
						website = true
					}
					// Add new server, if path isn't already taken
					if _, loaded := serverConns.loadOrStore(&ServerConn{path, name, website, addr, false}); loaded {
						w.Write([]byte("failure"))
					} else {
						w.Write([]byte("success"))
					}
				} else {
					http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
				}
			})
			return r
		}(),
		ErrorLog: serverLogger,
	}
	server.ListenAndServe()
}

func pinger() {
	// Possibly use timer rather than this
	client := &http.Client{Timeout: time.Second}
	start := time.Now().Add(time.Minute)
	for {
		if time.Now().After(start) {
			serverConns.loopThru(func(iPath, iConn interface{}) bool {
				path, conn := iPath.(string), iConn.(*ServerConn)
				if _, err := client.Get(conn.addr + path); err != nil {
					if strings.Contains(err.Error(), "connection refused") {
						if conn.failed {
							serverConns.delete(path)
						} else {
							conn.failed = true
						}
					} else {
						conn.failed = false
					}
				} else {
					conn.failed = false
				}
				return true
			})
			start.Add(time.Minute)
		}
	}
}
