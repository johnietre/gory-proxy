package main

import (
  "flag"
  "io/ioutil"
  "net/http"
  "net/url"
  "os/exec"
  "strings"
)

var (
  proxy_url string = "http://localhost:9999/"
)

func main() {
  route := flag.String("route", "", "The route for the proxy")
  host := flag.String("host", "", "The host/address of server")
  remove := flag.String("remove", "", "The route or host/address of the server to remove from the proxy")

  flag.Parse()

  if *remove != "" {
    form := url.Values{"remove": {*remove}}
    resp, err := http.PostForm(proxy_url, form)
    if err != nil {
      println(err.Error())
    } else {
      println(string(body))
    }
    return
  } else if *route == "" && *host == "" {
    resp, err := http.Get(proxy_url)
    if err != nil {
      println(err.Error())
    }
    body, err := iotuil.ReadAll(resp.Body)
    if err != nil {
      println(err.Error())
    } else {
      println(string(body))
    }
    return
  } else if *route == "" {
    println("Must provide route")
    return
  } else if *host == "" {
    println("Must provide host")
    return
  }
  // Clean the input
  if *host[len(*host)-1] == '/' {
  }
  if *route[0] != '/' {
    *route = "/" + route
  }
  if *route[len(*route)-1] != '/' {
    *route += "/"
  }

  form := url.Values{"route": {*route}, "host": {*host}}
  resp, err := http.PostForm(proxy_url, form)
  if err != nil {
    println(err.Error())
    return
  }
  body, err := ioutil.ReadAll(resp.Body)
  if err != nil {
    println(err.Error())
  } else {
    println(string(body))
  }
}

