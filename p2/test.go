package main

import (
	"fmt"
	"net/http"
)

func main() {
	client := &http.Client{}
	if _, err := client.Get("https://google.com"); err != nil {
		fmt.Println(err)
	}
	if _, err := client.Get("https://yahoo.com"); err != nil {
		fmt.Println(err)
	}
	if _, err := client.Get("http://localhost:8000"); err != nil {
		fmt.Println(err)
	}
}
