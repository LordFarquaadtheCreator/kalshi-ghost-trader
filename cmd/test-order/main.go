// Command test-order sends a single IOC bid order to Kalshi.
// Uses the same kalshiclient.Client + signing as the real emitter.
//
// Usage:
//
//	go run ./cmd/test-order -ticker KXITFMATCH-26JUL17OCODEL-DEL -price 0.86 -count 1
//
// Flags:
//
//	-ticker   Market ticker (required)
//	-price    Yes price 0.01-0.99 (required)
//	-count    Contract count, min 1 (default 1)
//	-key-id   Kalshi API key ID (env: KALSHI_API_KEY_ID)
//	-key-path Path to RSA private key (env: KALSHI_PRIVATE_KEY_PATH)
//	-env      "demo" or "prod" (env: KALSHI_ENV, default prod)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiauth"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiclient"
)

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func main() {
	ticker := flag.String("ticker", "", "market ticker (required)")
	price := flag.Float64("price", 0, "yes price 0.01-0.99 (required)")
	count := flag.Float64("count", 1, "contract count, min 1")
	keyID := flag.String("key-id", os.Getenv("KALSHI_API_KEY_ID"), "Kalshi API key ID")
	keyPath := flag.String("key-path", os.Getenv("KALSHI_PRIVATE_KEY_PATH"), "path to RSA private key")
	env := flag.String("env", envOr("KALSHI_ENV", "prod"), "demo or prod")
	flag.Parse()

	if *ticker == "" || *price <= 0 {
		fmt.Fprintln(os.Stderr, "ticker and price are required")
		os.Exit(1)
	}
	if *keyID == "" || *keyPath == "" {
		fmt.Fprintln(os.Stderr, "key-id and key-path required (flag or env)")
		os.Exit(1)
	}
	if *count < 1 {
		*count = 1
	}

	var baseURL string
	switch *env {
	case "demo":
		baseURL = "https://demo-api.kalshi.com/trade-api/v2"
	default:
		baseURL = "https://external-api.kalshi.com/trade-api/v2"
	}

	signer, err := kalshiauth.NewSignerFromFile(*keyID, *keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load signer: %v\n", err)
		os.Exit(1)
	}

	client := kalshiclient.NewClient(baseURL, signer, 30*time.Second, 15, slog.Default())

	type req struct {
		Ticker                  string `json:"ticker"`
		Side                    string `json:"side"`
		Count                   string `json:"count"`
		Price                   string `json:"price"`
		TimeInForce             string `json:"time_in_force"`
		SelfTradePreventionType string `json:"self_trade_prevention_type"`
		PostOnly                bool   `json:"post_only"`
		ReduceOnly              bool   `json:"reduce_only"`
	}

	type resp struct {
		OrderID        string `json:"order_id"`
		FillCount      string `json:"fill_count"`
		RemainingCount string `json:"remaining_count"`
	}

	body := req{
		Ticker:                  *ticker,
		Side:                    "bid",
		Count:                   fmt.Sprintf("%.2f", *count),
		Price:                   fmt.Sprintf("%.4f", *price),
		TimeInForce:             "immediate_or_cancel",
		SelfTradePreventionType: "taker_at_cross",
	}

	bodyJSON, _ := json.MarshalIndent(body, "", "  ")
	fmt.Printf("Sending order:\n%s\n\n", bodyJSON)

	var r resp
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err = client.Post(ctx, "/portfolio/events/orders", body, &r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED: %v\n", err)
		os.Exit(1)
	}

	respJSON, _ := json.MarshalIndent(r, "", "  ")
	fmt.Printf("Response:\n%s\n", respJSON)
}
