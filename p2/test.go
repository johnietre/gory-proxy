package main

import (
	"regexp"
)

func main() {
	// var urlRegex = regexp.MustCompile(`^(https?://[\w\.]+)/(\w+/?)`)
	var urlRegex = regexp.MustCompile(`^(https?://[\w:\.]+)(/\w+)?`)
	matches := urlRegex.FindStringSubmatch("https://localhost:8000/hello")
	if matches == nil {
		println("no")
	} else {
		for _, m := range matches {
			println(m)
		}
	}
	println()
	matches = urlRegex.FindStringSubmatch("https://google.com/hello")
	if matches == nil {
		println("no")
	} else {
		for _, m := range matches {
			println(m)
		}
	}
	println()
	matches = urlRegex.FindStringSubmatch("http://local/hello/world")
	if matches == nil {
		println("no")
	} else {
		for _, m := range matches {
			println(m)
		}
	}
	println()
	matches = urlRegex.FindStringSubmatch("http://google.com/")
	if matches == nil {
		println("no")
	} else {
		for _, m := range matches {
			println(m)
		}
	}
}
