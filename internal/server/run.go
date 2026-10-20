// Package server is the headless 1loop: no display, the agent and its
// tools served to paired phones and desktops over HTTPS.
package server

// The headless server's entry point (make server; see docs/SERVER.md). It serves
// the phone and desktop clients over HTTPS and pairs them from its console.

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

// Main runs the headless server.
func Main() {
	workspace := flag.String("workspace", ".", "Workspace allowed for remote conversations")
	listen := flag.String("listen", ":47101", "HTTPS listen address")
	dir := flag.String("data-dir", "", "Private server state directory")
	settings := flag.String("config", "", "Server connection configuration JSON")
	provider := flag.String("provider", "", "Provider for the initial connection")
	base := flag.String("base-url", "", "API base URL for the initial connection")
	model := flag.String("model", "", "Initial model ID")
	keyEnv := flag.String("api-key-env", "", "Environment variable containing the initial API key")
	cert := flag.String("tls-cert", "", "PEM certificate (optional; default generates a private certificate)")
	key := flag.String("tls-key", "", "PEM private key for -tls-cert")
	localToken := flag.String("local-client-token-file", "", "Private local-phone client token; requires a loopback listen address")
	localReady := flag.String("local-ready-file", "", "Private local-phone startup endpoint JSON")
	localBridge := flag.String("local-bridge-config", "", "Private local-phone native bridge configuration")
	installDebian := flag.String("install-mobile-debian", "", "Install the pinned mobile Debian filesystem into a new directory, then exit")
	installClaude := flag.Bool("install-mobile-claude", false, "Install Claude Code from its signed Debian repository, then exit")
	flag.Parse()
	if *installClaude {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := installMobileClaude(ctx); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *installDebian != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := installMobileDebian(ctx, *installDebian); err != nil {
			log.Fatal(err)
		}
		return
	}
	if (*localToken == "") != (*localReady == "") {
		log.Fatal("Local phone startup requires both -local-client-token-file and -local-ready-file")
	}
	if *localBridge != "" && *localToken == "" {
		log.Fatal("The native phone bridge is only available in local phone mode")
	}
	root, err := agent.ResolveWorkspace(*workspace)
	if err != nil {
		log.Fatal(err)
	}
	if *dir == "" {
		home, err := os.UserConfigDir()
		if err != nil {
			log.Fatal(err)
		}
		*dir = filepath.Join(home, "1loop-server")
	}
	if err = os.MkdirAll(*dir, 0700); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, err := newHeadlessServer(ctx, root, *dir, *settings)
	if err != nil {
		log.Fatal(err)
	}
	server.shutdown = stop
	if *localToken != "" {
		if err := server.authorizeLocalClient(*localToken, *listen); err != nil {
			log.Fatal(err)
		}
	}
	if *localBridge != "" {
		server.bridge, err = loadMobileBridge(*localBridge)
		if err != nil {
			log.Fatal(err)
		}
	}
	if server.bridge != nil {
		server.engine.Native = &mobileBrowser{bridge: server.bridge}
	}
	if *provider != "" || *model != "" || *base != "" {
		if *provider != "" {
			server.cfg.Provider = *provider
		}
		if *model != "" {
			server.cfg.Model = *model
		}
		if *base != "" {
			server.cfg.BaseURL = *base
		}
		existing := ""
		for _, conn := range server.connections {
			if conn.Provider == server.cfg.Provider && conn.BaseURL == server.cfg.BaseURL {
				existing = conn.ID
				break
			}
		}
		if existing == "" {
			existing = core.Identifier()
		}
		server.connections = []agent.Connection{{
			ID:        existing,
			Name:      agent.ProviderName(server.cfg.Provider),
			Provider:  server.cfg.Provider,
			BaseURL:   server.cfg.BaseURL,
			Enabled:   true,
			AllModels: true,
		}}
		server.cfg.ConnectionID = existing
	}
	if err = server.saveSettingsLocked(); err != nil {
		log.Fatal(err)
	}
	if *keyEnv != "" {
		value, ok := os.LookupEnv(*keyEnv)
		if !ok {
			log.Fatal("API key environment variable is not set")
		}
		server.keys[server.cfg.ConnectionID] = value
	}
	certificate, err := remoteapi.Certificate(*dir, *cert, *key)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	server.handler = server.routes()
	server.fingerprint = remoteapi.Fingerprint(certificate)
	httpServer := &http.Server{
		Handler:           server.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}},
	}
	server.tlsConfig = httpServer.TLSConfig
	fmt.Fprintf(os.Stderr, "1loop server listening on https://%s\nCertificate SHA-256: %s\nWorkspace: %s\nCommands: pair <id> <code>, devices, revoke <number>\n", listener.Addr(), remoteapi.Fingerprint(certificate), root)
	if server.local {
		if err := saveServerJSON(*localReady, map[string]any{
			"port":        listener.Addr().(*net.TCPAddr).Port,
			"fingerprint": remoteapi.Fingerprint(certificate),
		}); err != nil {
			log.Fatal(err)
		}
	} else {
		go server.console()
	}
	go server.loadModels()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
	}()
	err = httpServer.Serve(tls.NewListener(listener, httpServer.TLSConfig))
	server.close()
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
