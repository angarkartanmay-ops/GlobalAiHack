// Command heimdall watches a Bifrost gateway for models that are about to
// be retired, proves a replacement on real traffic, and cuts over.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/mcpserver"
	"github.com/angarkartanmay-ops/globalaihack/internal/server"
	"github.com/angarkartanmay-ops/globalaihack/web"
)

var version = "dev"

const usage = `heimdall - model retirement watch for the Bifrost AI gateway

Usage:
  heimdall serve       Run the watcher, API and dashboard (default)
  heimdall bootstrap   Write a Bifrost config.json for this environment and
                       print the environment both processes should run with
  heimdall version     Print the version

Configuration is read from the environment. See README.md for every setting.
`

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		serve()
	case "bootstrap":
		bootstrap(args)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func serve() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	server.Version = version
	cfg := server.LoadConfig()
	log.Info("heimdall starting", "version", version, "config", cfg.Describe())
	for _, p := range cfg.Validate() {
		log.Warn("configuration: " + p)
	}

	app, err := server.New(cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer app.Close()

	var ui fs.FS
	if sub, err := fs.Sub(web.Dist, "dist"); err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			ui = sub
		}
	}
	srv := &http.Server{Addr: cfg.Addr, Handler: app.Handler(ui, mcpserver.New(app)), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	app.Start(ctx)

	go func() {
		log.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server stopped", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	log.Info("heimdall stopped")
}

func bootstrap(args []string) {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	dir := fs.String("gateway-dir", "./gateway-data", "Bifrost app directory to write config.json into")
	mode := fs.String("mode", os.Getenv("HEIMDALL_MODE"), "sim or live (default: live when a provider key is set, otherwise sim)")
	heimdallURL := fs.String("heimdall-url", "http://127.0.0.1:8787", "URL at which the gateway reaches Heimdall (simulator endpoints)")
	gatewayURL := fs.String("gateway-url", "http://127.0.0.1:8080", "URL at which Heimdall reaches the gateway")
	budget := fs.Float64("budget", 5, "daily USD budget for Heimdall's virtual key on the gateway")
	_ = fs.Parse(args)

	res, err := server.Bootstrap(server.BootstrapOptions{Dir: *dir, Mode: *mode, HeimdallURL: *heimdallURL, GatewayURL: *gatewayURL, BudgetUSD: *budget})
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		os.Exit(1)
	}
	// Notes go to stderr so that stdout can be eval'd by a shell.
	fmt.Fprintf(os.Stderr, "heimdall bootstrap: mode=%s providers=%v tavily=%v config=%s/config.json\n", res.Mode, res.Providers, res.Tavily, *dir)
	for _, n := range res.Notes {
		fmt.Fprintln(os.Stderr, "heimdall bootstrap:", n)
	}
	fmt.Print(res.ShellExports())
}
