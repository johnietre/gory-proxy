package main

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/textproto"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const (
	network        = "tcpv4"
	reqBufferSize  = 2048
	pipeBufferSize = 1024
	readDur        = time.Second
)

var (
	ip, port                              string
	serverCertFilePath, serverKeyFilePath string
	clientCertFilePath, clientKeyFilePath string

	numRunning int32
	logger     = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)
	zeroTime   = time.Time{}
	reqExpr    = regexp.MustCompile(`^\w+ /([^/ ]+)?(/.+)? HTTP`)
	siteMap    sync.Map
)

func init() {
	flag.StringVar(
		&ip,
		"ip",
		"127.0.0.1",
		"IPv4 address to run proxy server on",
	)
	flag.StringVar(
		&port,
		"port",
		"12345",
		"Port to run proxy server on",
	)
	flag.StringVar(
		&serverCertFilePath,
		"cert",
		"",
		"Path to server PEM encoded cert file; must also include server PEM encoded key file",
	)
	flag.StringVar(
		&serverKeyFilePath,
		"key",
		"",
		"Path to server PEM encoded key file; must also include server PEM encoded cert file",
	)
	flag.StringVar(
		&clientCertFilePath,
		"cert",
		"",
		"Path to client PEM encoded cert file; must also include client PEM encoded key file",
	)
	flag.StringVar(
		&clientKeyFilePath,
		"key",
		"",
		"Path to client PEM encoded key file; must also include client PEM encoded cert file",
	)
	flag.Parse()
}

func main() {
	// Create listener
	var ln net.Listener
	var err error
	addr := net.JoinHostPort(ip, port)
	if serverCertFilePath != "" && serverKeyFilePath != "" {
		// Create the TLS listener with given credentials
		cert, err := tls.LoadX509KeyPair(serverCertFilePath, serverKeyFilePath)
		if err != nil {
			log.Fatal("fatal error loading X509 key pair: %v", err)
		}
		config := &tls.Config{Certificates: []tls.Certificate{cert}}
		config.Rand = rand.Reader
		ln, err = tls.Listen(network, addr, config)
	} else if serverCertFilePath == serverKeyFilePath {
		// If they're equal, they're both empty
		// Create a regular listener
		ln, err = net.Listen(network, addr)
	} else {
		log.Fatal("if providing server PEM files, both must be included")
	}
	if err != nil {
		log.Fatalf("fatal error creating proxy: %v", err)
	}
	// Create the TCP listener
	ln, ok := ln.(*net.TCPListener)
	if !ok {
		log.Fatal("fatal error making TCP listener from regular listener")
	}
	// Listen for new connections
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("fatal error: %v", err)
			break
		}
		go handleConn(conn)
	}
	for atomic.LoadInt32(&numRunning) != 0 {
	}
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

	if err := client.SetReadDeadline(time.Now().Add(readDur)); err != nil {
		// IDEA: Send error message to client?
		logger.Printf("error setting read deadline: %v", err)
		return
	}
	// Read the first line of the request
	tpReader := textproto.NewReader(bufio.NewReader(client))
	line, err := tpReader.ReadLine()
	if err != nil {
		if !errIsTimeout(err) && !errors.Is(err, io.EOF) {
			logger.Printf("error reading request line: %v", err)
		}
		return
	}
	// Get the path from the request
	parts := reqExpr.FindStringSubmatch(line)
	if len(parts) == 0 {
		// Invalid HTTP 1 request
		client.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}
	siteName := parts[1]

	var server net.Conn
	println(siteName)

	*closeConn = false
	go pipeConns(client, server)
	go pipeConns(server, client)
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

func errIsTimeout(err error) bool {
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}
