package handler

import (
	"net/http"
	"net/url"
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

	if fwdURI := r.Header.Get("x-forwarded-uri"); fwdURI != "" {
		if parsed, err := url.Parse(fwdURI); err == nil && parsed.Path != "" && parsed.Path != "/api/index" {
			r.URL.Path = parsed.Path
			if parsed.RawQuery != "" && r.URL.RawQuery == "" {
				r.URL.RawQuery = parsed.RawQuery
			}
		}
	}

	mux.ServeHTTP(w, r)
}
