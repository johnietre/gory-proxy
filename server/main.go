package main

import "C"

import (
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

type route string

// ServerConn is a server conn
type ServerConn struct {
	addr string
}

// ConnMap is used to store connections and associated routes
type ConnMap struct {
	conns map[route]string
	sync.RWMutex
}

// Add adds a route to the connections map
func(cm *ConnMap) Add(r route) {
	cm.Lock()
	cm.conns[r] = ""
	cm.Unlock()
}

// Delete deletes a route from the connections map
func(cm *ConnMap) Delete(r route) {
	cm.Lock()
	delete(cm.conns, r)
	cm.Unlock()
}

const (
	headerLength int = 4
	defaultIP string = "localhost"
	defaultPort string = "8000"
)

var (
	// IP is the IP address
	IP string
	// Port is the port
	Port string
	webLn net.Listener
	pingLn net.Listener
	err error
	routerLogger *log.Logger
	connMap ConnMap
)

func init() {
	webLn, err = net.Listen("tcp", IP + ":" + Port)
	if err != nil {
		panic(err)
	}
	pingLn, err = net.Listen("tcp", ":4444")
	if err != nil {
		panic(err)
	}
	routerLogger = log.New(os.Stdout, "Router: ", log.LstdFlags)
}

func listen() {
	for {
		conn, err := webLn.Accept()
		if err != nil {
			routerLogger.Println(err)
			continue
		}
		go handleConn(conn)
	}
}


func handleConn(conn net.Conn) {
	defer conn.Close()
}

func listenServers() {
	for {
		conn, err := pingLn.Accept()
		if err != nil {
			routerLogger.Println(err)
			continue
		}
		go pingPong(conn)
	}
}

// pingPong adds servers to the connections map, receives messages 
// from the connected servers and makes sure they are still active
func pingPong(conn net.Conn) {
	defer conn.Close()
	var r route
	// Use block so the byte block is removed from memory afterwards
	{
		var bmsg [64]byte
		if _, err := conn.Read(bmsg[:headerLength]); err != nil {
			if err.Error() == "EOF" {
				routerLogger.Printf("Error connecting %s: EOF...\n", conn.RemoteAddr().String())
			} else {
				routerLogger.Printf("Error connecting %s: %s...\n", conn.RemoteAddr().String(), err.Error())
			}
			return
		}
		l, err := strconv.ParseInt(string(bmsg[:headerLength]), 10, 32)
		if err != nil {
			routerLogger.Printf("Error connecting %s: %s", conn.RemoteAddr().String(), err.Error())
			conn.Write([]byte("ERROR"))
			return
		}
		if _, err := conn.Read(bmsg[:l]); err != nil {
			routerLogger.Printf("Error connecting %s: %s", conn.RemoteAddr().String(), err.Error())
			conn.Write([]byte("ERROR"))
			return
		}
		r = route(bmsg[:l])
		connMap.Add(r)
	}
	defer connMap.Delete(r)
	msgChan := make(chan string, 1)
	pinging := true
	after := time.After(time.Minute)
	for {
		select {
		case <- after:
			if pinging {
				// Write header
				conn.Write([]byte("ping"))
				pinging = false
			} else {
				routerLogger.Printf("Disconnecting %s: Failed to pong...\n", conn.RemoteAddr().String())
				return
			}
		case msg := <- msgChan:
			if msg == "closed" || msg == "error" {
				routerLogger.Printf("Disconnecting %s: %s...\n", conn.RemoteAddr().String(), msg)
				return
			} else if msg != "pong" {
				routerLogger.Printf("Disconnecting %s: Invalid message (%s)...\n", conn.RemoteAddr().String(), msg)
				return
			}
			pinging = true
			after = time.After(time.Minute)
		}
	}
}

func getMsg(conn net.Conn, msgChan chan string) {
	// Checks whether there was an error or not and sends a message accordingly
	check := func(err error, c chan string) bool {
		if err != nil {
			if err.Error() == "EOF" {
				msgChan <- "closed"
			} else {
				routerLogger.Println(err)
				msgChan <- "error"
			}
		}
		return err != nil
	}
	var bmsg [1024]byte
	for {
		// Read the msg header to get the length
		_, err := conn.Read(bmsg[:headerLength])
		if check(err, msgChan) {
			return
		}
		l, err := strconv.ParseInt(string(bmsg[:4]), 10, 32)
		if check(err, msgChan) {
			return
		}
		_, err = conn.Read(bmsg[:l])
		if check(err, msgChan) {
			return
		}
		msg := string(bmsg[:l])
		// Send closing message
		msgChan <- msg
		if msg != "pong" {
			return
		}
	}
}
