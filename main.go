package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
  "path/filepath"
  "runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type RW = http.ResponseWriter
type Req = *http.Request

const tunnelQueueLen uint32 = 1000

var (
  logger = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)
  logFilePath string
)

func init() {
  _, thisFile, _, ok := runtime.Caller(0)
  if !ok {
    logger.Fatal("error getting log directory")
  }
  logFilePath = filepath.Join(filepath.Dir(thisFile), "proxy.log")
  f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
  if err != nil {
    logger.Fatal(err)
  }
  logger.SetOutput(f)
}

func main() {
  tunnelSrvr := &Server{}
	var addr, tunnelAddr string
	flag.StringVar(&addr, "addr", "127.0.0.1:8000", "Address to run the server on")
	flag.StringVar(&tunnelAddr, "tunnel", "", "Address to connect tunnel to")
	flag.StringVar(
		&tunnelSrvr.Name,
		"name",
		"",
		"Name of the server displayed on the tunneled-to proxy (must have tunnel flag",
	)
	flag.StringVar(
		&tunnelSrvr.Path,
		"path",
		"",
		"Path of the server on the tunneled-to proxy (must have tunnel flag",
	)
  flag.BoolVar(&tunnelSrvr.Hidden, "hidden", false, "Whether the tunnel server should be hidden")
	flag.Parse()

	var (
		r   *Router
		err error
	)
	if tunnelAddr != "" {
		if tunnelSrvr.Name == "" || tunnelSrvr.Path == "" {
			fmt.Fprintln(os.Stderr, "must provide name and path when tunneling")
			return
		}
    log.Println("attempting tunneling to", tunnelAddr)
		r, err = NewTunneledRouter(addr, tunnelAddr, tunnelSrvr)
	} else {
		r, err = NewRouter(addr)
	}
	if err != nil {
		logger.Fatal(err)
	}
	s := &http.Server{
		Handler:  r,
		ErrorLog: logger,
	}
  log.Println("starting proxy on", addr)
	logger.Fatal(s.Serve(r))
}

type Router struct {
	ln         net.Listener
	acceptChan chan net.Conn
	lnErr      error

	routes SyncMap[string, *Server]

	tunnelQueue [tunnelQueueLen]chan net.Conn
	tunnelID    uint32

	tunnelAddr   string
	tunnelConn   net.Conn
	tunnelServer *Server
}

func NewRouter(addr string) (*Router, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	r := &Router{ln: ln, acceptChan: make(chan net.Conn, 5)}
	for i := uint32(0); i < tunnelQueueLen; i++ {
		r.tunnelQueue[i] = make(chan net.Conn)
	}
	go r.listen()
	return r, nil
}

func NewTunneledRouter(addr, tunnelAddr string, s *Server) (*Router, error) {
	// Connect to the tunnel
  s.Addr = "tunnel"
	c, err := connectTunnel(tunnelAddr, s)
	if err != nil {
		return nil, err
	}
	// Create the router
	r, err := NewRouter(addr)
	if err == nil {
		r.tunnelAddr = tunnelAddr
		r.tunnelConn = c
		r.tunnelServer = s
		go r.listenTunnel()
	}
	return r, err
}

func (router *Router) ServeHTTP(w RW, r Req) {
	// Get the base path slug
	var baseSlug string
	if i := strings.Index(r.URL.Path, "/"); i == -1 {
		baseSlug = r.URL.Path
	} else if i != 0 {
		baseSlug = r.URL.Path[:i]
	} else if i1 := strings.Index(r.URL.Path[1:], "/"); i1 != -1 {
		baseSlug = r.URL.Path[1 : 1+i1]
	} else {
		baseSlug = r.URL.Path[1:]
	}
	if baseSlug == "" {
		switch r.Method {
		case http.MethodPost:
			router.addServer(w, r)
		case http.MethodDelete:
			router.deleteServer(w, r)
		default:
			router.serveHome(w, r)
		}
		return
	} else if baseSlug == "log" {
    router.serveLog(w, r)
    return
  }
	if server, ok := router.routes.Load(baseSlug); ok {
		// TODO: Set "Forwarded" header
		if r.URL.Path[0] == '/' {
			baseSlug = "/" + baseSlug
		}
		r.URL.Path = strings.Replace(r.URL.Path, baseSlug, "", 1)
		server.proxy.ServeHTTP(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (router *Router) addServer(w RW, r Req) {
	defer r.Body.Close()
	srvr := &Server{}
	if err := json.NewDecoder(r.Body).Decode(srvr); err != nil {
    http.Error(w, "Bad json", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(srvr.Addr)
  if err != nil {
    http.Error(w, "Bad server address", http.StatusBadRequest)
    return
  } else if u.Scheme != "http" && u.Scheme != "https" {
    http.Error(w, "Invalid proto", http.StatusBadRequest)
    return
  } else if srvr.Path == "" || srvr.Name == "" {
    http.Error(w, "Must include path and name", http.StatusBadRequest)
    return
  }
	srvr.addProxy(httputil.NewSingleHostReverseProxy(u))
	if _, loaded := router.routes.LoadOrStore(srvr.Path, srvr); loaded {
		// TODO: Send different error w/ message
		http.Error(w, "Server already exists", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (router *Router) deleteServer(w RW, r Req) {
	defer r.Body.Close()
	srvr := &Server{}
	if err := json.NewDecoder(r.Body).Decode(srvr); err != nil {
    http.Error(w, "Bad json", http.StatusBadRequest)
		return
	}
	s, ok := router.routes.Load(srvr.Path)
	if !ok {
    http.Error(w, "Server does not exist", http.StatusNotFound)
		return
	}
	if srvr.Addr != s.Addr {
    http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	router.routes.Delete(srvr.Path)
	if s.isTunnel {
		s.tunnelConn.Close()
	}
	w.WriteHeader(http.StatusOK)
}

func (router *Router) serveHome(w RW, r Req) {
	t, err := template.ParseFiles("index.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.Println(err)
		return
	}
	parts := r.Header.Values("Gory-Proxy-Path")
	var data []pageData
	router.routes.Range(func(_ string, srvr *Server) bool {
    if !srvr.Hidden {
      data = append(data, srvr.ToPageData(parts))
    }
		return true
	})
	sort.Slice(data, func(i, j int) bool {
		return data[i].Name < data[j].Name
	})
	if err := t.Execute(w, data); err != nil {
		logger.Println(err)
	}
}

func (router *Router) serveLog(w RW, r Req) {
  http.ServeFile(w, r, logFilePath)
}

func (router *Router) listen() {
	for {
		c, err := router.ln.Accept()
		if err != nil {
			router.lnErr = err
      logger.Println(err)
			// TODO
			//close(router.acceptChan)
			return
		}
		go router.handleConn(c)
	}
}

func (router *Router) handleConn(c net.Conn) {
	// Check the error?
	c.SetReadDeadline(time.Now().Add(time.Second * 30))
	// Convert the conn into a buf conn and check for a tunnel req header
	bc := NewBufConn(c)
	h, err := bc.Peek(4)
	// TODO: Log error?
	if err != nil {
		bc.Close()
		return
	}
	if header := getHeader(h); header == HeaderConnect {
		// Read 8 bytes: 4 for the header still in the buffer and 4 for the id
		h = make([]byte, 8)
		if n, err := bc.Read(h); err != nil {
			// TODO: Log error?
			bc.Close()
			return
		} else if n != 8 {
			// TODO: Log someting?
			bc.Close()
			return
		}
		bc.SetReadDeadline(time.Time{})
		index := binary.BigEndian.Uint32(h[4:]) % tunnelQueueLen
		select {
		case router.tunnelQueue[index] <- bc:
		default:
			// The one requesting the conn is no longer waiting for it
			bc.Close()
		}
	} else if header == HeaderTunnel {
		buf := make([]byte, 256)
		n, err := bc.Read(buf)
		if err != nil {
			// TODO: Do something with error (or delete logging)
			logger.Println("error reading from connecting tunnel proxy: %v", err)
			bc.Close()
			return
		}
		bc.SetReadDeadline(time.Time{})
		s := &Server{}
		// Start from 4 to get rid of the header bytes that were still in the buffer
		if err := json.Unmarshal(buf[4:n], &s); err != nil || s.Name == "" || s.Path == "" {
			if err != nil {
				logger.Println(err)
			}
			// TODO: Do something with error?
			bc.Write(headerBadMessageBytes)
			bc.Close()
			return
		}
		s.addProxy(router.newTunnelProxy(bc))
		s.isTunnel = true
		s.tunnelConn = bc
		if _, loaded := router.routes.LoadOrStore(s.Path, s); loaded {
			bc.Write(headerAlreadyExistsBytes)
			bc.Close()
		}
		bc.Write(headerSuccessBytes)
	} else {
		bc.SetReadDeadline(time.Time{})
		router.acceptChan <- bc
	}
}

// Accept should only be called by the http package server
func (router *Router) Accept() (net.Conn, error) {
	c := <-router.acceptChan
	if c == nil {
		return nil, router.lnErr
	}
	return c, nil
}

func (router *Router) Close() error {
	if router.tunnelConn != nil {
		router.tunnelConn.Close()
	}
	return router.ln.Close()
}

func (router *Router) Addr() net.Addr {
	return router.ln.Addr()
}

var tunnelURL = mustValue(url.Parse("http://0.0.0.0:0"))

func (router *Router) newTunnelProxy(c net.Conn) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(tunnelURL)
	transport := *http.DefaultTransport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, _ string, _ string) (net.Conn, error) {
		id := router.nextID()
		index := id % tunnelQueueLen
		// Remove an old conn if one exists
		select {
		case old := <-router.tunnelQueue[index]:
			old.Close()
		default:
		}
		// TODO: Do something more with the error?
		if _, err := c.Write(append(headerConnectBytes, put4(id)...)); err != nil {
      // TODO: Remove the tunnel if it was disconnected?
			return nil, fmt.Errorf("error getting tunnel connection: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case tc := <-router.tunnelQueue[index]:
			return tc, nil
		}
	}
	p.Transport = &transport
	return p
}

func (router *Router) listenTunnel() {
	// TODO: Do something to signify the tunnel has been closed
tunnelLoop:
	for {
		var buf [8]byte
		// TODO: Log error?
		if n, err := router.tunnelConn.Read(buf[:]); err != nil {
			log.Println("tunnel disconnected")
			for {
				// TODO: Do more with error
				c, err := connectTunnel(router.tunnelAddr, router.tunnelServer)
				if err != nil {
					// Return if the tunnel has been replaced on the tunneled-to server
					if te, ok := err.(*TunnelError); ok && te.header == HeaderAlreadyExists {
						return
					}
				} else {
					router.tunnelConn = c
					continue tunnelLoop
        }
				time.Sleep(time.Minute)
			}
			return
		} else if n != 8 {
			// TODO: Something?
			continue
		}
		go router.handleTunnelConn(buf)
	}
}

func (router *Router) handleTunnelConn(buf [8]byte) {
	if getHeader(buf[:]) != HeaderConnect {
		return
	}
	id := binary.BigEndian.Uint32(buf[4:])
	// TODO: Log error?
	// TODO: Dial with server dial options (or something)?
	c, err := net.Dial("tcp", router.tunnelAddr)
	if err != nil {
		return
	}
	if _, err := c.Write(append(headerConnectBytes, put4(id)...)); err != nil {
		c.Close()
		return
	}
	// TODO: Do something if tunnel closed
	router.acceptChan <- c
}

func (router *Router) nextID() uint32 {
	return atomic.AddUint32(&router.tunnelID, 1)
}

type TunnelError struct {
	header uint32
	msg    string
}

func newTunnelError(header uint32, msg string) *TunnelError {
	return &TunnelError{header: header, msg: msg}
}

func (e *TunnelError) Error() string {
	return e.msg
}

func connectTunnel(addr string, s *Server) (net.Conn, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	// Marshal and the send the server data, then wait for a response
	buf, err := json.Marshal(s)
	if err != nil {
		c.Close()
		return nil, err
	}
	if _, err := c.Write(append(headerTunnelBytes, buf...)); err != nil {
		c.Close()
		return nil, fmt.Errorf("error writing when connecting: %w", err)
	} else if _, err := c.Read(buf); err != nil { // TODO: Use deadline?
		c.Close()
		return nil, fmt.Errorf("error reading when connecting: %w", err)
	}
	// Check the response
	switch getHeader(buf) {
	case HeaderSuccess:
	case HeaderBadMessage:
		c.Close()
		return nil, newTunnelError(HeaderBadMessage, "bad name or path")
	case HeaderAlreadyExists:
		c.Close()
		return nil, newTunnelError(
			HeaderAlreadyExists, "name or path already exists on tunneled-to server")
	default:
		c.Close()
		return nil, newTunnelError(HeaderNothing, "an error occurred")
	}
	return c, nil
}

type Server struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	Addr string `json:"addr,omitempty"`
  // Hold whether the server should be displayed on the site or not
  Hidden bool `json:"hidden,omitempty"`

	proxy *httputil.ReverseProxy

	isTunnel   bool
	tunnelConn net.Conn
}

func (s *Server) addProxy(p *httputil.ReverseProxy) {
	p.ErrorLog = logger
	// The ReverseProxy will log an error if it's original director isn't called
	if d := p.Director; d == nil {
		p.Director = func(r *http.Request) {
			r.Header.Add("Gory-Proxy-Path", s.Path)
		}
	} else {
		p.Director = func(r *http.Request) {
			r.Header.Add("Gory-Proxy-Path", s.Path)
			d(r)
		}
	}
	/*
	  p.ModifyResponse = func(resp *http.Response) error {
	    resp.Request.URL.Path = path.Join(s.Path, resp.Request.URL.Path)
	    return nil
	  }
	*/
	s.proxy = p
}

type pageData struct {
	Name, Path string
}

func (s *Server) ToPageData(parts []string) pageData {
	return pageData{
		Name: s.Name,
		Path: path.Join(path.Join(parts...), s.Path),
	}
}

type BufConn struct {
	r *bufio.Reader
	net.Conn
}

func NewBufConn(c net.Conn) BufConn {
	return BufConn{bufio.NewReader(c), c}
}

func (c BufConn) Peek(n int) ([]byte, error) {
	return c.r.Peek(n)
}

func (c BufConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

const (
	// HeaderNothing represents no header
	HeaderNothing uint32 = 0x0
	// HeaderTunnel is the header used to create a new tunnel
	HeaderTunnel uint32 = 0xFFFFFFFF
	// HeaderConnect is used to connect a new conn to the tunnel
	HeaderConnect uint32 = 0xFFFFFFFE
	// HeaderSucess represents a successful action
	HeaderSuccess uint32 = 0xFFFFFFFD
	// HeaderBadMessage represents a bad message send
	HeaderBadMessage uint32 = 0xFFFFFFFC
	// HeaderAlreadyExists means the server already exists
	HeaderAlreadyExists uint32 = 0xFFFFFFB
)

var (
	headerTunnelBytes        = put4(HeaderTunnel)
	headerConnectBytes       = put4(HeaderConnect)
	headerSuccessBytes       = put4(HeaderSuccess)
	headerBadMessageBytes    = put4(HeaderBadMessage)
	headerAlreadyExistsBytes = put4(HeaderAlreadyExists)
)

func getHeader(p []byte) uint32 {
	if len(p) < 4 {
		return HeaderNothing
	}
	if p[0] == 255 && p[1] == 255 && p[2] == 255 {
		switch p[3] {
		case 0xFF:
			return HeaderTunnel
		case 0xFE:
			return HeaderConnect
		case 0xFD:
			return HeaderSuccess
		case 0xFC:
			return HeaderBadMessage
		case 0xFB:
			return HeaderAlreadyExists
		}
	}
	return HeaderNothing
}

func put4(u uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, u)
	return b
}

func mustValue[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
