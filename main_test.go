package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strava-proxy/pkg/proxy"
)

func TestTokenStore(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "test_tokens.json")

	store := proxy.NewTokenStore(tokenFile)

	if token := store.GetAccessToken(); token != "" {
		t.Fatalf("expected empty access token initially, got: %s", token)
	}

	testToken := "ca5c2d3096460b49c0caf893fd7002efb8384ef2"
	err := store.UpdateConfig(proxy.TokenConfig{
		AccessToken:  testToken,
		RefreshToken: "ref_123",
	})
	if err != nil {
		t.Fatalf("failed to update config: %v", err)
	}

	if token := store.GetAccessToken(); token != testToken {
		t.Fatalf("expected token %s, got %s", testToken, token)
	}

	if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
		t.Fatalf("token file was not created at %s", tokenFile)
	}

	newStore := proxy.NewTokenStore(tokenFile)
	if token := newStore.GetAccessToken(); token != testToken {
		t.Fatalf("expected loaded token %s, got %s", testToken, token)
	}
}

func TestServerEndpoints(t *testing.T) {
	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "tokens.json")

	store := proxy.NewTokenStore(tokenFile)
	server, err := proxy.NewServer(store)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	mux := server.SetupRoutes()

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected health status 200, got %d", rec.Code)
	}

	updateBody := `{"access_token": "ca5c2d3096460b49c0caf893fd7002efb8384ef2", "refresh_token": "refresh_abc"}`
	req = httptest.NewRequest("POST", "/token", strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/token", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected get token status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON from GET /token: %v", err)
	}

	if resp["access_token"] != "ca5c2d3096460b49c0caf893fd7002efb8384ef2" {
		t.Fatalf("expected token ca5c2d3096460b49c0caf893fd7002efb8384ef2, got %v", resp["access_token"])
	}
}

func TestProxyHeaderRewrite(t *testing.T) {
	var receivedAuthHeader string
	var receivedPath string
	var receivedQuery string

	mockStrava := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		receivedPath = r.URL.Path
		receivedQuery = r.URL.RawQuery

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id": 12345, "name": "Morning Ride", "distance": 10500.5, "average_speed": 6.8}]`))
	}))
	defer mockStrava.Close()

	tempDir := t.TempDir()
	tokenFile := filepath.Join(tempDir, "tokens.json")
	store := proxy.NewTokenStore(tokenFile)

	testToken := "ca5c2d3096460b49c0caf893fd7002efb8384ef2"
	_ = store.UpdateConfig(proxy.TokenConfig{AccessToken: testToken})

	server, err := proxy.NewServer(store)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	proxyEngine := server.GetProxy()
	origRewrite := proxyEngine.Rewrite
	proxyEngine.Rewrite = func(r *httputil.ProxyRequest) {
		origRewrite(r)
		target, _ := url.Parse(mockStrava.URL)
		r.SetURL(target)
		r.Out.Host = target.Host
	}

	mux := server.SetupRoutes()

	req := httptest.NewRequest("GET", "/athlete/activities", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("proxy request failed with status %d: %s", rec.Code, rec.Body.String())
	}

	if receivedAuthHeader != "Bearer "+testToken {
		t.Fatalf("expected Authorization 'Bearer %s', got '%s'", testToken, receivedAuthHeader)
	}

	if receivedPath != "/api/v3/athlete/activities" {
		t.Fatalf("expected path '/api/v3/athlete/activities', got '%s'", receivedPath)
	}

	if !strings.Contains(receivedQuery, "page=1") || !strings.Contains(receivedQuery, "per_page=1") {
		t.Fatalf("expected query to contain default page=1 and per_page=1, got '%s'", receivedQuery)
	}

	respBody := rec.Body.String()
	if !strings.Contains(respBody, "distance") || !strings.Contains(respBody, "average_speed") {
		t.Fatalf("expected response body to contain distance and average_speed, got: %s", respBody)
	}
	if strings.Contains(respBody, "Morning Ride") || strings.Contains(respBody, "12345") {
		t.Fatalf("expected response body to filter out name and id, got: %s", respBody)
	}
}

func TestVercelRewrittenPath(t *testing.T) {
	var receivedPath string
	var receivedQuery string

	mockStrava := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer mockStrava.Close()

	tempDir := t.TempDir()
	store := proxy.NewTokenStore(filepath.Join(tempDir, "tokens.json"))
	server, _ := proxy.NewServer(store)

	proxyEngine := server.GetProxy()
	origRewrite := proxyEngine.Rewrite
	proxyEngine.Rewrite = func(r *httputil.ProxyRequest) {
		origRewrite(r)
		target, _ := url.Parse(mockStrava.URL)
		r.SetURL(target)
		r.Out.Host = target.Host
	}

	mux := server.SetupRoutes()

	req1 := httptest.NewRequest("GET", "/api/index", nil)
	req1.Header.Set("x-forwarded-uri", "/athlete/activities")
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)

	if receivedPath != "/api/v3/athlete/activities" {
		t.Fatalf("expected path '/api/v3/athlete/activities', got '%s'", receivedPath)
	}

	req2 := httptest.NewRequest("GET", "/api/index?__proxy_path=/athlete/activities", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if receivedPath != "/api/v3/athlete/activities" {
		t.Fatalf("expected path '/api/v3/athlete/activities', got '%s'", receivedPath)
	}
	if strings.Contains(receivedQuery, "__proxy_path") {
		t.Fatalf("expected __proxy_path to be removed from upstream query, got '%s'", receivedQuery)
	}
}
