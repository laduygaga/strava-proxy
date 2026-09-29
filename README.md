# Strava API Proxy Server

A lightweight Go proxy server for Strava API (`https://www.strava.com/api/v3`) that allows dynamic token updating, persistence, and auto-refreshing.

## Features

- **Automatic Bearer Token Injection**: Injects stored Strava access token into proxy requests.
- **Dynamic Token Updates**: Update token on-the-fly via `POST /token` without restarting server.
- **Persistence**: Persists tokens to `tokens.json` automatically.
- **OAuth Token Refresh Support**: Auto-refreshes expired access tokens using `POST /token/refresh` with Strava OAuth endpoint.
- **Transparent Path Mapping**: Automatically maps requests like `/athlete/activities` or `/api/v3/athlete/activities` to `https://www.strava.com/api/v3/...`.
- **Default Query Parameters**: Automatically sets `page=1` and `per_page=1` when omitted.
- **Response Field Filtering**: Automatically filters activity responses to return only `distance` and `average_speed`.

## Quick Start

### 1. Build and Run

```bash
# Build
go build -o strava-proxy main.go

# Run (default port 9090)
./strava-proxy
```

Or set initial token via environment variable:
```bash
STRAVA_ACCESS_TOKEN="ca5c2d3096460b49c0caf893fd7002efb8384ef2" ./strava-proxy
```

---

### 2. Update Token Dynamic Endpoint

Update your Strava Access Token anytime:

```bash
curl -X POST http://localhost:9090/token \
  -H "Content-Type: application/json" \
  -d '{
    "access_token": "ca5c2d3096460b49c0caf893fd7002efb8384ef2",
    "refresh_token": "YOUR_REFRESH_TOKEN",
    "client_id": "YOUR_CLIENT_ID",
    "client_secret": "YOUR_CLIENT_SECRET"
  }'
```

Get current token info:
```bash
curl http://localhost:9090/token
```

Refresh token with Strava API automatically (when client_id, client_secret, refresh_token are configured):
```bash
curl -X POST http://localhost:9090/token/refresh
```

---

### 3. Make Proxied Strava Requests

Now you can make calls through the proxy without passing `Authorization` header manually:

```bash
curl -G "http://localhost:9090/athlete/activities" \
  -d page=1 -d per_page=1
```

Or using full path:

```bash
curl -G "http://localhost:9090/api/v3/athlete/activities" \
  -d page=1 -d per_page=1
```
