package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ConnMap is used to store connections and associated routes
type ConnMap struct {
	conns map[string]string
	sync.RWMutex
}

// Add adds a route to the connections map
func (cm *ConnMap) Add(route string, addr string) bool {
	cm.Lock()
	defer cm.Unlock()
	if cm.conns[route] != "" {
		return false
	}
	cm.conns[route] = addr
	return true
}

// Get returns the address associated with the given route
func (cm *ConnMap) Get(route string) string {
	cm.RLock()
	defer cm.RUnlock()
	return cm.conns[route]
}

// Delete deletes a route from the connections map
func (cm *ConnMap) Delete(route string) {
	cm.Lock()
	defer cm.Unlock()
	delete(cm.conns, route)
}

const (
	headerLength int    = 4
	defaultIP    string = "localhost"
	defaultPort  string = "8000"
)

var (
	// IP is the IP address
	IP string
	// Port is the port
	Port         string
	webLn        net.Listener
	pingLn       net.Listener
	err          error
	routerLogger *log.Logger
	connMap      ConnMap
)

func main() {
	webLn, err = net.Listen("tcp", IP+":"+Port)
	if err != nil {
		panic(err)
	}
	pingLn, err = net.Listen("tcp", ":4444")
	if err != nil {
		panic(err)
	}
	routerLogger = log.New(os.Stdout, "Router: ", log.LstdFlags)

	go listenServers()
	listenWeb()
}

/* Web Listener */

func listenWeb() {
	for {
		conn, err := webLn.Accept()
		if err != nil {
			routerLogger.Println(err)
			continue
		}
		go handleConn(conn)
	}
}

// Handle errors here somehow (send some sort of message back to sender)
func handleConn(client net.Conn) {
	defer client.Close()
	var bmsg [5000]byte
	l, err := client.Read(bmsg[:])
	if err != nil {
		routerLogger.Println(err)
		return
	}
	// Parse header to find route
	start := false
	route := ""
	for _, b := range bmsg[:l] {
		if b == '/' && !start {
			start = true
			route = "/"
		} else if start && (b == '/' || b == ' ') {
			break
		} else if start {
			route += string(b)
		}
	}
	// Get the associated address and pass the request to the server
	addr := connMap.Get(route)
	if addr == "" {
		// Send 404
		return
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		// Send internal server error
		routerLogger.Println(err)
		return
	}
	defer conn.Close()
	if _, err = conn.Write(bmsg[:l]); err != nil {
		// Send internal server error
		routerLogger.Println(err)
		return
	}
	if l, err = conn.Read(bmsg[:]); err != nil {
		// Send internal server error
		routerLogger.Println(err)
		return
	}
	if _, err = client.Write(bmsg[:l]); err != nil {
		routerLogger.Println(err)
	}
}

/* Server Listener */

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
	connAddr := conn.RemoteAddr().String()
	var route string
	var addr string // Address to be routed to
	// Use block so the byte block is removed from memory afterwards
	{
		var bmsg [64]byte
		// Get message length and convert it to an integer
		if _, err := conn.Read(bmsg[:headerLength]); err != nil {
			if err.Error() == "EOF" {
				routerLogger.Printf("Error connecting %s: EOF...\n", connAddr)
			} else {
				routerLogger.Printf("Error connecting %s: %s...\n", connAddr, err.Error())
			}
			return
		}
		l, err := strconv.ParseInt(string(bmsg[:headerLength]), 10, 32)
		if err != nil {
			routerLogger.Printf("Error connecting %s: %s\n", connAddr, err.Error())
			sendMsg(conn, "error")
			return
		}
		// Get the route and address, separated by a tab
		if _, err := conn.Read(bmsg[:l]); err != nil {
			routerLogger.Printf("Error connecting %s: %s\n", connAddr, err.Error())
			sendMsg(conn, "error")
			return
		}
		parts := strings.Split(string(bmsg[:l]), "\t")
		route, addr = parts[0], parts[1]
		if !connMap.Add(route, addr) {
			sendMsg(conn, "address in use")
			return
		}
	}
	defer connMap.Delete(route)
	sendMsg(conn, "connected")
	routerLogger.Printf("Connected Address: %s (%s) Route: %s\n", addr, connAddr, route)

	// Start pinging
	msgChan := make(chan string, 1)
	pinging := true
	after := time.After(time.Minute)
	for {
		select {
		case <-after:
			if pinging {
				if !sendMsg(conn, "ping") {
					routerLogger.Printf("Disconnecting Address: %s (%s) Route: %s: Error pinging\n", addr, connAddr, route)
					return
				}
				pinging = false
			} else {
				routerLogger.Printf("Disconnecting Address: %s (%s) Route: %s: Failed to pong...\n", addr, connAddr, route)
				return
			}
		case msg := <-msgChan:
			if msg == "closed" || msg == "error" {
				routerLogger.Printf("Disconnecting Address: %s (%s) Route: %s: %s...\n", addr, connAddr, route, msg)
				return
			} else if msg != "pong" {
				routerLogger.Printf("Disconnecting Address: %s (%s) Route: %s: Invalid message (%s)...\n", addr, connAddr, route, msg)
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

func sendMsg(conn net.Conn, msg string) bool {
	// Get the length of the message (as bytes) and add the header to the message
	msg = fmt.Sprintf("%0*d", headerLength, len([]byte(msg))) + msg
	bmsg := []byte(msg)
	if _, err := conn.Write(bmsg); err != nil {
		routerLogger.Println(err)
		return false
	}
	return true
}