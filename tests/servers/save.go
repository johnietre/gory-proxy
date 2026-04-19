package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/johnietre/gory-proxy"
)

func main() {
	addr := "127.0.0.1:8000"
	proxy := goryproxy.NewRouterHandler()
	proxy.SaveOnChangeTo("saved-servers.json")
	srvrs := map[string]*goryproxy.Server{}
	bytes, err := os.ReadFile("saved-servers.json")
	if err != nil {
		if os.IsNotExist(err) {
			log.Print("file doesn't exist, creating new servers")
			srvrs["Test1"] = &goryproxy.Server{
				Name: "Test1",
				Addr: "http://127.0.0.1:8080",
				Path: "test1",
			}
		} else {
			log.Fatal("error opening file: ", err)
		}
	} else {
		if err = json.Unmarshal(bytes, &srvrs); err != nil {
			log.Fatal("error parsing json: ", err)
		}
		log.Print("loaded servers from file")
	}
	for _, srvr := range srvrs {
		if err := srvr.AddNewProxyFromAddr(); err != nil {
			log.Fatalf(
				"error adding proxy for server (%s) with url %s: %v",
				srvr.Name, srvr.Addr, err,
			)
		}
	}
	errs, err := proxy.AddServers(srvrs)
	for name, err := range errs {
		if err != nil {
			log.Fatal("error adding server (%s): %v", name, err)
		}
	}
	if err != nil {
		log.Fatal("error saving servers: %v", err)
	}
	log.Fatal(http.ListenAndServe(addr, proxy))
}
