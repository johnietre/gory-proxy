package goryproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	jtutils "github.com/johnietre/utils/go"
	"go.uber.org/zap"
)

type RW = http.ResponseWriter
type Req = *http.Request

const tunnelQueueLen uint32 = 1000

var (
	// Logger is the logger used.
	Logger = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)
	// Currently unused
	ZapLogger *zap.Logger
	// LogFilePath is used to serve a log file if a file is logged to.
	LogFilePath string

	TunnelConnectTimeout time.Duration
)

// Router is a proxy router used to proxy connections. To use it solely as a
// handler, create it from NewRouterHandler. To use as a with its full
// functionality, create from any of the functions and use as both a handler
// and listener (e.g., http.Serve(router, router)).
type Router struct {
	ln         net.Listener
	acceptChan chan net.Conn
	lnErr      error
	started    atomic.Bool

	routes jtutils.SyncMap[string, *Server]

	tunnelQueue [tunnelQueueLen]chan net.Conn
	tunnelID    uint32

	tunnelAddr   string
	tunnelConn   net.Conn
	tunnelServer *Server

	savePath string
	saveMtx  jtutils.Mutex[jtutils.Unit]
}

// RunHTTP creates a http.Server and runs it using the Router as the handler
// and listener (http.Server.Serve(r)).
func (r *Router) RunHTTP() error {
	r.Start()
	s := &http.Server{
		Handler:  r,
		ErrorLog: Logger,
	}
	return s.Serve(r)
}

// RunHTTPS creates a http.Server and runs it using the Router as the handler
// and listener (http.Server.ServeTLS(r, keyFile, certFile)).
func (r *Router) RunHTTPS(keyFile, certFile string) error {
	r.Start()
	s := &http.Server{
		Handler:  r,
		ErrorLog: Logger,
	}
	return s.ServeTLS(r, keyFile, certFile)
}

// NewRouterHandler creates a new Router to be used solely as an http.Handler.
func NewRouterHandler() *Router {
	return &Router{}
}

// NewRouter creates a new Router which listens on the given addr. It can also
// be used as an http.Handler.
func NewRouter(addr string) (*Router, error) {
	return RouterConfig{LnFunc: ConnectLn(addr), Start: true}.Create()
}

// RouterFromListener creates a new Router with the given `ln`. It can also be
// used as an http.Handler.
func RouterFromListener(ln net.Listener) *Router {
	return jtutils.Must(RouterConfig{LnFunc: ReturnLn(ln), Start: true}.Create())
}

// NewTunneledRouter creates a new Router which listens on the given addr
// and tunnels to the given tunnelAddr. The passed Server `s` is the server
// used for the tunneled-to proxy. It can also be used as an http.Handler.
func NewTunneledRouter(addr, tunnelAddr string, s *Server) (*Router, error) {
	s.Addr = "tunnel"
	return RouterConfig{
		LnFunc:       ConnectLn(addr),
		TunnelAddr:   tunnelAddr,
		TunnelServer: s,
		Start:        true,
	}.Create()
}

// TunneledRouterFromListener creates a new Router with the given `ln` and
// tunnels to the given tunnelAddr. The passed Server `s` is the server used
// for the tunneled-to proxy. It can also be used as an http.Handler.
func TunneledRouterFromListener(
	ln net.Listener,
	tunnelAddr string,
	s *Server,
) (*Router, error) {
	s.Addr = "tunnel"
	return RouterConfig{
		LnFunc:       ReturnLn(ln),
		TunnelAddr:   tunnelAddr,
		TunnelServer: s,
		Start:        true,
	}.Create()
}

// RouterConfig are the options passed to RouterFromConfig
type RouterConfig struct {
	// LnFunc is the function to get the listener from (see ReturnLn and
	// ConnectLn).
	// It must be specified.
	LnFunc func() (net.Listener, error)

	// TunnelAddr is the address to tunnel to. When tunneling, both this and
	// TunnelServer must be provided.
	TunnelAddr string
	// TunnelServer is the server used for the tunneled-to proxy. When tunneling,
	// both this and TunnelAddr must be provided.
	TunnelServer *Server

	// SaveTo is the path to save changes in servers to. An empty string means
	// nothing is saved.
	SaveTo string
	// LoadFrom is the path to load servers from (to populate router). An empty
	// string means nothing is loaded. Loading occurs before anything else (e.g.,
	// before LnFunc is called).
	LoadFrom string

	// AcceptChanLen is the length of the channel that holds newly accepted conns
	// from the listener. When the channel is full, no more conns are accepted
	// until a conn is removed from the channel. A value <= 0 defaults to 5.
	AcceptChanLen int

	// Start specifies whether or not to start the router after creation.
	Start bool
}

// ReturnLn creates a function that returns the listener (and never errors).
func ReturnLn(ln net.Listener) func() (net.Listener, error) {
	return func() (net.Listener, error) { return ln, nil }
}

// ConnectLn returns a function that calls and returns the result of
// net.Listen("tcp", addr).
func ConnectLn(addr string) func() (net.Listener, error) {
	return func() (net.Listener, error) { return net.Listen("tcp", addr) }
}

// Create creates the router (see RouterFromConfig).
func (ro RouterConfig) Create() (*Router, error) {
	return RouterFromConfig(ro)
}

// RouterFromConfig creates a new Router from the give options. It does NOT
// automatically start the router (unless specified in the opts).
func RouterFromConfig(opts RouterConfig) (*Router, error) {
	var err error

	if opts.LnFunc == nil {
		return nil, fmt.Errorf("must provide LnFunc")
	}

	if opts.AcceptChanLen <= 0 {
		opts.AcceptChanLen = 5
	}
	r := &Router{
		acceptChan:   make(chan net.Conn, opts.AcceptChanLen),
		tunnelAddr:   opts.TunnelAddr,
		tunnelServer: opts.TunnelServer,
		savePath:     opts.SaveTo,
	}
	for i := range tunnelQueueLen {
		r.tunnelQueue[i] = make(chan net.Conn)
	}

	if opts.LoadFrom != "" {
		err := r.LoadServers(opts.LoadFrom)
		if err != nil {
			return nil, err
		}
	}

	if opts.TunnelAddr != "" || opts.TunnelServer != nil {
		if opts.TunnelAddr == "" || opts.TunnelServer == nil {
			return nil, fmt.Errorf("tunneled router must have both tunnel addr and tunnel server")
		}
		r.tunnelConn, err = connectTunnel(r.tunnelAddr, r.tunnelServer)
		if err != nil {
			return nil, err
		}
	}

	ln, err := opts.LnFunc()
	if err != nil {
		return nil, err
	}
	r.ln = ln

	if opts.Start {
		r.Start()
	}
	return r, nil
}

// Start starts the router (if its not a handler only and isn't already
// running).
func (r *Router) Start() {
	if r.ln == nil || r.started.Swap(true) {
		return
	}
	go r.listen()
	if r.tunnelAddr != "" {
		go r.listenTunnel()
	}
}

// LoadServers attempts to load servers from the specified path. If the
// specified path is empty (blank string), the path servers are saved to (if
// set) is used.
func (r *Router) LoadServers(path string) error {
	if path == "" {
		return r.loadServers(nil)
	}
	return r.loadServers(&path)
}

// IsHandlerOnly returns whether the proxy can only be used as a handler.
func (r *Router) IsHandlerOnly() bool {
	return r.ln == nil && r.acceptChan == nil
}

// SaveOnChangeTo sets what path the router should save its servers to. An
// empty path means nothing is saved. Should not be called after the router
// has been started (whether it's serving as a handler or started as a server).
func (r *Router) SaveOnChangeTo(path string) {
	r.savePath = path
}

// SavePath returns the path the router saves its servers to. An empty path
// means nothing is saved.
func (r *Router) SavePath() string {
	return r.savePath
}

// ServeHTTP implements the http.Handler interface.
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
	if !router.IsHandlerOnly() {
		switch baseSlug {
		case "":
			switch r.Method {
			case http.MethodPost:
				router.addServer(w, r)
			case http.MethodDelete:
				router.deleteServer(w, r)
			default:
				router.serveHome(w, r)
			}
			return
		case "log":
			router.serveLog(w, r)
			return
		}
	}
	if server, ok := router.routes.Load(baseSlug); ok {
		if r.URL.Path[0] == '/' {
			baseSlug = "/" + baseSlug
		}
		r.URL.Path = strings.Replace(r.URL.Path, baseSlug, "", 1)
		server.proxy.ServeHTTP(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

var (
	// ErrServerExists is returned when trying to add a server that already
	// exists.
	ErrServerExists = fmt.Errorf("server already exists")
	// ErrNoServerProxy is returned when trying to add a server that doesn't
	// have an attached proxy.
	ErrNoServerProxy = fmt.Errorf("server must have proxy")
)

// AddServer adds a server (i.e., a new path) to the proxy.
func (router *Router) AddServer(srvr *Server) error {
	return router.addServerHelper(srvr, true)
}

func (router *Router) addServerHelper(srvr *Server, checkSave bool) error {
	if srvr.Path == "" || srvr.Name == "" {
		return fmt.Errorf("must have server name and path")
	} else if srvr.proxy == nil {
		return ErrNoServerProxy
	} else if _, loaded := router.routes.LoadOrStore(srvr.Path, srvr.Clone()); loaded {
		return ErrServerExists
	}
	if checkSave {
		return router.saveServers()
	}
	return nil
}

// AddServers adds the servers from the map by passing each to
// `Router.AddServer`. The returned map is a mapping of servers and
// corresponding errors from adding them. If a server is successfully added, it
// will not be present as a key in the returned map. The map will never be nil.
// An error is returned if an error occurs while saving (if applicable).
func (router *Router) AddServers(srvrs map[string]*Server) (map[*Server]error, error) {
	errs := map[*Server]error{}
	for _, srvr := range srvrs {
		err := router.addServerHelper(srvr, false)
		if err != nil {
			errs[srvr] = err
		}
	}
	return errs, router.saveServers()
}

func (router *Router) saveServers() error {
	if router.savePath == "" {
		return nil
	}
	bytes, err := json.Marshal(router.GetServers())
	if err != nil {
		return err
	}
	router.saveMtx.Lock()
	defer router.saveMtx.Unlock()
	return os.WriteFile(router.savePath, bytes, 0777)
}

func (router *Router) loadServers(pathPtr *string) error {
	path := router.savePath
	if pathPtr != nil {
		path = *pathPtr
	}
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	m := make(map[string]*Server)
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return err
	}
	for _, srvr := range m {
		if err := router.addServerHelper(srvr, false); err != nil {
			return err
		}
	}
	return nil
}

var (
	// ErrServerNotExist is returned when trying to remove a server that doesn't
	// exist.
	ErrServerNotExist = fmt.Errorf("server does not exist")
	// ErrMismatchAddr is returned when the addr of a server to delete doesn't
	// match the one stored.
	ErrMismatchAddr = fmt.Errorf("mistmatch addresses")
)

// DeleteServer removes a server from the proxy. The server passed doesn't need
// to be the one stored by the proxy, but must have the same Path and Addr as
// the one to delete.
func (router *Router) DeleteServer(srvr *Server) error {
	s, ok := router.routes.Load(srvr.Path)
	if !ok {
		return ErrServerNotExist
	} else if srvr.Addr != s.Addr {
		return ErrMismatchAddr
	}
	router.routes.Delete(srvr.Path)
	return router.saveServers()
}

// GetServers returns clones of the stored servers.
func (router *Router) GetServers() map[string]*Server {
	// TODO: ignore tunnels? only when saving?
	srvrs := make(map[string]*Server)
	router.routes.Range(func(path string, srvr *Server) bool {
		srvrs[path] = srvr.Clone()
		return true
	})
	return srvrs
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
	srvr.AddProxy(httputil.NewSingleHostReverseProxy(u))
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
	/*
		//t, err := template.ParseFiles("index.html")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			Logger.Println(err)
			return
		}
	*/
	t := tmpl
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
		Logger.Println(err)
	}
}

func (router *Router) serveLog(w RW, r Req) {
	http.ServeFile(w, r, LogFilePath)
}

func (router *Router) listen() {
	for {
		c, err := router.ln.Accept()
		if err != nil {
			router.lnErr = err
			Logger.Println(err)
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
			Logger.Printf("error reading from connecting tunnel proxy: %v", err)
			bc.Close()
			return
		}
		bc.SetReadDeadline(time.Time{})
		s := &Server{}
		// Start from 4 to get rid of the header bytes that were still in the buffer
		if err := json.Unmarshal(buf[4:n], &s); err != nil || s.Name == "" || s.Path == "" {
			if err != nil {
				Logger.Println(err)
			}
			// TODO: Do something with error?
			bc.Write(headerBadMessageBytes)
			bc.Close()
			return
		}
		s.AddProxy(router.newTunnelProxy(bc))
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

// Accept should only be called by the http package server.
func (router *Router) Accept() (net.Conn, error) {
	if router.acceptChan == nil {
		return nil, fmt.Errorf("router is handler only")
	}
	c := <-router.acceptChan
	if c == nil {
		return nil, router.lnErr
	}
	return c, nil
}

// Close is used to close the proxy.
func (router *Router) Close() error {
	if router.tunnelConn != nil {
		router.tunnelConn.Close()
	}
	if router.ln == nil {
		return nil
	}
	return router.ln.Close()
}

// Addr returns the address of the proxy's listener.
func (router *Router) Addr() net.Addr {
	if router.ln == nil {
		return nil
	}
	return router.ln.Addr()
}

var tunnelURL = mustValue(url.Parse("http://0.0.0.0:0"))

func (router *Router) newTunnelProxy(c net.Conn) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(tunnelURL)
	transport := http.DefaultTransport.(*http.Transport)
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
	p.Transport = transport
	return p
}

func (router *Router) listenTunnel() {
	// TODO: Do something to signify the tunnel has been closed
tunnelLoop:
	for {
		var buf [8]byte
		if n, err := router.tunnelConn.Read(buf[:]); err != nil {
			Logger.Println("tunnel disconnected")
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

	shouldRun := jtutils.NewT(true)
	deferrer := jtutils.NewDeferredCloser(shouldRun)
	defer deferrer.Run()
	deferrer.Add(c)

	if TunnelConnectTimeout > 0 {
		if err := c.SetDeadline(time.Now().Add(TunnelConnectTimeout)); err != nil {
			return nil, err
		}
		defer c.SetDeadline(time.Time{})
	}
	// Marshal and the send the server data, then wait for a response
	buf, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write(append(headerTunnelBytes, buf...)); err != nil {
		return nil, fmt.Errorf("error writing when connecting: %w", err)
	} else if _, err := c.Read(buf); err != nil {
		return nil, fmt.Errorf("error reading when connecting: %w", err)
	}
	// Check the response
	switch getHeader(buf) {
	case HeaderSuccess:
	case HeaderBadMessage:
		return nil, newTunnelError(HeaderBadMessage, "bad name or path")
	case HeaderAlreadyExists:
		return nil, newTunnelError(
			HeaderAlreadyExists, "name or path already exists on tunneled-to server")
	default:
		return nil, newTunnelError(HeaderNothing, "an error occurred")
	}
	*shouldRun = false
	return c, nil
}

// Server is a proxied-to server. To add a server to the router, a proxy
// must be attached with one of the AddNewProxy*/AddProxy methods.
type Server struct {
	// Name is the display name for the server.
	Name string `json:"name,omitempty"`
	// Path is the unique path the server is accessible from through the router.
	Path string `json:"path,omitempty"`
	// Addr is the address of the server.
	Addr string `json:"addr,omitempty"`
	// Hold whether the server should be displayed on the site or not
	Hidden bool `json:"hidden,omitempty"`

	proxy *httputil.ReverseProxy

	isTunnel   bool
	tunnelConn net.Conn
}

// Clone returns a shallow clone of the Server.
func (s *Server) Clone() *Server {
	return &Server{
		Name:     s.Name,
		Path:     s.Path,
		Addr:     s.Addr,
		Hidden:   s.Hidden,
		proxy:    s.proxy,
		isTunnel: s.isTunnel,
	}
}

// AddNewProxy creates a url.URL and passes it to AddNewProxyWithURL. Returns
// an error if the a URL can't be parsed.
func (s *Server) AddNewProxy(addr string) error {
	u, err := url.Parse(addr)
	if err != nil {
		return err
	}
	s.AddNewProxyWithURL(u)
	return nil
}

// AddNewProxyWithURL creates a new httputil.ReverseProxy and calls AddProxy
// with it.
func (s *Server) AddNewProxyWithURL(u *url.URL) {
	s.AddProxy(httputil.NewSingleHostReverseProxy(u))
}

// AddNewProxyFromAddr calls `Server.AddNewProxy` with the server's
// `Server.Addr` field.
func (s *Server) AddNewProxyFromAddr() error {
	return s.AddNewProxy(s.Addr)
}

// Proxy returns the httputil.ReverseProxy attached to the server.
func (s *Server) Proxy() *httputil.ReverseProxy {
	return s.proxy
}

// AddProxy attaches the reverse proxy to the Server. A proxy is required to
// add a Server to a router.
func (s *Server) AddProxy(p *httputil.ReverseProxy) {
	p.ErrorLog = Logger
	// The ReverseProxy will log an error if it's original director isn't called
	/*
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
	*/
	rwf := p.Rewrite
	if rwf == nil {
		rwf = noopRewrite
	}
	p.Rewrite = func(r *httputil.ProxyRequest) {
		// TODO: Forwarded
		r.Out.Header.Add("Gory-Proxy-Path", s.Path)
		r.Out.Header["X-Forwarded-For"] = r.In.Header["X-Forwarded-For"]
		r.SetXForwarded()
	}
	/*
	  p.ModifyResponse = func(resp *http.Response) error {
	    resp.Request.URL.Path = path.Join(s.Path, resp.Request.URL.Path)
	    return nil
	  }
	*/
	s.proxy = p
}

func noopRewrite(*httputil.ProxyRequest) {}

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

const indexHtml = `
<!DOCTYPE html>

<html lang="en-US">

<head>
  <title>Gory Proxy</title>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>

<body>
  {{range .}}
    <a href="/{{.Path}}">{{.Name}}</a></br>
  {{end}}
</body>

</html>
`

var tmpl = template.Must(template.New("index.html").Parse(indexHtml))
