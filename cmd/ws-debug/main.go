// Command ws-debug is a diagnostic tool for testing Kalshi API REST and WebSocket
// endpoints with RSA-PSS-SHA256 signing.
//
// It performs signed GET requests to portfolio balance and account limits
// endpoints, then attempts a WebSocket handshake — printing results for each
// step. Useful for verifying credentials and connectivity before running the
// full ghost-trader service.
//
// Environment variables:
//   - KALSHI_API_KEY_ID — Kalshi API key ID
//   - KALSHI_PRIVATE_KEY_PATH — path to RSA PEM private key
//   - KALSHI_ENV — "demo" or "prod" (determines API URLs)
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"crypto/x509"
)

func sign(rsakey *rsa.PrivateKey, msg string) string {
	hash := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPSS(rand.Reader, rsakey, 0x05, hash[:], nil)
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func main() {
	keyID := os.Getenv("KALSHI_API_KEY_ID")
	keyPath := os.Getenv("KALSHI_PRIVATE_KEY_PATH")

	pemData, err := os.ReadFile(keyPath)
	if err != nil {
		fmt.Println("read key:", err)
		os.Exit(1)
	}
	block, _ := pem.Decode(pemData)
	if block == nil {
		fmt.Println("no PEM block")
		os.Exit(1)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		pkcs1, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err2 != nil {
			fmt.Println("parse key:", err, err2)
			os.Exit(1)
		}
		parsed = pkcs1
	}
	rsaKey := parsed.(*rsa.PrivateKey)

	// Test 1: REST private endpoint (portfolio/balance)
	restURL := "https://external-api.kalshi.com/trade-api/v2/portfolio/balance"
	tsMs := strconv.FormatInt(time.Now().UnixMilli(), 10)
	restSig := sign(rsaKey, tsMs+"GET/trade-api/v2/portfolio/balance")

	req, _ := http.NewRequest("GET", restURL, nil)
	req.Header.Set("KALSHI-ACCESS-KEY", keyID)
	req.Header.Set("KALSHI-ACCESS-SIGNATURE", restSig)
	req.Header.Set("KALSHI-ACCESS-TIMESTAMP", tsMs)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("REST req:", err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Println("REST /portfolio/balance:")
		fmt.Println("  Status:", resp.StatusCode)
		fmt.Println("  Body:", string(body))
		fmt.Println()
	}

	// Test 1b: Account limits (tier)
	limitsURL := "https://external-api.kalshi.com/trade-api/v2/account/limits"
	tsMs = strconv.FormatInt(time.Now().UnixMilli(), 10)
	limitsSig := sign(rsaKey, tsMs+"GET/trade-api/v2/account/limits")
	req3, _ := http.NewRequest("GET", limitsURL, nil)
	req3.Header.Set("KALSHI-ACCESS-KEY", keyID)
	req3.Header.Set("KALSHI-ACCESS-SIGNATURE", limitsSig)
	req3.Header.Set("KALSHI-ACCESS-TIMESTAMP", tsMs)
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		fmt.Println("REST limits:", err)
	} else {
		body3, _ := io.ReadAll(resp3.Body)
		resp3.Body.Close()
		fmt.Println("REST /account/limits:")
		fmt.Println("  Status:", resp3.StatusCode)
		fmt.Println("  Body:", string(body3))
		fmt.Println()
	}

	// Test 2: WS handshake
	wsURL := "https://external-api-ws.kalshi.com/trade-api/ws/v2"
	tsMs = strconv.FormatInt(time.Now().UnixMilli(), 10)
	wsSig := sign(rsaKey, tsMs+"GET/trade-api/ws/v2")

	req2, _ := http.NewRequest("GET", wsURL, nil)
	req2.Header.Set("Connection", "Upgrade")
	req2.Header.Set("Upgrade", "websocket")
	req2.Header.Set("Sec-WebSocket-Version", "13")
	req2.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req2.Header.Set("KALSHI-ACCESS-KEY", keyID)
	req2.Header.Set("KALSHI-ACCESS-SIGNATURE", wsSig)
	req2.Header.Set("KALSHI-ACCESS-TIMESTAMP", tsMs)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		fmt.Println("WS req:", err)
	} else {
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		fmt.Println("WS handshake:")
		fmt.Println("  Status:", resp2.StatusCode)
		fmt.Println("  Body:", string(body2))
	}
}
