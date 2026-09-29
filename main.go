package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"strava-proxy/pkg/proxy"
)

func main() {
	host := flag.String("host", "0.0.0.0", "Host/interface to bind to")
	port := flag.Int("port", 9090, "Port to listen on")
	tokenFile := flag.String("token-file", proxy.DefaultTokenFile, "Path to token persistence file")
	flag.Parse()

	if envHost := os.Getenv("HOST"); envHost != "" {
		*host = envHost
	}

	if envPort := os.Getenv("PORT"); envPort != "" {
		fmt.Sscanf(envPort, "%d", port)
	}

	tokenStore := proxy.NewTokenStore(*tokenFile)
	server, err := proxy.NewServer(tokenStore)
	if err != nil {
		slog.Error("Failed to create server", "error", err)
		os.Exit(1)
	}

	mux := server.SetupRoutes()
	addr := fmt.Sprintf("%s:%d", *host, *port)

	slog.Info("Strava Proxy Server running", "addr", addr, "token_file", *tokenFile)
	if tokenStore.GetAccessToken() == "" {
		slog.Warn("No Strava access_token configured! Set it via POST /token or STRAVA_ACCESS_TOKEN env var.")
	} else {
		slog.Info("Loaded Strava access_token")
	}

	if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
