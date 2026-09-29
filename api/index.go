package handler

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

	query := r.URL.Query()
	realPath := ""

	if p := query.Get("__proxy_path"); p != "" {
		realPath = p
		query.Del("__proxy_path")
		r.URL.RawQuery = query.Encode()
	} else if invokePath := r.Header.Get("x-invoke-path"); invokePath != "" {
		realPath = invokePath
	} else if fwdURI := r.Header.Get("x-forwarded-uri"); fwdURI != "" {
		if parsed, err := url.Parse(fwdURI); err == nil && parsed.Path != "" && parsed.Path != "/api/index" {
			realPath = parsed.Path
		}
	}

	if realPath != "" && realPath != "/api/index" {
		if !strings.HasPrefix(realPath, "/") {
			realPath = "/" + realPath
		}
		r.URL.Path = realPath
	}

	mux.ServeHTTP(w, r)
}
