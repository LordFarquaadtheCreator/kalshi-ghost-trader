package kalshiAuth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func genRSAKey(t *testing.T, pkcs8 bool) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var der []byte
	if pkcs8 {
		der, err = x509.MarshalPKCS8PrivateKey(key)
	} else {
		der = x509.MarshalPKCS1PrivateKey(key)
	}
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  pemBlockType(pkcs8),
		Bytes: der,
	})
}

func pemBlockType(pkcs8 bool) string {
	if pkcs8 {
		return "PRIVATE KEY"
	}
	return "RSA PRIVATE KEY"
}

func TestParsePrivateKey_PKCS8(t *testing.T) {
	pemBytes := genRSAKey(t, true)
	key, err := ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("PKCS8 parse: %v", err)
	}
	if key == nil {
		t.Fatal("nil key")
	}
}

func TestParsePrivateKey_PKCS1(t *testing.T) {
	pemBytes := genRSAKey(t, false)
	key, err := ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("PKCS1 parse: %v", err)
	}
	if key == nil {
		t.Fatal("nil key")
	}
}

func TestParsePrivateKey_Invalid(t *testing.T) {
	_, err := ParsePrivateKey([]byte("not a pem"))
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestParsePrivateKey_NotRSA(t *testing.T) {
	// PKCS8 EC key — should fail RSA type assertion
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	ecPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	_, err = ParsePrivateKey(ecPEM)
	if err == nil {
		t.Fatal("expected error for non-RSA key")
	}
	if !strings.Contains(err.Error(), "not RSA") {
		t.Fatalf("expected 'not RSA' error, got: %v", err)
	}
}

func TestNewSignerFromPEM(t *testing.T) {
	pemBytes := genRSAKey(t, true)
	s, err := NewSignerFromPEM("test-key-id", pemBytes)
	if err != nil {
		t.Fatalf("NewSignerFromPEM: %v", err)
	}
	if s.KeyID != "test-key-id" {
		t.Fatalf("KeyID = %q, want %q", s.KeyID, "test-key-id")
	}
	if s.PrivateKey == nil {
		t.Fatal("nil PrivateKey")
	}
}

func TestAuthHeaders(t *testing.T) {
	pemBytes := genRSAKey(t, true)
	s, err := NewSignerFromPEM("test-key-id", pemBytes)
	if err != nil {
		t.Fatalf("NewSignerFromPEM: %v", err)
	}

	headers, err := s.AuthHeaders("GET", "/trade-api/v2/events")
	if err != nil {
		t.Fatalf("AuthHeaders: %v", err)
	}

	required := []string{"KALSHI-ACCESS-KEY", "KALSHI-ACCESS-SIGNATURE", "KALSHI-ACCESS-TIMESTAMP"}
	for _, h := range required {
		if headers[h] == "" {
			t.Fatalf("header %s empty", h)
		}
	}
	if headers["KALSHI-ACCESS-KEY"] != "test-key-id" {
		t.Fatalf("KALSHI-ACCESS-KEY = %q, want %q", headers["KALSHI-ACCESS-KEY"], "test-key-id")
	}
}

func TestAuthHeaders_DifferentPathsProduceDifferentSignatures(t *testing.T) {
	pemBytes := genRSAKey(t, true)
	s, _ := NewSignerFromPEM("k", pemBytes)

	h1, _ := s.AuthHeaders("GET", "/trade-api/v2/events")
	h2, _ := s.AuthHeaders("GET", "/trade-api/v2/markets")
	if h1["KALSHI-ACCESS-SIGNATURE"] == h2["KALSHI-ACCESS-SIGNATURE"] {
		t.Fatal("different paths should produce different signatures")
	}
}

func TestWSHeaders(t *testing.T) {
	pemBytes := genRSAKey(t, true)
	s, _ := NewSignerFromPEM("k", pemBytes)

	headers, err := s.WSHeaders()
	if err != nil {
		t.Fatalf("WSHeaders: %v", err)
	}
	if headers["KALSHI-ACCESS-KEY"] != "k" {
		t.Fatalf("key = %q", headers["KALSHI-ACCESS-KEY"])
	}
}

func TestStripQuery(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/events?limit=200&cursor=abc", "/events"},
		{"/markets/KXATPMATCH-123", "/markets/KXATPMATCH-123"},
		{"/events", "/events"},
		{"", ""},
	}
	for _, c := range cases {
		got := stripQuery(c.in)
		if got != c.want {
			t.Fatalf("stripQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
