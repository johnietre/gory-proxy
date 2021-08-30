package main

import (
  "fmt"
  "net/url"
  "regexp"
)

var pathRegexp = regexp.MustCompile(`^/(\w+)`)

func main() {
  g1, _ := url.Parse("https://google.com/")
  g2, _ := url.Parse("https://google.com")
  g3, _ := url.Parse("https://google.com/johnie")
  g4, _ := url.Parse("https://google.com/johnie/tre")
  fmt.Println(g1.Path, pathRegexp.FindStringSubmatch(g1.Path))
  fmt.Println(g2.Path, pathRegexp.FindStringSubmatch(g2.Path))
  fmt.Println(g3.Path, pathRegexp.FindStringSubmatch(g3.Path))
  fmt.Println(g4.Path, pathRegexp.FindStringSubmatch(g4.Path))
}
