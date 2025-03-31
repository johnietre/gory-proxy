package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/johnietre/gory-proxy/server"
	jtutils "github.com/johnietre/utils/go"
	"github.com/spf13/cobra"
)

func main() {
	log.SetFlags(0)

	cmd := &cobra.Command{
		Use:                   "gory-proxy",
		DisableFlagsInUseLine: true,
	}
	cmd.AddCommand(makeServerCmd(), makeClientCmd())
	if err := cmd.Execute(); err != nil {
		log.SetFlags(0)
		log.SetOutput(os.Stderr)
		log.Fatal(err)
	}
}

func makeServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "proxy",
		Run:                   runServer,
		DisableFlagsInUseLine: true,
	}
	flags := cmd.Flags()

	flags.String("addr", "127.0.0.1:8000", "Address to run the server on")
	flags.String("tunnel", "", "Address to connect tunnel to")
	flags.String("name",
		"",
		"Name of the server displayed on the tunneled-to proxy (must have tunnel flag",
	)
	flags.String(
		"path",
		"",
		"Path of the server on the tunneled-to proxy (must have tunnel flag",
	)
	flags.Bool("hidden", false, "Whether the tunnel server should be hidden")
	flags.String("cert", "", "Path to cert file for TLS")
	flags.String("key", "", "Path to key file for TLS")
	cmd.MarkFlagsRequiredTogether("cert", "key")
	cmd.MarkFlagsRequiredTogether("name", "path")

	return cmd
}

func runServer(cmd *cobra.Command, _ []string) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		server.Logger.Fatal("error getting log directory")
	}
	server.LogFilePath = filepath.Join(filepath.Dir(thisFile), "proxy.log")
	f, err := os.OpenFile(server.LogFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		server.Logger.Fatal(err)
	}
	server.Logger.SetOutput(f)

	flags := cmd.Flags()
	addr := jtutils.Must(flags.GetString("addr"))
	tunnelAddr := jtutils.Must(flags.GetString("tunnel"))
	tunnelSrvr := &server.Server{
		Name: jtutils.Must(flags.GetString("name")),
		Path: jtutils.Must(flags.GetString("path")),
	}
	certPath := jtutils.Must(flags.GetString("cert"))
	keyPath := jtutils.Must(flags.GetString("key"))

	if keyPath != "" {
		if _, err := os.Stat(keyPath); err != nil {
			log.Fatal("error checking key file: ", err)
		} else if _, err = os.Stat(certPath); err != nil {
			log.Fatal("error checking cert file: ", err)
		}
	}

	var r *server.Router
	if tunnelAddr != "" {
		if tunnelSrvr.Name == "" || tunnelSrvr.Path == "" {
			fmt.Fprintln(os.Stderr, "must provide name and path when tunneling")
			return
		}
		log.Println("attempting tunneling to", tunnelAddr)
		r, err = server.NewTunneledRouter(addr, tunnelAddr, tunnelSrvr)
	} else {
		r, err = server.NewRouter(addr)
	}
	if err != nil {
		server.Logger.Fatal(err)
	}
	s := &http.Server{
		Handler:  r,
		ErrorLog: server.Logger,
	}
	log.Println("starting proxy on", addr)
	if keyPath != "" {
		server.Logger.Fatal(s.ServeTLS(r, certPath, keyPath))
	} else {
		server.Logger.Fatal(s.Serve(r))
	}
}

func makeClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "client",
		Run:                   runClient,
		DisableFlagsInUseLine: true,
	}
	flags := cmd.Flags()

	flags.String("name", "", "Name of the server")
	flags.String("path", "", "Path of the server")
	flags.String("addr", "", "Addr of the server (include proto)")
	flags.Bool("hidden", false, "Whether the server is hidden or not")
	flags.String("server", "127.0.01:8000", "Addr of the server to send to (include proto)")
	flags.Bool("del", false, "Send delete request")
	flags.Bool("skip-verify", false, "Skip verifying server's certificate")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("path")
	cmd.MarkFlagRequired("addr")

	return cmd
}

func runClient(cmd *cobra.Command, _ []string) {
	type Server struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Addr   string `json:"addr"`
		Hidden bool   `json:"hidden"`
	}

	flags := cmd.Flags()

	srvr := Server{
		Name:   jtutils.Must(flags.GetString("name")),
		Path:   jtutils.Must(flags.GetString("path")),
		Addr:   jtutils.Must(flags.GetString("addr")),
		Hidden: jtutils.Must(flags.GetBool("hidden")),
	}
	server := jtutils.Must(flags.GetString("server"))
	del := jtutils.Must(flags.GetBool("del"))
	skipVerify := jtutils.Must(flags.GetBool("skip-verify"))

	if srvr.Name == "" || srvr.Path == "" || srvr.Addr == "" {
		log.Fatal("must provide name, path, and addr")
	}
	// Encode the server
	b := bytes.NewBuffer(nil)
	json.NewEncoder(b).Encode(srvr)
	// Create the request
	var method string
	if !del {
		method = http.MethodPost
	} else {
		method = http.MethodDelete
	}
	req, err := http.NewRequest(method, server, b)
	if err != nil {
		log.Fatal(err)
	}
	// Sendn the request and get the response
	var client *http.Client
	if skipVerify {
		client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
	} else {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	// Check the response
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("received non-OK status '%s' with body: %s", resp.Status, body)
	}
}
