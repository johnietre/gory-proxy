package main

/* Connecting tunnels
* Message sent by connecting attempting to connect:
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
    * 1 byte invalid password code (0)
    * 1 byte message length (0)
  * Error:
    * 1 byte error code (13)
    * 1 byte message length
    * Error message
*/

/* Executing commands from local machine or remote
* Message sent to update server
  * 2 byte header padding (3375)
  * 2 byte message length
  * 1 byte password length (no password required if client is local machine)
  * 1 byte 0 pad
  * Message (JSON)
    * Action
      * Add server
      * Remove server
      * Shutdown
    * Contents
      * Add server:
        * ServerInfo struct as JSON
      * Remove server:
        * Server name
      * Shutdown
        * Shutdown timeout until force shutdown (if any)
  * Password
* Responses:
  * 2 byte header padding (3375) on all responses
  * Success
    * 1 byte success code (15)
    * 1 byte message length (0)
  * Invalid password:
    * 1 byte invalid password code (0)
    * 1 byte message length (0)
  * Error:
    * 1 byte error code (13)
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
	badRequestResponse          = "HTTP/1.1 400 Bad Request\r\n\r\n"
	notFoundResponse            = "HTTP/1.1 404 Not Found\r\n\r\n"
	internalServerErrorResponse = "HTTP/1.1 500 Internal Server Error\r\n\r\n"
	gatewayTimeoutResponse      = "HTTP/1.1 504 Gateway Timeout\r\n\r\n"
  successResponseCode = 0xF
  invalidPasswordResponseCode = 0x00
  errorResponseCode = 0x0D
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
		header := binary.BigEndian.Uint16(reqBuf[:2])
    if isTunnelHeader(header)
			go createTunnel(client, reqBuf[2:])
			*closeConn = false
			return
		} else if isCommandHeader(header) {
      go serveCommand(client, reqBuf[2:])
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
	timeot := time.NewTimer(tunnelTimeout)
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
  closeConn := new(bool)
  *closeConn  = true
  defer func() {
    if *closeConn {
      client.Close()
    }
  }()
	if len(req) < 2 {
		client.Write(errorResp(tunnelHeader(), "malformed request"))
		return
	}
  // The site name  and password lengths
  siteNameLen, passwordLen := req[0], req[1]
	req = req[2:]
	if len(req) != siteNameLen + passwordLen {
		client.Write(errorResp(tunnelHeader(), "content length mismatch"))
		return
	}
  // Check the password
  if string(req[:siteNameLen]) != tunnelPassword {
    client.Write(invaldPasswordResp(tunnelHeader()))
		return
	}
  // Add the site name
  siteName := string(req[siteNameLen:])
  serverInfo := ServerInfo{Name: siteName, Tunnel: true}
	if _, loaded := serverInfoMap.LoadOrStore(siteName, serverInfo); loaded {
    client.Write(errorResp(tunnelHeader(), "server name already exists"))
    return
  }
  tunnel
}

type Command struct {
  Action string `json:"action"`
  ServerInfo ServerInfo `json:"serverInfo,omitempty"`
  ShutdownTimeout int `json:"shutdownTimeout,omitempty"`
}

func serveCommand(client net.Conn, req []byte) {
  defer client.Close()
  resp := commandHeader()
  if len(req) < 4 {
    client.Write(errorResp(commandHeader(), "malformed request"))
    return
  }
  // The message and password lengths
	msgLen, passwordLen := binary.BigEndian.Uint16(req[:2]), req[3]
  req = req[4:] // 4 to remote the 1 byte 0 pad
  if len(req) != msgLen + passwordLen {
    client.Write(errorResp(commandHeader(), "content length mismatch"))
    return
  }
  // Get the password if the connection is not from the local machine
  host, _, _ := net.SplitHostPort(client.RemoteAddr().String())
  if host != "127.0.0.1" {
    if string(req[msgLen:]) != commandPassword {
      client.Write(invalidPasswordResp(commandHeader())
      return
    }
  }
  // Get and execute the command
  var command Command
  if err := json.Unmarshal(req[:msgLen], &command); err != nil {
    client.Write(errorResp(commandHeader(), "malformed request json"))
    return
  }
  switch command.Action {
  case "add":
    serverInfo := command.ServerInfo
    if serverInfo.Name == "" || serverInfo.Addr == "" {
      client.Write(errorResp(commandHeader(), "invalid server info"))
      return
    } else if serverInfo.Tunnel {
      client.Write(errorResp(commandHeader(), "cannot add tunnel server"))
      return
    }
    if _, loaded := serverInfoMap.LoadOrStore(serverInfo.Name, serveInfo); loaded {
      client.Write(errorResp(commandHeader(), "server name already exists"))
      return
    }
  case "remove":
    serverInfoMap.Delete(command.ServerInfo.Name)
  case "shutdown":
    if command.ShutdownTimeout > 0 {
      time.AfterFunc(func() {
        time.Sleep(time.Second*time.Duration(command.ShutdownTimeout))
        logger.Fatal("shutdown timed out, forcefully shutting down")
      })
    }
    ln.Close()
  default:
    client.Write(errorResp(commandHeader(), "invalid action: "+command.Action))
    return
  }
  client.Write(commandHeader(), successResp)
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

// Header constants
func tunnelHeader() []byte {
  return []byte{0x9B, 0x87}
}

func commandHeader() []byte {
  return []byte{0x0D, 0x2F}
}

func isTunnelHeader(s []byte) bool {
  return bytes.Equal(tunnelHeader(), s)
}

func isCommandHeader(s []byte) bool {
  return bytes.Equal(commandHeader(), s)
}

func successResp(header []byte) []byte {
  return append(header, []byte{0x0F, 0x00}...)
}

func invalidPasswordResp(header []byte) []byte {
  return append(header, []byte{0x00, 0x00}...)
}

func errorResp(header []byte, msg string) []byte {
  return append(append(header, []byte{0x0D, byte(len(msg))}...), msg...)
}
