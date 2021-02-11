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
    // Take the route over the host
    if *route != "" {
      form = url.Values{"remove": {"1"}, "route": {*route}}
    } else if *host != "" {
      form = url.Values{"remove": {"1"}, "host": {*host}}
    } else {
      println("Must provide host or route")
      return
    }
    // Send the remove form
    if body, err := sendRequest(form); err != nil {
      println(err.Error())
    } else {
      println(body)
    }
    return
  } else if *route == "" && *host == "" {
    // Get a list of all host and routes currently connected
    if body, err := sendRequest(nil); err != nil {
      println(err.Error())
    } else {
      println(body)
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

  // Send the form
  form = url.Values{"route": {*route}, "host": {*host}}
  if body, err := sendRequest(form); err != nil {
    println(err.Error())
  } else {
    println(body)
  }
}

func sendRequest(form url.Values) (string, error) {
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

