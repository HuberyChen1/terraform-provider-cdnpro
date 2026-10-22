package client

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // The CDN Pro API mandates HMAC-SHA1 for request signing.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

// HTTPDate formats t as the RFC 1123 value the API expects in the Date header,
// for example "Tue, 21 Oct 2026 08:15:30 GMT". The server parses it with the
// pattern "EEE, dd MMM yyyy HH:mm:ss zzz" and rejects requests whose Date is more
// than a few minutes away from its own clock, so callers must generate the value
// immediately before sending.
func HTTPDate(t time.Time) string {
	return t.UTC().Format(http.TimeFormat)
}

// SignPassword returns the HTTP Basic password for one request:
// base64(HMAC-SHA1(key = apiKey, data = date)).
//
// The server recomputes the same value from the Date header and the account's
// API key and compares it with a constant-time check. Mirrors
// DigestUtil.signHmacSHA1 in platform-sdk.
func SignPassword(apiKey, date string) string {
	mac := hmac.New(sha1.New, []byte(apiKey))
	mac.Write([]byte(date))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// BasicAuthorization returns the full Authorization header value for one request:
// "Basic " + base64(apiUser + ":" + SignPassword(apiKey, date)).
func BasicAuthorization(apiUser, apiKey, date string) string {
	credentials := apiUser + ":" + SignPassword(apiKey, date)
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
}

// Literal private_key values the API accepts instead of PEM material when it is
// asked to generate a self-signed certificate. They are sent as-is, never encrypted.
var privateKeyKeywords = map[string]bool{
	"RSA2048": true,
	"ECC256":  true,
}

// IsPrivateKeyKeyword reports whether value is one of the reserved literals
// (RSA2048, ECC256) that request a generated key instead of uploading one.
func IsPrivateKeyKeyword(value string) bool {
	return privateKeyKeywords[value]
}

var whitespace = regexp.MustCompile(`\s`)

// DeriveKeyMaterial returns the AES-128-CBC IV and key the API uses for private
// key upload. Both come from one HMAC-SHA256(key = apiKey, data = date) digest
// rendered as 64 lowercase hex characters: the first 32 are the IV, the last 32
// are the key. Mirrors CertificateEncryptionHelper in NGCdnApiService.
func DeriveKeyMaterial(apiKey, date string) (iv, key []byte) {
	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte(date))
	digest := mac.Sum(nil) // 32 bytes -> 64 hex chars
	hexDigest := hex.EncodeToString(digest)
	iv, _ = hex.DecodeString(hexDigest[:32])
	key, _ = hex.DecodeString(hexDigest[32:])
	return iv, key
}

// EncryptPrivateKey encrypts a PEM private key for upload with the scheme the API
// requires: AES-128-CBC with PKCS#7 padding, IV and key from DeriveKeyMaterial,
// output base64 encoded. It is equivalent to
//
//	printf '%s' "$PEM" | openssl enc -aes-128-cbc -base64 -A -nosalt -K "$key" -iv "$iv"
//
// The same date string must be sent as the request's Date header, because the
// server derives the decryption material from that header. Reserved keywords
// (RSA2048, ECC256) are returned unchanged.
func EncryptPrivateKey(privateKeyPEM, apiKey, date string) (string, error) {
	if IsPrivateKeyKeyword(privateKeyPEM) {
		return privateKeyPEM, nil
	}
	if privateKeyPEM == "" {
		return "", errors.New("private key is empty")
	}
	iv, key := DeriveKeyMaterial(apiKey, date)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("init AES cipher: %w", err)
	}
	plaintext := pkcs7Pad([]byte(privateKeyPEM), block.BlockSize())
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintext)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptPrivateKey reverses EncryptPrivateKey. The server strips all whitespace
// from the ciphertext before decoding, so this does the same. It exists for tests
// and tooling; the provider itself never needs to decrypt.
func DecryptPrivateKey(ciphertextB64, apiKey, date string) (string, error) {
	if IsPrivateKeyKeyword(ciphertextB64) {
		return ciphertextB64, nil
	}
	raw, err := base64.StdEncoding.DecodeString(whitespace.ReplaceAllString(ciphertextB64, ""))
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	iv, key := DeriveKeyMaterial(apiKey, date)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("init AES cipher: %w", err)
	}
	if len(raw) == 0 || len(raw)%block.BlockSize() != 0 {
		return "", errors.New("ciphertext length is not a multiple of the AES block size")
	}
	plaintext := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, raw)
	unpadded, err := pkcs7Unpad(plaintext, block.BlockSize())
	if err != nil {
		return "", err
	}
	return string(unpadded), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty plaintext")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, errors.New("invalid PKCS#7 padding")
		}
	}
	return data[:len(data)-padding], nil
}
