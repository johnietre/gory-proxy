package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
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
	// The path will always have a leading slash but not one trailing
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
	// Ex: http://192.168.1.130:8765
	addr string
	// Failed tells whether the connection has failed once before
	// If so, the next fail (consecutive fails) will result in the server being
	// disconnected
	failed bool
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
				// None are used for GET
				// path or addr are used for DELETE
				// path, addr, and name (optional) are used for POST
				path, name, addr, website := r.FormValue("path"), r.FormValue("name"), r.FormValue("addr"), false
				if r.Method == http.MethodGet {
					// Get the list of the servers
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
					// Delete a server
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
					// Add a server
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
	log.Println(server.ListenAndServe())
}

func pinger() {
	client := &http.Client{Timeout: time.Second}
	start := time.Now().Add(time.Minute)
	// Wait for 1 minute, poll the servers, then repeat
	for {
		if time.Now().After(start) {
			// Loop through the map of server conns
			serverConns.loopThru(func(iPath, iConn interface{}) bool {
				path, conn := iPath.(string), iConn.(*ServerConn)
				// Send a GET request through the client to the server
				if _, err := client.Get(conn.addr + path); err != nil {
					// If the connection was refused, it's possible the server is no
					// longer running
					if strings.Contains(err.Error(), "connection refused") {
						// If the connection has already failed once, delete the server
						// from the map of servers
						if conn.failed {
							serverConns.delete(path)
						} else {
							// If it didn't fail previously, specify that it has failed once
							conn.failed = true
						}
					} else {
						// If the connection wasn't refused, assume the server is running,
						// therefore there was no connection failure
						conn.failed = false
					}
				} else {
					// If there was no error, the server is running
					conn.failed = false
				}
				return true
			})
			// Wait for another minute
			start = time.Now().Add(time.Minute)
		}
	}
}
