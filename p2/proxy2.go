package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ServerConn.Path are keys and ServerConn structs are values
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

// ServerConn holds information about servers connected to the proxy
type ServerConn struct {
	// The path (on the proxy) which points to the server
	// Ex) http://localhost:8000/hello => path = /hello
	// Ex) http://localhost:8000/hello/world => path = /hello
	Path string `json:"path"`
	// The name/title of the connection (website)
	// Path and Name aren't the same because Name can have spaces
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

const (
	ip           string = "localhost"
	port         string = "8080"
	internalIP   string = "localhost"
	internalPort string = "8888"
)

var (
	serverConns smap
	logger      *log.Logger
)

func main() {
	logger = log.New(os.Stdout, "Proxy: ", log.LstdFlags)

	go pageChecker()
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
/* TODO: Handle errors better */
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
		}
		pageMut.Unlock()
	}
}

/* IDEA: Allow path (and add arg to function) that allows ALL servers to be printed (even those without a site) */
func servePage(conn net.Conn, all bool) {
	// There will always be one, and only one, "%s" in the pageResponseFile
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

func listenForServers() {
	/* TODO: Make sure path is clean */
	/* TODO: Make responses better */
	/* Idea: Send different codes for failures */
	serverLogger := log.New(os.Stdout, "Proxy Server: ", log.LstdFlags)
	server := &http.Server{
		Addr: internalIP + ":" + internalPort,
		Handler: func() *http.ServeMux {
			r := http.NewServeMux()
			r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				// None used for GET
				// path or addr used for DELETE
				// path, addr, and name (optional) used for POST
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
						serverConns.delete(path)
					} else if addr != "" {
						serverConns.loopThru(func(iPath, iConn interface{}) bool {
							if iConn.(*ServerConn).addr == addr {
								serverConns.delete(iPath.(string))
								return false
							}
							return true
						})
					} else {
						http.Error(w, "Must provide 'addr' or 'path'", http.StatusBadRequest)
						return
					}
					w.Write([]byte("success"))
				} else if r.Method == http.MethodPost {
					// Path can't be favicon.ico
					if addr == "" || path == "" {
						http.Error(w, "Must provide 'addr' and 'path'", http.StatusBadRequest)
						return
					}
					if r.FormValue("website") == "1" {
						website = true
					}
					// Add new server, if path and name aren't already taken
					server := &ServerConn{path, name, website, addr, false}
					if _, loaded := serverConns.loadOrStore(server); loaded {
						w.Write([]byte("path already exists"))
					} else {
						nameExists := false
						serverConns.loopThru(func(iPath, iConn interface{}) bool {
							serv := iConn.(*ServerConn)
							if serv.Name == "" {
								return true
							} else if serv.Name == server.Name {
								if serv.Path == server.Path {
									return true
								}
								nameExists = true
								serverConns.delete(server.Path)
								return false
							}
							return true
						})
						if nameExists {
							w.Write([]byte("name already exists"))
						} else {
							w.Write([]byte("success"))
						}
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
	/* IDEA: Use timer rather than this */
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
