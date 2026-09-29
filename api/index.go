package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"strava-proxy/pkg/proxy"
)

var (
	mux  *http.ServeMux
	once sync.Once
)

func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(func() {
		tokenFile := os.Getenv("TOKEN_FILE")
		if tokenFile == "" {
			tokenFile = filepath.Join(os.TempDir(), "tokens.json")
		}

		store := proxy.NewTokenStore(tokenFile)
		server, err := proxy.NewServer(store)
		if err != nil {
			return
		}
		mux = server.SetupRoutes()
	})

	if mux == nil {
		http.Error(w, "server initialization failed", http.StatusInternalServerError)
		return
	}

	mux.ServeHTTP(w, r)
}
