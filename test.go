package main

import (
	"fmt"
	"net"
)

func main() {
	ips, err := net.LookupIP("https://google.com")
	if err != nil {
		panic(err)
	}
	for i, ip := range ips {
		fmt.Println(i, ip.String())
	}
}
