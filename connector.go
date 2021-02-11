package main

import (
  "flag"
  "io/ioutil"
  "net/http"
  "net/url"
  // "os/exec"
  // "strings"
)

var (
  proxyURL string = "http://localhost:9999/"
)

func main() {
  // Get the flags from the command line
  // If adding a host, "route" must be combined with "host"
  // If removing a host, "remove" must be added as well as "route" or "host"
  route := flag.String("route", "", "The route for the proxy")
  host := flag.String("host", "", "The host/address of server")
  remove := flag.Bool("remove", false, "Remove the host or route given using the -host or -route flags")

  flag.Parse()
  var form url.Values
  if *remove {
    if *route != "" {
      form = url.Values{"remove": {"1"}, "route": {*route}}
    } else if *host != "" {
      form = url.Values{"remove": {"1"}, "host": {*host}}
    } else {
      println("Must provide host or route")
      return
    }
    resp, err := http.PostForm(proxyURL, form)
    if err != nil {
      println(err.Error())
      return
    }
    if body, err := ioutil.ReadAll(resp.Body); err != nil {
      println(err.Error())
    } else {
      println(string(body))
    }
    return
  } else if *route == "" && *host == "" {
    resp, err := http.Get(proxyURL)
    if err != nil {
      println(err.Error())
      return
    }
    if body, err := ioutil.ReadAll(resp.Body); err != nil {
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
  if (*host)[len(*host)-1] == '/' {
  }
  if (*route)[0] != '/' {
    *route = "/" + *route
  }
  if (*route)[len(*route)-1] != '/' {
    *route += "/"
  }

  form := url.Values{"route": {*route}, "host": {*host}}
  resp, err := http.PostForm(proxyURL, form)
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

func sendRequst(form url.Values) (string, error) {
  var err error
  var resp *http.Response
  if form == nil {
    resp, err = http.Get(proxyURL)
  } else {
    resp, err = http.PostForm(proxyURL, form)
  }
  if err != nil {
    return "", err
  }
  body, err := ioutil.ReadAll(resp.Body)
  return string(body), err
}

