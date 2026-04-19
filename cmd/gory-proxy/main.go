package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caddyserver/certmagic"
	proxy "github.com/johnietre/gory-proxy"
	jtutils "github.com/johnietre/utils/go"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	noReuseAddr bool
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
		Use:   "proxy [ADDR (default: 127.0.0.1:8000)]",
		Run:   runServer,
		Short: "Run the proxy",
		//DisableFlagsInUseLine: true,
	}
	flags := cmd.Flags()

	flags.String("addr", "127.0.0.1:8000", "Address to run the server on")
	flags.String("tunnel", "", "Address to connect tunnel to")
	flags.String("name",
		"",
		"Name of the server displayed on the tunneled-to proxy (must have tunnel flag)",
	)
	flags.String(
		"path",
		"",
		"Path of the server on the tunneled-to proxy (must have tunnel flag)",
	)
	flags.Bool("hidden", false, "Whether the tunnel server should be hidden")
	flags.String("cert", "", "Path to cert file for TLS")
	flags.String("key", "", "Path to key file for TLS")
	flags.StringVar(
		&proxy.LogFilePath,
		"log",
		"",
		"Path to log file (empty means stderr)",
	)
	flags.DurationVar(
		&proxy.TunnelConnectTimeout,
		"tunnel-connect-timeout",
		0,
		"Max time taken to connect a tunnel (<= 0 means no timeout)",
	)
	flags.String("save-to", "", "Path to save servers to")
	flags.String("load-from", "", "Path to load servers from")
	flags.String(
		"servers",
		"",
		"Path to save/load servers to/from (specifying --save-to/--load-from overwrites the appropriate function this flag affects)",
	)
	flags.Bool(
		"autotls",
		false,
		"TLS automation through certmagic (provide domain name(s) rather than IP:PORT address); the email address used to for the ACME server account can be specified with the GORY_PROXY_ACME_EMAIL environment variable",
	)
	flags.BoolVar(
		&noReuseAddr,
		"no-reuse-addr",
		false,
		"Disable setting SO_REUSEADDR",
	)

	cmd.MarkFlagsRequiredTogether("cert", "key")
	cmd.MarkFlagsRequiredTogether("name", "path")
	flags.MarkDeprecated("addr", "pass as command-line argument")

	return cmd
}

func runServer(cmd *cobra.Command, _ []string) {
	ctx := context.Background()

	if proxy.LogFilePath != "" {
		f, err := os.OpenFile(proxy.LogFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}

		w := zapcore.Lock(f)
		proxy.ZapLogger = zap.New(zapcore.NewCore(
			zapcore.NewConsoleEncoder(zap.NewProductionEncoderConfig()),
			w,
			zap.InfoLevel,
		))

		proxy.Logger.SetOutput(w)
	}

	flags := cmd.Flags()
	addr := jtutils.Must(flags.GetString("addr"))
	if len(flags.Args()) != 0 {
		addr = flags.Arg(0)
	}
	rc := proxy.RouterConfig{
		LnFunc: func() (net.Listener, error) {
			lc := newListenConfig()
			return lc.Listen(ctx, "tcp", addr)
		},
		TunnelAddr: jtutils.Must(flags.GetString("tunnel")),
		TunnelServer: &proxy.Server{
			Name: jtutils.Must(flags.GetString("name")),
			Path: jtutils.Must(flags.GetString("path")),
		},
	}

	certPath := jtutils.Must(flags.GetString("cert"))
	keyPath := jtutils.Must(flags.GetString("key"))

	servers := jtutils.Must(flags.GetString("servers"))
	rc.LoadFrom, rc.SaveTo = servers, servers
	loadFromFlag := flags.Lookup("load-from")
	if loadFromFlag.Changed {
		rc.LoadFrom = loadFromFlag.Value.String()
	}
	saveToFlag := flags.Lookup("save-to")
	if saveToFlag.Changed {
		rc.SaveTo = saveToFlag.Value.String()
	}

	autotls := jtutils.Must(flags.GetBool("autotls"))

	if keyPath != "" {
		if _, err := os.Stat(keyPath); err != nil {
			log.Fatal("error checking key file: ", err)
		} else if _, err = os.Stat(certPath); err != nil {
			log.Fatal("error checking cert file: ", err)
		}
	}

	if rc.TunnelAddr != "" {
		if rc.TunnelServer.Name == "" || rc.TunnelServer.Path == "" {
			log.Fatal("must provide name and path when tunneling")
		}
		//log.Println("attempting tunneling to", tunnelAddr)
	}

	var httpSrvr *http.Server
	if autotls {
		domains := flags.Args()
		acmeEmail := os.Getenv("GORY_PROXY_ACME_EMAIL")
		if acmeEmail == "" {
			proxy.Logger.Print("GORY_PROXY_ACME_EMAIL is empty")
		}

		// Most of this code was copied from certmagic.HTTPS/certmagic.TLS functions.
		certCfg := certmagic.NewDefault()

		rc.LnFunc = func() (net.Listener, error) {
			var tlsConf *tls.Config
			certmagic.DefaultACME.Agreed = true
			certmagic.DefaultACME.Email = acmeEmail
			if proxy.ZapLogger != nil {
				certmagic.DefaultACME.Logger = proxy.ZapLogger
				certmagic.Default.Logger = proxy.ZapLogger
			}
			tlsConf = certCfg.TLSConfig()
			if err := certCfg.ManageSync(ctx, domains); err != nil {
				return nil, err
			}
			tlsConf.NextProtos = append(
				[]string{"h2", "http/1.1"},
				tlsConf.NextProtos...,
			)
			lc := newListenConfig()
			ln, err := lc.Listen(ctx, "tcp", ":443")
			if err != nil {
				return nil, err
			}
			ln = tls.NewListener(ln, tlsConf)
			return ln, err
		}

		httpSrvr = &http.Server{
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      5 * time.Second,
			IdleTimeout:       5 * time.Second,
			BaseContext:       func(ln net.Listener) context.Context { return ctx },
		}
		// NOTE: I don't believe any other function modifies Issuers after the cfg
		// is created.
		if len(certCfg.Issuers) != 0 {
			if am, ok := certCfg.Issuers[0].(*certmagic.ACMEIssuer); ok {
				httpSrvr.Handler = am.HTTPChallengeHandler(http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						reqHost, _, err := net.SplitHostPort(r.Host)
						if err != nil {
							reqHost = r.Host
						}
						to := reqHost + r.URL.RequestURI()
						w.Header().Set("Connection", "close")
						http.Redirect(w, r, to, http.StatusMovedPermanently)
					},
				))
			}
		}

		addr = ":443"
	}

	r, err := rc.Create()
	if err != nil {
		log.Fatal(err)
	}

	errCh := jtutils.NewUChan[error](2)

	if httpSrvr != nil {
		lc := newListenConfig()
		httpLn, err := lc.Listen(ctx, "tcp", ":80")
		if err != nil {
			log.Fatalf("error starting HTTP listener: %v", err)
		}

		go func() {
			errCh.Send(fmt.Errorf("http server error: %w", httpSrvr.Serve(httpLn)))
		}()
	}
	proxySrvr := &http.Server{
		Handler:  r,
		ErrorLog: proxy.Logger,
	}
	if keyPath != "" {
		log.Println("starting proxy on", "https://"+addr)
		go func() {
			errCh.Send(proxySrvr.ServeTLS(r, certPath, keyPath))
		}()
	} else {
		if autotls {
			log.Println("starting proxy on", "https://"+addr)
		} else {
			log.Println("starting proxy on", "http://"+addr)
		}
		go func() {
			errCh.Send(proxySrvr.Serve(r))
		}()
	}

	interruptChan := make(chan os.Signal, 5)
	go func() {
		<-interruptChan
		errCh.Send(nil)
		proxy.Logger.Print("shutting down")
		go func() {
			if httpSrvr != nil {
				httpSrvr.Shutdown(context.Background())
			}
			proxySrvr.Shutdown(context.Background())
			errCh.Close()
		}()

		<-interruptChan
		proxy.Logger.Print("forcing shut down")
		if httpSrvr != nil {
			httpSrvr.Close()
		}
		proxySrvr.Close()
		errCh.Close()
	}()
	signal.Notify(interruptChan, os.Interrupt)

	// nil error means shutting down
	if err, _ := errCh.Recv(); err != nil {
		proxy.Logger.Printf("fatal error (shutting down): %v", err)
		/*
		   if httpSrvr != nil {
		     httpSrvr.Shutdown(context.Background())
		   }
		   proxySrvr.Shutdown(context.Background())
		*/
		if httpSrvr != nil {
			httpSrvr.Close()
		}
		proxySrvr.Close()
		os.Exit(1)
	}
	// Empty chan and wait for close (means servers are done)
	for {
		_, ok := errCh.Recv()
		if !ok {
			break
		}
	}
	proxy.Logger.Print("done")
}

func makeClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client <SERVER_URL>",
		Run:   runClient,
		Short: "Send a command to a gory-proxy client (SERVER_URL must include proto)",
		//DisableFlagsInUseLine: true,
	}
	flags := cmd.Flags()

	flags.String("name", "", "Name of the server")
	flags.String("path", "", "Path of the server")
	flags.String("addr", "", "Addr of the server (include proto)")
	flags.Bool("hidden", false, "Whether the server is hidden or not")
	flags.String("server", "", "Addr of the server to send to (include proto)")
	flags.Bool("del", false, "Send delete request")
	flags.Bool("skip-verify", false, "Skip verifying server's certificate")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("path")
	cmd.MarkFlagRequired("addr")
	flags.MarkDeprecated("server", "pass as command-line argument")

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
	server := jtutils.Must(flags.GetString("server"))
	if len(flags.Args()) != 0 {
		server = flags.Arg(0)
	}

	srvr := Server{
		Name:   jtutils.Must(flags.GetString("name")),
		Path:   jtutils.Must(flags.GetString("path")),
		Addr:   jtutils.Must(flags.GetString("addr")),
		Hidden: jtutils.Must(flags.GetBool("hidden")),
	}
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

func newListenConfig() net.ListenConfig {
	return net.ListenConfig{
		Control: func(network, addr string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				if err := syscall.SetsockoptInt(
					int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1,
				); err != nil {
					proxy.Logger.Fatalf("error setting SO_REUSEADDR: %v", err)
				}
			})
		},
	}
}
