// Command validate checks environment configuration, API credentials, and
// connectivity for the Kalshi Ghost Trader service.
//
// It verifies:
//   - Environment variables (KALSHI_API_KEY_ID, KALSHI_PRIVATE_KEY_PATH, KALSHI_ENV)
//   - RSA key loading and signing (PKCS#8 and PKCS#1)
//   - REST API connectivity (portfolio balance, account limits)
//   - WebSocket endpoint reachability
//   - SQLite database availability
//
// Each check reports PASS, FAIL, or WARN with a detail message.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type result struct {
	name   string
	status string // PASS, FAIL, WARN
	detail string
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func main() {
	var results []result

	// 1. .env file
	envPath := ".env"
	envBytes, err := os.ReadFile(envPath)
	if err != nil {
		results = append(results, result{".env file", "FAIL", fmt.Sprintf("read: %v", err)})
		printResults(results)
		os.Exit(1)
	}
	results = append(results, result{".env file", "PASS", fmt.Sprintf("%d bytes", len(envBytes))})

	// Parse env vars from .env file
	envVars := parseEnvFile(string(envBytes))

	// 2. Required env vars
	required := []string{"KALSHI_API_KEY_ID", "KALSHI_PRIVATE_KEY_PATH", "KALSHI_ENV"}
	for _, k := range required {
		v, ok := envVars[k]
		if !ok || v == "" {
			results = append(results, result{"env: " + k, "FAIL", "missing or empty"})
		} else {
			results = append(results, result{"env: " + k, "PASS", maskValue(k, v)})
		}
	}

	// 3. KALSHI_ENV value
	env := envVars["KALSHI_ENV"]
	var restBase, wsBase string
	switch env {
	case "demo":
		restBase = "https://external-api.demo.kalshi.co/trade-api/v2"
		wsBase = "wss://external-api-ws.demo.kalshi.co/trade-api/ws/v2"
	case "prod":
		restBase = "https://external-api.kalshi.com/trade-api/v2"
		wsBase = "wss://external-api-ws.kalshi.com/trade-api/ws/v2"
	default:
		results = append(results, result{"env: KALSHI_ENV", "FAIL", fmt.Sprintf("must be demo or prod, got %q", env)})
		printResults(results)
		os.Exit(1)
	}
	results = append(results, result{"env: KALSHI_ENV", "PASS", env})

	// 4. Key ID format
	keyID := envVars["KALSHI_API_KEY_ID"]
	if !uuidRe.MatchString(keyID) {
		results = append(results, result{"key ID format", "FAIL", fmt.Sprintf("not a UUID: %s", keyID)})
	} else {
		results = append(results, result{"key ID format", "PASS", keyID})
	}

	// 5. Private key file
	keyPath := envVars["KALSHI_PRIVATE_KEY_PATH"]
	pemData, err := os.ReadFile(keyPath)
	if err != nil {
		results = append(results, result{"private key file", "FAIL", fmt.Sprintf("read %s: %v", keyPath, err)})
		printResults(results)
		os.Exit(1)
	}
	results = append(results, result{"private key file", "PASS", fmt.Sprintf("%s (%d bytes)", keyPath, len(pemData))})

	// 6. Parse key
	block, _ := pem.Decode(pemData)
	if block == nil {
		results = append(results, result{"private key parse", "FAIL", "no PEM block found"})
		printResults(results)
		os.Exit(1)
	}
	var rsaKey *rsa.PrivateKey
	parsed, err8 := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err8 == nil {
		rk, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			results = append(results, result{"private key parse", "FAIL", fmt.Sprintf("not RSA: %T", parsed)})
			printResults(results)
			os.Exit(1)
		}
		rsaKey = rk
		results = append(results, result{"private key parse", "PASS", fmt.Sprintf("PKCS#8 %s, %d-bit", block.Type, rk.N.BitLen())})
	} else {
		rk, err1 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err1 != nil {
			results = append(results, result{"private key parse", "FAIL", fmt.Sprintf("PKCS8: %v; PKCS1: %v", err8, err1)})
			printResults(results)
			os.Exit(1)
		}
		rsaKey = rk
		results = append(results, result{"private key parse", "PASS", fmt.Sprintf("PKCS#1 %s, %d-bit", block.Type, rk.N.BitLen())})
	}

	// 7. Sign test — can we produce a signature?
	testSig := sign(rsaKey, "test")
	if testSig == "" {
		results = append(results, result{"signature generation", "FAIL", "empty signature"})
	} else {
		results = append(results, result{"signature generation", "PASS", fmt.Sprintf("produced %d-char base64 sig", len(testSig))})
	}

	// 8. REST public endpoint (no auth needed)
	restURL := restBase + "/events?series_ticker=KXATPMATCH&limit=1"
	resp, body := httpGet(restURL, nil, 15*time.Second)
	switch {
	case resp == nil:
		results = append(results, result{"REST public (/events)", "FAIL", fmt.Sprintf("request: %s", string(body))})
	case resp.StatusCode == 200:
		var ev struct {
			Events []struct {
				EventTicker string `json:"event_ticker"`
			} `json:"events"`
		}
		if json.Unmarshal(body, &ev) == nil && len(ev.Events) > 0 {
			results = append(results, result{"REST public (/events)", "PASS", fmt.Sprintf("200 OK, %d event(s)", len(ev.Events))})
		} else {
			results = append(results, result{"REST public (/events)", "PASS", "200 OK"})
		}
	case resp.StatusCode == 429:
		results = append(results, result{"REST public (/events)", "WARN", "429 rate limited (endpoint reachable)"})
	default:
		results = append(results, result{"REST public (/events)", "FAIL", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncBody(body))})
	}

	// 9. REST private endpoint (auth required) — tests key validity
	authHeaders := func(method, path string) map[string]string {
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		return map[string]string{
			"KALSHI-ACCESS-KEY":       keyID,
			"KALSHI-ACCESS-SIGNATURE": sign(rsaKey, ts+method+path),
			"KALSHI-ACCESS-TIMESTAMP": ts,
		}
	}

	balanceURL := restBase + "/portfolio/balance"
	resp, body = httpGet(balanceURL, authHeaders("GET", "/trade-api/v2/portfolio/balance"), 10*time.Second)
	switch {
	case resp == nil:
		results = append(results, result{"REST auth (/portfolio/balance)", "FAIL", fmt.Sprintf("request: %s", string(body))})
	case resp.StatusCode == 200:
		results = append(results, result{"REST auth (/portfolio/balance)", "PASS", "200 OK — key valid"})
	case resp.StatusCode == 401:
		detail := parseAuthError(body)
		if strings.Contains(detail, "NOT_FOUND") {
			results = append(results, result{"REST auth (/portfolio/balance)", "FAIL",
				fmt.Sprintf("401 NOT_FOUND — key ID %s not recognized by %s env", keyID, env)})
		} else if strings.Contains(detail, "signature") {
			results = append(results, result{"REST auth (/portfolio/balance)", "FAIL",
				fmt.Sprintf("401 signature error — key file doesn't match key ID")})
		} else {
			results = append(results, result{"REST auth (/portfolio/balance)", "FAIL",
				fmt.Sprintf("401: %s", detail)})
		}
	case resp.StatusCode == 403:
		results = append(results, result{"REST auth (/portfolio/balance)", "WARN",
			fmt.Sprintf("403 — key valid but lacks permission (read-only key?): %s", truncBody(body))})
	default:
		results = append(results, result{"REST auth (/portfolio/balance)", "FAIL",
			fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncBody(body))})
	}

	// 10. REST events with auth (tests auth on public endpoint too)
	eventsAuthURL := restBase + "/events?series_ticker=KXATPMATCH&limit=1"
	resp, body = httpGet(eventsAuthURL, authHeaders("GET", "/trade-api/v2/events"), 10*time.Second)
	if resp != nil && resp.StatusCode == 200 {
		results = append(results, result{"REST auth (/events)", "PASS", "200 OK with auth headers"})
	} else if resp != nil && resp.StatusCode == 401 {
		results = append(results, result{"REST auth (/events)", "FAIL",
			fmt.Sprintf("401: %s", parseAuthError(body))})
	} else if resp != nil {
		results = append(results, result{"REST auth (/events)", "WARN",
			fmt.Sprintf("HTTP %d", resp.StatusCode)})
	} else {
		results = append(results, result{"REST auth (/events)", "FAIL", "request failed"})
	}

	// 11. WS endpoint reachability (HTTP upgrade attempt)
	wsHTTPURL := strings.Replace(wsBase, "wss://", "https://", 1)
	wsHeaders := authHeaders("GET", "/trade-api/ws/v2")
	wsHeaders["Connection"] = "Upgrade"
	wsHeaders["Upgrade"] = "websocket"
	wsHeaders["Sec-WebSocket-Version"] = "13"
	wsHeaders["Sec-WebSocket-Key"] = "dGhlIHNhbXBsZSBub25jZQ=="
	resp, body = httpGet(wsHTTPURL, wsHeaders, 10*time.Second)
	switch {
	case resp == nil:
		results = append(results, result{"WS handshake", "FAIL", fmt.Sprintf("request: %s", string(body))})
	case resp.StatusCode == 101:
		results = append(results, result{"WS handshake", "PASS", "101 Switching Protocols"})
	case resp.StatusCode == 401:
		detail := parseAuthError(body)
		if strings.Contains(detail, "NOT_FOUND") {
			results = append(results, result{"WS handshake", "FAIL",
				fmt.Sprintf("401 NOT_FOUND — key ID not recognized")})
		} else {
			results = append(results, result{"WS handshake", "FAIL",
				fmt.Sprintf("401: %s", detail)})
		}
	case resp.StatusCode == 403:
		results = append(results, result{"WS handshake", "WARN",
			fmt.Sprintf("403 — key valid but lacks WS permission: %s", truncBody(body))})
	default:
		results = append(results, result{"WS handshake", "FAIL",
			fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncBody(body))})
	}

	// 11b. Cross-env check — try key against PROD to detect env mismatch
	if env == "demo" {
		prodBalanceURL := "https://external-api.kalshi.com/trade-api/v2/portfolio/balance"
		resp, body = httpGet(prodBalanceURL, authHeaders("GET", "/trade-api/v2/portfolio/balance"), 10*time.Second)
		switch {
		case resp == nil:
			results = append(results, result{"cross-env (prod /balance)", "WARN", "request failed"})
		case resp.StatusCode == 200:
			results = append(results, result{"cross-env (prod /balance)", "FAIL",
				"key works on PROD but .env says demo — env mismatch! Set KALSHI_ENV=prod"})
		case resp.StatusCode == 401:
			detail := parseAuthError(body)
			if strings.Contains(detail, "NOT_FOUND") {
				results = append(results, result{"cross-env (prod /balance)", "PASS",
					"key not found on prod either — not an env mismatch"})
			} else {
				results = append(results, result{"cross-env (prod /balance)", "WARN",
					fmt.Sprintf("401: %s", detail)})
			}
		default:
			results = append(results, result{"cross-env (prod /balance)", "WARN",
				fmt.Sprintf("HTTP %d", resp.StatusCode)})
		}
	}

	// 12. SQLite
	dbPath := filepath.Join(os.TempDir(), "kalshi_validate_test.db")
	defer os.Remove(dbPath)
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)", dbPath)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		results = append(results, result{"SQLite", "FAIL", fmt.Sprintf("open: %v", err)})
	} else {
		sqlDB, _ := db.DB()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			results = append(results, result{"SQLite", "FAIL", fmt.Sprintf("ping: %v", err)})
		} else {
			results = append(results, result{"SQLite", "PASS", "open + ping OK"})
		}
		sqlDB.Close()
	}

	// 13. Optional config vars
	optionalInts := map[string]int{
		"SCAN_INTERVAL_HOURS": 24,
		"TRACK_LEAD_MINUTES":  5,
		"WS_MIN_BACKOFF_SECS": 1,
		"WS_MAX_BACKOFF_SECS": 30,
		"BATCH_SIZE":          500,
		"FLUSH_TIMEOUT_MS":    250,
		"HTTP_TIMEOUT_SECS":   30,
		"SCHEDULER_POLL_SECS": 30,
	}
	for k, def := range optionalInts {
		v, ok := envVars[k]
		if !ok || v == "" {
			results = append(results, result{"env: " + k, "WARN", fmt.Sprintf("not set, default %d", def)})
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			results = append(results, result{"env: " + k, "FAIL", fmt.Sprintf("not integer: %q", v)})
		} else if n <= 0 {
			results = append(results, result{"env: " + k, "FAIL", fmt.Sprintf("must be > 0, got %d", n)})
		} else {
			results = append(results, result{"env: " + k, "PASS", fmt.Sprintf("%d", n)})
		}
	}

	// 14. SERIES_TICKERS
	series := envVars["SERIES_TICKERS"]
	if series == "" {
		results = append(results, result{"env: SERIES_TICKERS", "WARN", "not set, will use defaults"})
	} else {
		tickers := strings.Split(series, ",")
		results = append(results, result{"env: SERIES_TICKERS", "PASS", fmt.Sprintf("%d series: %s", len(tickers), series)})
	}

	printResults(results)

	// Exit code
	failed := 0
	for _, r := range results {
		if r.status == "FAIL" {
			failed++
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func sign(key *rsa.PrivateKey, msg string) string {
	hash := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPSS(rand.Reader, key, 0x05, hash[:], nil)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func httpGet(u string, headers map[string]string, timeout time.Duration) (*http.Response, []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, []byte(fmt.Sprintf("new request: %v", err))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, []byte(err.Error())
	}
	// 101 Switching Protocols — body is now a WebSocket stream.
	// Don't read it; close immediately. Reading blocks forever.
	if resp.StatusCode == 101 {
		resp.Body.Close()
		return resp, []byte("101 Switching Protocols")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func parseEnvFile(content string) map[string]string {
	vars := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		// Strip quotes
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		vars[k] = v
	}
	return vars
}

func parseAuthError(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		if details, ok := m["details"]; ok {
			return fmt.Sprintf("%v", details)
		}
		if errMsg, ok := m["error"]; ok {
			if em, ok2 := errMsg.(map[string]any); ok2 {
				if d, ok3 := em["details"]; ok3 {
					return fmt.Sprintf("%v", d)
				}
			}
		}
	}
	return truncBody(body)
}

func truncBody(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func maskValue(k, v string) string {
	switch k {
	case "KALSHI_API_KEY_ID":
		return v // UUID, not sensitive
	case "KALSHI_PRIVATE_KEY_PATH":
		return v
	default:
		if len(v) > 4 {
			return v[:2] + "..." + v[len(v)-2:]
		}
		return "***"
	}
}

func printResults(results []result) {
	fmt.Println()
	fmt.Println("  VALIDATION REPORT")
	fmt.Println("  =================")
	fmt.Println()
	pass, fail, warn := 0, 0, 0
	for _, r := range results {
		var icon string
		switch r.status {
		case "PASS":
			icon = "[PASS]"
			pass++
		case "FAIL":
			icon = "[FAIL]"
			fail++
		case "WARN":
			icon = "[WARN]"
			warn++
		}
		fmt.Printf("  %-8s %-35s %s\n", icon, r.name, r.detail)
	}
	fmt.Println()
	fmt.Printf("  Summary: %d pass, %d fail, %d warn\n", pass, fail, warn)
	fmt.Println()
}
