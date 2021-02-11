package main

/* TODO
 * Make loops through conns (for loops) concurrent safe
 * Possibly remove some logs of errors that naturally occur (like EOF on conns)
 * Fix invalid type assertion error around line 160 (iConn.(*Conn)
 * Fix http.HandleFunc used as value around line 250
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

// Conn holds connection info
type Conn struct {
	host string
	// Disconnect is set to true if the server fails to respond the first time
	// If disconnect is already true, the next failure results in removal
	disconnect bool
}

// ConnMap holds the connections map
type ConnMap struct {
	conns map[string]*Conn
	sync.RWMutex
}

// Load retrieves the value for a given key
func (cm *ConnMap) Load(k string) (v *Conn, ok bool) {
	cm.RLock()
	defer cm.RUnlock()
	v, ok = cm.conns[k]
	return
}

// Store stores a given key:value pair if the key doesn't exist
func (cm *ConnMap) Store(k string, v *Conn) bool {
	cm.Lock()
	defer cm.Unlock()
	if _, ok := cm.conns[k]; ok {
		return false
	}
	cm.conns[k] = v
	return true
}

// Delete deletes a given key
func (cm *ConnMap) Delete(k string) {
	cm.Lock()
	defer cm.Unlock()
	delete(cm.conns, k)
}

var (
	ip           string = "192.168.1.137"
	port         string = "443"
	internalIP   string = "localhost"
	internalPort string = "9999"
	conns        ConnMap
	logger       *log.Logger
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
		conns: make(map[string]*Conn),
	}

	go listenServer()

	go ping()

	ln, err := net.Listen("tcp", ip+":"+port)
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
		c, ok := conns.Load(route)
		if !ok {
			return
		}
		// c := iConn.(*Conn)
		serverConn, err = net.Dial("tcp", c.host)
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

func listenServer() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			rAndH := ""
			for r, c := range conns.conns {
				rAndH += fmt.Sprintf("%s\t%s\n", r, c.host)
			}
			w.Write([]byte(rAndH))
			return
		}
		host, route, remove := r.FormValue("host"), r.FormValue("route"), r.FormValue("remove")
		if remove != "" {
			if route != "" {
				if conn, ok := conns.Load(route); !ok {
					w.Write([]byte("route " + route + "  doesn't exist"))
				} else {
					conns.Delete(route)
					w.Write([]byte("removed " + conn.host + route))
				}
			} else if host != "" {
				for r, c := range conns.conns {
					if host == c.host {
						conns.Delete(r)
						w.Write([]byte("removed " + c.host + r))
						return
					}
				}
				w.Write([]byte("host " + host + "  doesn't exist"))
			} else {
				w.Write([]byte("must provide host or route"))
			}
			return
		}
		msg, bad := "", false
		for r, c := range conns.conns {
			if !bad {
				bad = (c.host == host || r == route)
			}
			if c.host == host {
				msg += "host name taken "
			}
			if r == route {
				msg += "route is taken"
			}
		}
		if bad {
			return
		}
		if !conns.Store(route, &Conn{host, false}) {
			w.Write([]byte("route or host taken"))
		} else {
			w.Write([]byte("all good"))
		}
	})
	http.ListenAndServe(ip+":"+port, nil)
}

// ping sends GET requests to each conn every minute to make sure they're alive
func ping() {
	timer := time.AfterFunc(time.Minute, func() {
		for route, conn := range conns.conns {
			_, err := http.Get(conn.host)
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

