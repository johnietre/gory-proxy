package main

import (
	"fmt"
	"io/ioutil"
)

func main() {
	bytes, err := ioutil.ReadFile("index.html")
	if err != nil {
		panic(err)
	}
	fmt.Printf(string(bytes), `<a href="/hello">Hello, World</a>\n`)
}
