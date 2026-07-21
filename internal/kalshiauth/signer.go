// Package kalshiAuth implements RSA-PSS-SHA256 request signing for the Kalshi API.
//
// Kalshi requires three headers on every authenticated request:
//   - KALSHI-ACCESS-KEY: the key ID from the Kalshi dashboard
//   - KALSHI-ACCESS-SIGNATURE: base64-encoded RSA-PSS signature
//   - KALSHI-ACCESS-TIMESTAMP: current time in milliseconds
//
// The signed message is: timestamp_ms + HTTP_method + request_path.
// Query parameters are stripped before signing — only the path is signed.
//
// For WebSocket connections, the handshake always signs "GET" + "/trade-api/ws/v2".
//
// Private keys in both PKCS#8 ("PRIVATE KEY") and PKCS#1 ("RSA PRIVATE KEY")
// PEM formats are supported. Kalshi dashboard exports PKCS#8 by default.
package kalshiAuth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Signer holds Kalshi API credentials and signs requests with RSA-PSS-SHA256.
type Signer struct {
	KeyID      string
	PrivateKey *rsa.PrivateKey
}

// wsHandshakePath is the path signed for WebSocket handshake auth.
const wsHandshakePath = "/trade-api/ws/v2"

// NewSignerFromFile loads an RSA private key from a PEM file on disk.
// Supports both PKCS#8 ("PRIVATE KEY") and PKCS#1 ("RSA PRIVATE KEY").
func NewSignerFromFile(keyID, privateKeyPath string) (*Signer, error) {
	pemData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key file %s: %w", privateKeyPath, err)
	}
	return NewSignerFromPEM(keyID, pemData)
}

// NewSignerFromPEM parses an RSA private key from PEM-encoded bytes.
func NewSignerFromPEM(keyID string, pemBytes []byte) (*Signer, error) {
	key, err := ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, err
	}
	return &Signer{KeyID: keyID, PrivateKey: key}, nil
}

// ParsePrivateKey handles both PKCS#8 and PKCS#1 PEM formats.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private key")
	}
	// Try PKCS#8 first (Kalshi default), fall back to PKCS#1
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		pkcs1Key, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if pkcs1Err != nil {
			return nil, fmt.Errorf("parse private key: PKCS8: %v; PKCS1: %v", err, pkcs1Err)
		}
		return pkcs1Key, nil
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA (got %T)", key)
	}
	return rsaKey, nil
}

// signPSS signs text with RSA-PSS-SHA256, base64-encodes the result.
// Salt length = hash length (32 bytes for SHA-256), matching Python's PSS.DIGEST_LENGTH.
// MGF1 hash defaults to SHA-256 when PSSOptions.Hash is 0.
func signPSS(privateKey *rsa.PrivateKey, text string) (string, error) {
	hash := sha256.Sum256([]byte(text))
	sig, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hash[:],
		&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	if err != nil {
		return "", fmt.Errorf("RSA-PSS sign: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// AuthHeaders returns the 3 Kalshi auth headers for a REST request.
// method: "GET", "POST", etc. path: full path WITHOUT query string.
func (s *Signer) AuthHeaders(method, path string) (map[string]string, error) {
	return s.buildHeaders(method, stripQuery(path))
}

// WSHeaders returns auth headers for a WebSocket handshake.
// Always signs: timestamp + "GET" + wsHandshakePath
func (s *Signer) WSHeaders() (map[string]string, error) {
	return s.buildHeaders("GET", wsHandshakePath)
}

func (s *Signer) buildHeaders(method, path string) (map[string]string, error) {
	tsMs := strconv.FormatInt(time.Now().UnixMilli(), 10)
	msg := tsMs + method + path
	sig, err := signPSS(s.PrivateKey, msg)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"KALSHI-ACCESS-KEY":       s.KeyID,
		"KALSHI-ACCESS-SIGNATURE": sig,
		"KALSHI-ACCESS-TIMESTAMP": tsMs,
	}, nil
}

func stripQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}
