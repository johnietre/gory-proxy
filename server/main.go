package main

import (
	"encoding/json"
	"log"
	"os"
	"path"
	"runtime"
)

type configuration struct {
	ProxyAddr     string
	ConnectorAddr string
	Password      string
}

var (
	config      configuration
	serverConns smap
	logger      *log.Logger
)

func init() {
	logger = log.New(os.Stdout, "", log.LstdFlags|log.Lshortfile)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		logger.Fatal("error getting source file")
	}
	thisDir := path.Dir(thisFile)

	logFile, err := os.OpenFile(
		path.Join(thisDir, "proxy.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0644,
	)
	if err != nil {
		logger.Fatal(err)
	}
	logger = log.New(logFile, "", log.LstdFlags|log.Lshortfile)

	parentDir := path.Dir(thisDir)
	configFile, err := os.Open(path.Join(parentDir, "config", "congif.json"))
	if err != nil {
		logger.Fatal(err)
	}
	defer configFile.Close()
	if err := json.NewDecoder(configFile).Decode(&config); err != nil {
		logger.Fatal(err)
	}
}

func main() {
	if config.ProxyAddr == "" {
		logger.Fatal("must provide proxy address")
	} else if config.ConnectorAddr == "" {
		logger.Fatal("must provide connector address")
	}

	// go pageChecker()
	go listenForServers()
	go pinger()
	log.Fatal(startProxy())
}
