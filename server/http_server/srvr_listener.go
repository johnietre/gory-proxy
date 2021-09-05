package main

import (
	"encoding/json"
	"log"
  "net"
	"net/http"
  "net/url"
	"os"
	"strings"
	"sync"
	"time"
)

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
  // Network address of the server
  // When creating, everything that should be passed is as follows:
  // Scheme
  // IP
  // Port
  // Path to get to server (must be single slug)
  // ex) wss://123.123.123.123:8000/server1
  // ex) invalid: https://123.123.123.123:8000/server2/slug3
  addr *url.URL
	// Failed tells whether the connection has failed once before
	// If so, the next fail (consecutive fails) will result in the server being
	// disconnected
	failed bool
}

// ServerConn.Path are keys and ServerConn structs are values
type smap struct {
	mmap sync.Map
}

func (s *smap) Store(conn *ServerConn) {
	s.mmap.Store(conn.addr.Path, conn)
}

func (s *smap) Load(path string) *ServerConn {
	ic, exists := s.mmap.Load(path)
	if exists {
		return ic.(*ServerConn)
	}
	return nil
}

func (s *smap) LoadOrStore(conn *ServerConn) (*ServerConn, bool) {
	ic, loaded := s.mmap.LoadOrStore(conn.addr.Path, conn)
	return ic.(*ServerConn), loaded
}

func (s *smap) Range(f func(k, v interface{}) bool) {
	s.mmap.Range(f)
}

func (s *smap) Delete(path string) {
	s.mmap.Delete(path)
}

func listenForServers() {
  u, _ := url.Parse("http://localhost:8123")
  serverConns.Store(&ServerConn{
    Path: "/server1",
    Name: "server1",
    Website: false,
    addr: u,
  })
  u, _ = url.Parse("http://localhost:8234")
  serverConns.Store(&ServerConn{
    Path: "/server2",
    Name: "server2",
    addr: u,
  })
  return

	serverLogger := log.New(os.Stdout, "Proxy Server: ", log.LstdFlags)
	server := &http.Server{
		Addr: config.ConnectorAddr,
		Handler: func() *http.ServeMux {
			r := http.NewServeMux()
      r.HandleFunc("/servers", serversHandler)
			return r
		}(),
		ErrorLog: serverLogger,
	}
	log.Println(server.ListenAndServe())
}

var reservedPaths = map[string]bool{
  "/": true,
  "/favicon.ico": true,
  "/admin": true,
}

func serversHandler(w http.ResponseWriter, r *http.Request) {
  /* TODO: Make sure path is clean */
  /* TODO: Make responses better */
  /* IDEA: Send different codes for failures */

  // None of the following variables are used for GET
  // addr is the address for the server (host (ip+port) and/or path)
  // If the host is provided, a scheme must also be provided
  // addr is used for DELETE
  // addr, name (optional), and website (optional) are used for POST
  addr, err := url.Parse(r.FormValue("addr"))
  if err != nil {
    http.Error(
      w,
      "Invalid addr (must be /path, scheme://host, or scheme://host/path)",
      http.StatusBadRequest,
    )
    return
  }
  name := r.FormValue("name")
  website := r.FormValue("website") == "1"
  if r.Method == http.MethodGet {
    // Get the list of servers (do any sorting and filtering)
    /* TODO: take queries for sorting and filtering */
    var servers []*ServerConn
    serverConns.Range(func(k, iConn interface{}) bool {
      servers = append(servers, iConn.(*ServerConn))
      return true
    })
    // Send the list of servers
    if err := json.NewEncoder(w).Encode(servers); err != nil {
      http.Error(w, "Internal Server Error", http.StatusInternalServerError)
      logger.Println(err)
    }
  } else if r.Method == http.MethodDelete {
    // Delete a server
    if !reservedPaths[addr.Path] && addr.Path != "" {
      // Directly delete the server if the path is given
      serverConns.Delete(addr.Path)
    } else if addr.Host != "" {
      // Find the address and delete the based on the path correlated with it
      serverConns.Range(func(iPath, iConn interface{}) bool {
        if iConn.(*ServerConn).addr.Host == addr.Host {
          serverConns.Delete(iPath.(string))
          return false
        }
        return true
      })
    } else {
      http.Error(
        w,
        "Invalid addr (must be /path, scheme://host, or scheme://host/path)",
        http.StatusBadRequest,
      )
      return
    }
    w.Write([]byte("sucess"))
  } else if r.Method == http.MethodPost {
    // Add a server
    if addr.Host == "" || addr.Path == "" {
      http.Error(w, "Must provide 'addr' and 'path'", http.StatusBadRequest)
    }
    if reservedPaths[addr.Path] {
      http.Error(w, "Invalid path", http.StatusBadRequest)
      return
    }
    // Add new server, if path and name aren't already taken
    server := &ServerConn{
      Path: addr.Path,
      Name: name,
      Website: website,
      // addr: addr,
      failed: false,
    }
    // Failure if a server with the same necessary information already exists
    if _, loaded := serverConns.LoadOrStore(server); loaded {
      w.Write([]byte("path already exists"))
      return
    }
    // Check to make sure the name doesn't already exist
    nameExists := false
    serverConns.Range(func(iPath, iConn interface{}) bool {
      conn := iConn.(*ServerConn)
      if conn.Name == "" {
        return true
      } else if conn.Name == server.Name {
        // If the name is the same, check to make sure it's not the same server
        if conn.Path == server.Path {
          return true
        }
        nameExists = true
        serverConns.Delete(server.Path)
        return false
      }
      return true
    })
    // Failure if the name already exists
    if nameExists {
      w.Write([]byte("name already exists"))
    } else {
      w.Write([]byte("success"))
    }
  } else {
    // Invalid method
    http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
  }
}

func pinger() {
	start := time.Now().Add(time.Minute)
	// Wait for 1 minute, poll the servers, then repeat
	for {
		if time.Now().After(start) {
			// Loop through the map of server conns
			serverConns.Range(func(iPath, iConn interface{}) bool {
				path, conn := iPath.(string), iConn.(*ServerConn)
				// Send a GET request through the client to the server
        if sock, err := net.Dial("tcp", conn.addr.Host); err != nil {
					// If the connection was refused, it's possible the server is no
					// longer running
					if strings.Contains(err.Error(), "connection refused") {
						// If the connection has already failed once, delete the server
						// from the map of servers
						if conn.failed {
							serverConns.Delete(path)
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
          sock.Close()
					conn.failed = false
				}
				return true
			})
			// Wait for another minute
			start = time.Now().Add(time.Minute)
		}
	}
}
