package main

/* Connecting tunnels
* Message send by connecting attempting to connect:
  * 2 byte header padding (31623, square root of the lowest number over a billion with a perfect square root)
  * 1 byte server name length
  * 1 byte password length
  * Server name
  * Password
* Responses:
  * 2 byte header padding (31623) on all responses
  * Success:
    * 1 byte success code (15)
    * 1 byte message length (0)
  * Invalid password:
    * 1 byte success code (0)
    * 1 byte message length (0)
  * Error:
    * 1 byte success code (13)
    * 1 byte message length
    * Error message
*/

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const (
	network                     = "tcp4"
	reqBufferSize               = 2048
	pipeBufferSize              = 1024
	readDeadlineDuration        = time.Second
	tunnelHeaderPadding         = uint16(31623)
	badRequestResponse          = "HTTP/1.1 400 Bad Request\r\n\r\n"
	notFoundResponse            = "HTTP/1.1 404 Not Found\r\n\r\n"
	internalServerErrorResponse = "HTTP/1.1 500 Internal Server Error\r\n\r\n"
	gatewayTimeoutResponse      = "HTTP/1.1 504 Gateway Timeout\r\n\r\n"
)

type ServerInfo struct {
	Name   string `json:"name"`
	Addr   string `json:"addr"`
	Tunnel bool   `json:"tunnel"`
	Secure bool   `json:"secure"`
}

type ProxyConfig struct {
	Addr               string       `json:"addr"`
	ServerCertFilePath string       `json:"serverCertFilePath"`
	ServerKeyFilePath  string       `json:"serverKeyFilePath"`
	ClientCertFilePath string       `json:"clientCertFilePath"`
	Tunnel             bool         `json:"tunnel"`
	Servers            []ServerInfo `json:"servers"`
	Shutdown           bool         `json:"shutdown"`
	ForceShutdown      bool         `json:"forceShutdown"`
}

var (
	configFilePath string
	tunnelPassword string

	numRunning    int32
	serverInfoMap sync.Map
	ln            net.Listener
	tunnelReqs    uint32
	tunnelChans   sync.Map
	// TODO: Possibly keep "InsecureSkipVerify" as false
	clientTLSConfig = &tls.Config{InsecureSkipVerify: true}
	logger          = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)
	reqExpr         = regexp.MustCompile(`^\w+ /([^/ ]+)?(/.+)? HTTP`)
	reservedPaths   = map[string]func(net.Conn, []byte){
		"":            serveHome,
		"favicon.ico": serveFavicon,
	}
)

func init() {
	flag.StringVar(
		&configFilePath,
		"config-file-path",
		"",
		"The path to the proxy configuration file; uses $HOME/gory-proxy/config.json as default",
	)
	flag.StringVar(
		&tunnelPassword,
		"tunnel-password",
		"",
		"The password required for tunnels attempting to connect to the proxy or for connecting a tunnel to other proxies (max length 256 bytes)",
	)
	flag.Parse()
}

func main() {
	log.SetFlags(0)
	// Load the config file
	if configFilePath == "" {
		configFilePath = path.Join(os.Getenv("HOME"), "gory-proxy", "config.json")
	}
	proxyConfig, err := readConfigFile(configFilePath)
	if err != nil {
		log.Fatalf("error reading config file: %v", err)
	}
	// Create listener
	if proxyConfig.ServerCertFilePath != "" && proxyConfig.ServerKeyFilePath != "" {
		// Create the TLS listener with given credentials
		cert, err := tls.LoadX509KeyPair(
			proxyConfig.ServerCertFilePath,
			proxyConfig.ServerKeyFilePath,
		)
		if err != nil {
			log.Fatal("fatal error loading X509 key pair: %v", err)
		}
		config := &tls.Config{Certificates: []tls.Certificate{cert}}
		ln, err = tls.Listen(network, proxyConfig.Addr, config)
	} else if proxyConfig.ServerCertFilePath == proxyConfig.ServerKeyFilePath {
		// If they're equal, they're both empty
		// Create a regular listener
		ln, err = net.Listen(network, proxyConfig.Addr)
	} else {
		log.Fatal("if providing server PEM files, both must be included")
	}
	if err != nil {
		log.Fatalf("fatal error creating proxy: %v", err)
	}
	// Load the client cert if given
	if proxyConfig.ClientCertFilePath != "" {
		roots := x509.NewCertPool()
		if pemBytes, err := ioutil.ReadFile(proxyConfig.ClientCertFilePath); err != nil {
			log.Fatalf("error reading client cert file: %v", err)
		} else if !roots.AppendCertsFromPEM(pemBytes) {
			log.Fatal("failed to parse client certificate")
		}
		clientTLSConfig.InsecureSkipVerify = false
		clientTLSConfig.RootCAs = roots
	}
	// Load the server infos
	for _, serverInfo := range proxyConfig.Servers {
		serverInfoMap.Store(serverInfo.Name, serverInfo)
	}
	go watchConfigFile(proxyConfig)
	// Listen for new connections
	logger.Printf("starting proxy on %s", proxyConfig.Addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				logger.Printf("fatal error: %v", err)
			} else {
				logger.Print("proxy listener closed, shutting down")
			}
			break
		}
		go handleConn(conn)
	}
	logger.Print("waiting for processes to finish")
	for atomic.LoadInt32(&numRunning) != 0 {
	}
	logger.Print("all processes finished")
}

func handleConn(client net.Conn) {
	atomic.AddInt32(&numRunning, 1)
	defer atomic.AddInt32(&numRunning, -1)
	closeConn := new(bool)
	*closeConn = true
	defer func() {
		if *closeConn {
			client.Close()
		}
	}()

	if err := client.SetReadDeadline(time.Now().Add(readDeadlineDuration)); err != nil {
		// IDEA: Send error message to client?
		logger.Printf("error setting read deadline: %v", err)
		return
	}
	// Read the first line of the request
	var reqBuf [reqBufferSize]byte
	n, err := client.Read(reqBuf[:])
	if err != nil {
		if !errIsTimeout(err) && !errors.Is(err, io.EOF) {
			logger.Printf("error reading request line: %v", err)
		}
		return
	}
	// Reset the read deadline
	client.SetReadDeadline(time.Time{})
	// Check if the request is a tunnel request
	if n < 2 {
		// Invalid request
		client.Write([]byte(badRequestResponse))
		return
	} else {
		// Check the first 2 bytes for the header
		headerPadding := binary.BigEndian.Uint16(reqBuf[:2])
		if headerPadding == tunnelHeaderPadding {
			go createTunnel(client, reqBuf[2:])
			*closeConn = false
			return
		}
	}
	req := reqBuf[:n]
	// Get the path from the request
	parts := reqExpr.FindSubmatch(req)
	if len(parts) == 0 {
		// Invalid HTTP 1 request
		client.Write([]byte(badRequestResponse))
		return
	}
	siteName := string(parts[1])
	// Check if the site name is reserved
	serveFunc, ok := reservedPaths[siteName]
	if ok {
		serveFunc(client, req)
		return
	}
	// Get the server/site info
	iServerInfo, ok := serverInfoMap.Load(siteName)
	if !ok {
		// Site doesn't exist
		client.Write([]byte(notFoundResponse))
		return
	}
	// Remove the site name from the request URI
	if req[bytes.IndexByte(req, '/')+len(siteName)+1] == '/' {
		req = bytes.Replace(req, []byte(siteName+"/"), []byte{}, 1)
	} else {
		req = bytes.Replace(req, []byte(siteName), []byte{}, 1)
	}
	// Append the "Forwarded" header for servers that want client information
	req = bytes.Replace(
		req,
		[]byte("\r\n\r\n"),
		[]byte("\r\nForwarded: for"+client.RemoteAddr().String()+"\r\n\r\n"),
		1,
	)
	// Handle tunnels appropriately
	serverInfo := iServerInfo.(ServerInfo)
	if serverInfo.Tunnel {
		go serveTunnel(client, req)
		*closeConn = false
		return
	}
	// Connect to the server
	var server net.Conn
	if serverInfo.Secure {
		server, err = tls.Dial(network, serverInfo.Addr, clientTLSConfig)
	} else {
		server, err = net.Dial(network, serverInfo.Addr)
	}
	if err != nil {
		client.Write([]byte(internalServerErrorResponse))
		logger.Printf("error connecting to server: %v", err)
		return
	}
	// Write the request to the server
	if _, err := server.Write([]byte(req)); err != nil {
		client.Write([]byte(internalServerErrorResponse))
		logger.Printf("error writing request line to server: %v")
		return
	}

	// Pipe the client and server
	*closeConn = false
	go pipeConns(client, server)
	go pipeConns(server, client)
}

func serveHome(client net.Conn, req []byte) {
	client.Write([]byte(notFoundResponse))
}

func serveFavicon(client net.Conn, req []byte) {
	client.Write([]byte(notFoundResponse))
}

func serveTunnel(client net.Conn, req []byte) {
	atomic.AddInt32(&numRunning, 1)
	defer atomic.AddInt32(&numRunning, -1)
	tunnelID := atomic.AddUint32(&tunnelReqs, 1)
	timeout := time.NewTimer(tunnelTimeout)
	var server net.Conn
	select {
	case <-timeout.C:
		// Write
		client.Write([]byte(gatewayTimeoutResponse))
		client.Close()
		return
	case server = <-tunnelConnChan:
	}
	go pipeConns(client, server)
	go pipeConns(server, client)
}

func createTunnel(client net.Conn, req []byte) {
	if len(req) < 2 {
		// Send error, invalid request
		resp := make([]byte, 4)
		binary.BigEndian.PutUint16(resp, tunnelHeaderPadding)
		client.Write(resp)
		client.Close()
		return
	}
	lengths := binary.BigEndian.Uint16(req[:2])
	req = req[2:]
	if len(req) != lengths {
		// Invalid request
		client.Write(resp)
		client.Close()
		return
	}
	siteNameLen := byte(lengths)
	siteName := string(req[:siteNameLen])
	passwordLen := lengths >> 8
	password := string(req[siteNameLen:])
	if password != tunnelPassword {
		// Incorrect password
		client.Close()
		return
	}
	_, loaded := serverInfoMap
}

func pipeConns(from, to net.Conn) {
	atomic.AddInt32(&numRunning, 1)
	defer atomic.AddInt32(&numRunning, -1)
	defer from.Close()
	for {
		var buf [pipeBufferSize]byte
		if n, err := from.Read(buf[:]); err != nil {
			// Handle the error if it isn't an io.EOF or closed connection
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				logger.Println(err)
			}
			// Close the other connection which will lead to the other instance
			// of the function running to return
			to.Close()
			return
		} else if _, err = to.Write(buf[:n]); err != nil {
			if !errors.Is(err, net.ErrClosed) {
				logger.Println(err)
			}
			return
		}
	}
}

func watchConfigFile(oldConfig *ProxyConfig) {
	const timerDuration = time.Second * 30
	lastModTime := time.Time{}
	var timer *time.Timer
	timer = time.AfterFunc(timerDuration, func() {
		defer timer.Reset(timerDuration)
		// Get the file data to see if it
		info, err := os.Stat(configFilePath)
		if err != nil {
			logger.Fatalf("error reading config file stats: %v", err)
		}
		if !info.ModTime().After(lastModTime) {
			return
		}
		// Read the config file
		newConfig, err := readConfigFile(configFilePath)
		if err != nil {
			if _, ok := err.(*os.PathError); ok {
				logger.Fatalf("error reading config file: %v", err)
			}
			logger.Printf("error parsing congif file: %v", err)
			return
		}
		// Close the listener if specified
		if newConfig.Shutdown {
			ln.Close()
			return
		}
		if newConfig.ForceShutdown {
			logger.Fatal("forcing shutdown")
		}
		// Check to see if the length of the servers array is the same
		if len(newConfig.Servers) == len(oldConfig.Servers) {
			// Check to see if any other server infos are different
			update := false
			for i, serverInfo := range newConfig.Servers {
				if serverInfo != oldConfig.Servers[i] {
					update = true
					break
				}
			}
			// Don't continue if there doesn't need to be an update
			if !update {
				oldConfig = newConfig
				return
			}
		}
		// Update the server infos
		serverInfoMap.Range(func(name, iInfo interface{}) bool {
			info := iInfo.(ServerInfo)
			serverInfoMap.Delete(k)
			return true
		})
		for _, serverInfo := range newConfig.Servers {
			serverInfoMap.Store(serverInfo.Name, serverInfo)
		}
		oldConfig = newConfig
	})
}

func readConfigFile(filePath string) (*ProxyConfig, error) {
	configFile, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	config := &ProxyConfig{}
	if err := json.NewDecoder(configFile).Decode(config); err != nil {
		return nil, err
	}
	return config, nil
}

func errIsTimeout(err error) bool {
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}
