package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const DefaultStravaTarget = "https://www.strava.com"
const DefaultTokenFile = "tokens.json"

type TokenConfig struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
}

type TokenStore struct {
	mu       sync.RWMutex
	config   TokenConfig
	filePath string
}

func NewTokenStore(filePath string) *TokenStore {
	store := &TokenStore{
		filePath: filePath,
	}

	if err := store.Load(); err != nil {
		tmpPath := filepath.Join(os.TempDir(), "tokens.json")
		if tmpData, errTmp := os.ReadFile(tmpPath); errTmp == nil {
			var cfg TokenConfig
			if json.Unmarshal(tmpData, &cfg) == nil {
				store.config = cfg
				store.filePath = tmpPath
			}
		}
	}

	return store
}

func (s *TokenStore) GetConfig() TokenConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := s.config
	if cfg.AccessToken == "" {
		cfg.AccessToken = os.Getenv("STRAVA_ACCESS_TOKEN")
	}
	if cfg.RefreshToken == "" {
		cfg.RefreshToken = os.Getenv("STRAVA_REFRESH_TOKEN")
	}
	if cfg.ClientID == "" {
		cfg.ClientID = os.Getenv("STRAVA_CLIENT_ID")
	}
	if cfg.ClientSecret == "" {
		cfg.ClientSecret = os.Getenv("STRAVA_CLIENT_SECRET")
	}

	return cfg
}

func (s *TokenStore) GetAccessToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.config.AccessToken != "" {
		return s.config.AccessToken
	}
	return os.Getenv("STRAVA_ACCESS_TOKEN")
}

func (s *TokenStore) UpdateConfig(cfg TokenConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.AccessToken != "" {
		s.config.AccessToken = cfg.AccessToken
	}
	if cfg.RefreshToken != "" {
		s.config.RefreshToken = cfg.RefreshToken
	}
	if cfg.ClientID != "" {
		s.config.ClientID = cfg.ClientID
	}
	if cfg.ClientSecret != "" {
		s.config.ClientSecret = cfg.ClientSecret
	}
	if cfg.ExpiresAt > 0 {
		s.config.ExpiresAt = cfg.ExpiresAt
	}
	if cfg.TokenType != "" {
		s.config.TokenType = cfg.TokenType
	}

	return s.saveLocked()
}

func (s *TokenStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	var cfg TokenConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	s.config = cfg
	return nil
}

func (s *TokenStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *TokenStore) saveLocked() error {
	if s.filePath == "" {
		return nil
	}

	data, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal token config: %w", err)
	}

	dir := filepath.Dir(s.filePath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return s.saveToTmpLocked(data)
		}
	}

	if err := os.WriteFile(s.filePath, data, 0600); err != nil {
		return s.saveToTmpLocked(data)
	}

	return nil
}

func (s *TokenStore) saveToTmpLocked(data []byte) error {
	tmpPath := filepath.Join(os.TempDir(), "tokens.json")
	if err := os.WriteFile(tmpPath, data, 0600); err == nil {
		s.filePath = tmpPath
	}
	return nil
}

type StravaRefreshResponse struct {
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (s *TokenStore) RefreshAccessToken() (*StravaRefreshResponse, error) {
	cfg := s.GetConfig()
	clientID := cfg.ClientID
	clientSecret := cfg.ClientSecret
	refreshToken := cfg.RefreshToken

	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return nil, errors.New("client_id, client_secret, and refresh_token are required to refresh token")
	}

	formData := url.Values{}
	formData.Set("client_id", clientID)
	formData.Set("client_secret", clientSecret)
	formData.Set("grant_type", "refresh_token")
	formData.Set("refresh_token", refreshToken)

	resp, err := http.Post("https://www.strava.com/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to send refresh request to Strava: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("strava refresh token failed with status %d: %s", resp.StatusCode, string(body))
	}

	var refreshResp StravaRefreshResponse
	if err := json.Unmarshal(body, &refreshResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal refresh response: %w", err)
	}

	err = s.UpdateConfig(TokenConfig{
		AccessToken:  refreshResp.AccessToken,
		RefreshToken: refreshResp.RefreshToken,
		ExpiresAt:    refreshResp.ExpiresAt,
		TokenType:    refreshResp.TokenType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update token store with refreshed token: %w", err)
	}

	return &refreshResp, nil
}

type Server struct {
	tokenStore *TokenStore
	proxy      *httputil.ReverseProxy
}

func NewServer(tokenStore *TokenStore) (*Server, error) {
	targetURL, err := url.Parse(DefaultStravaTarget)
	if err != nil {
		return nil, fmt.Errorf("invalid default target URL: %w", err)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(targetURL)
			r.Out.Host = targetURL.Host

			reqPath := r.In.URL.Path
			if !strings.HasPrefix(reqPath, "/api/v3") {
				if strings.HasPrefix(reqPath, "/v3") {
					reqPath = "/api" + reqPath
				} else {
					reqPath = "/api/v3" + reqPath
				}
			}
			r.Out.URL.Path = reqPath

			query := r.Out.URL.Query()
			if !query.Has("page") {
				query.Set("page", "1")
			}
			if !query.Has("per_page") {
				query.Set("per_page", "1")
			}
			r.Out.URL.RawQuery = query.Encode()

			accessToken := tokenStore.GetAccessToken()
			if accessToken != "" {
				r.Out.Header.Set("Authorization", "Bearer "+accessToken)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode != http.StatusOK {
				return nil
			}

			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			_ = resp.Body.Close()

			trimmed := bytes.TrimSpace(bodyBytes)
			var filteredBytes []byte

			if len(trimmed) > 0 && trimmed[0] == '[' {
				var rawItems []map[string]interface{}
				if err := json.Unmarshal(trimmed, &rawItems); err == nil {
					filteredList := make([]map[string]interface{}, len(rawItems))
					for i, item := range rawItems {
						filtered := make(map[string]interface{})
						if v, ok := item["distance"]; ok {
							filtered["distance"] = v
						}
						if v, ok := item["average_speed"]; ok {
							filtered["average_speed"] = v
						}
						filteredList[i] = filtered
					}
					filteredBytes, _ = json.Marshal(filteredList)
				} else {
					filteredBytes = bodyBytes
				}
			} else if len(trimmed) > 0 && trimmed[0] == '{' {
				var rawMap map[string]interface{}
				if err := json.Unmarshal(trimmed, &rawMap); err == nil {
					filtered := make(map[string]interface{})
					if v, ok := rawMap["distance"]; ok {
						filtered["distance"] = v
					}
					if v, ok := rawMap["average_speed"]; ok {
							filtered["average_speed"] = v
					}
					filteredBytes, _ = json.Marshal(filtered)
				} else {
					filteredBytes = bodyBytes
				}
			} else {
				filteredBytes = bodyBytes
			}

			resp.Body = io.NopCloser(bytes.NewReader(filteredBytes))
			resp.ContentLength = int64(len(filteredBytes))
			resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(filteredBytes)))
			return nil
		},
	}

	return &Server{
		tokenStore: tokenStore,
		proxy:      proxy,
	}, nil
}

func (srv *Server) GetProxy() *httputil.ReverseProxy {
	return srv.proxy
}

func (srv *Server) HandleGetToken(w http.ResponseWriter, r *http.Request) {
	cfg := srv.tokenStore.GetConfig()

	response := map[string]interface{}{
		"access_token":  cfg.AccessToken,
		"refresh_token": cfg.RefreshToken,
		"client_id":     cfg.ClientID,
		"has_secret":    cfg.ClientSecret != "",
		"expires_at":    cfg.ExpiresAt,
		"token_type":    cfg.TokenType,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (srv *Server) HandleUpdateToken(w http.ResponseWriter, r *http.Request) {
	var input TokenConfig
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, fmt.Sprintf("invalid json payload: %v", err), http.StatusBadRequest)
		return
	}

	if err := srv.tokenStore.UpdateConfig(input); err != nil {
		http.Error(w, fmt.Sprintf("failed to save token: %v", err), http.StatusInternalServerError)
		return
	}

	slog.Info("Token updated successfully", "expires_at", input.ExpiresAt)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Token updated successfully",
		"config":  srv.tokenStore.GetConfig(),
	})
}

func (srv *Server) HandleRefreshToken(w http.ResponseWriter, r *http.Request) {
	refreshed, err := srv.tokenStore.RefreshAccessToken()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to refresh token: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Token refreshed successfully with Strava API",
		"data":    refreshed,
	})
}

func (srv *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (srv *Server) SetupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /token", srv.HandleGetToken)
	mux.HandleFunc("POST /token", srv.HandleUpdateToken)
	mux.HandleFunc("PUT /token", srv.HandleUpdateToken)
	mux.HandleFunc("POST /token/refresh", srv.HandleRefreshToken)
	mux.HandleFunc("GET /health", srv.HandleHealth)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Proxying request", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery)
		srv.proxy.ServeHTTP(w, r)
	})

	return mux
}
