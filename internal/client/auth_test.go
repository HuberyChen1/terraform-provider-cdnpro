package client

import (
	"testing"
	"time"
)

// Vectors generated independently with OpenSSL 3.5.5:
//
//	KEY='my-api-key-0123456789'; DATE='Tue, 21 Oct 2026 08:15:30 GMT'
//	printf '%s' "$DATE" | openssl dgst -sha1 -hmac "$KEY" -binary | base64
//	printf '%s' "$DATE" | openssl dgst -sha256 -hmac "$KEY"
//	printf '%s' "$PEM"  | openssl enc -aes-128-cbc -base64 -A -nosalt -K "$key" -iv "$iv"
const (
	testAPIKey = "my-api-key-0123456789"
	testDate   = "Tue, 21 Oct 2026 08:15:30 GMT"
	testPEM    = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7\n-----END PRIVATE KEY-----"

	wantPassword  = "7dQsdlhA3tYrGtxg9+Szwg6OQ1U="
	wantAuthValue = "Basic YWxpY2U6N2RRc2RsaEEzdFlyR3R4ZzkrU3p3ZzZPUTFVPQ=="
	wantIVHex     = "1374e5263c6e4ede18810511ea342716"
	wantKeyHex    = "9e1c4a1d304539e6b5a09445ad208a66"
	wantCipher    = "yyPl3z1IzyfufbCkhP+pGdb6O5AyoDJ0xLOe77aCtIpK9uVp1nDij+tDVI1njg18diviUXIZpnyxeUP4nHFgxPhfniViTg8Jjqqen8Rqd7OAQ3pxYbkAYhxwuW5+5KyhHOUxlrugO7fQM+AskT6xvg=="
)

func TestHTTPDate(t *testing.T) {
	// 21 Oct 2026 is a Wednesday; testDate's "Tue" is only a signing input.
	in := time.Date(2026, time.October, 21, 16, 15, 30, 0, time.FixedZone("CST", 8*3600))
	if got, want := HTTPDate(in), "Wed, 21 Oct 2026 08:15:30 GMT"; got != want {
		t.Fatalf("HTTPDate = %q, want %q", got, want)
	}
}

func TestSignPassword(t *testing.T) {
	if got := SignPassword(testAPIKey, testDate); got != wantPassword {
		t.Fatalf("SignPassword = %q, want %q", got, wantPassword)
	}
}

func TestBasicAuthorization(t *testing.T) {
	if got := BasicAuthorization("alice", testAPIKey, testDate); got != wantAuthValue {
		t.Fatalf("BasicAuthorization = %q, want %q", got, wantAuthValue)
	}
}

func TestDeriveKeyMaterial(t *testing.T) {
	iv, key := DeriveKeyMaterial(testAPIKey, testDate)
	if got := hexString(iv); got != wantIVHex {
		t.Errorf("iv = %s, want %s", got, wantIVHex)
	}
	if got := hexString(key); got != wantKeyHex {
		t.Errorf("key = %s, want %s", got, wantKeyHex)
	}
}

func TestEncryptPrivateKey(t *testing.T) {
	got, err := EncryptPrivateKey(testPEM, testAPIKey, testDate)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantCipher {
		t.Fatalf("EncryptPrivateKey =\n%s\nwant\n%s", got, wantCipher)
	}
}

func TestEncryptPrivateKeyRoundTrip(t *testing.T) {
	for _, pem := range []string{"a", "0123456789abcdef", testPEM, string(make([]byte, 1000))} {
		enc, err := EncryptPrivateKey(pem, testAPIKey, testDate)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := DecryptPrivateKey(enc, testAPIKey, testDate)
		if err != nil {
			t.Fatal(err)
		}
		if dec != pem {
			t.Fatalf("round trip mismatch for %q", pem)
		}
	}
}

func TestDecryptPrivateKeyIgnoresWhitespace(t *testing.T) {
	wrapped := wantCipher[:40] + "\r\n" + wantCipher[40:80] + " \n" + wantCipher[80:]
	got, err := DecryptPrivateKey(wrapped, testAPIKey, testDate)
	if err != nil {
		t.Fatal(err)
	}
	if got != testPEM {
		t.Fatalf("decrypted %q, want %q", got, testPEM)
	}
}

func TestEncryptPrivateKeyKeywords(t *testing.T) {
	for _, kw := range []string{"RSA2048", "ECC256"} {
		got, err := EncryptPrivateKey(kw, testAPIKey, testDate)
		if err != nil {
			t.Fatal(err)
		}
		if got != kw {
			t.Fatalf("keyword %s was encrypted to %q", kw, got)
		}
	}
	if _, err := EncryptPrivateKey("", testAPIKey, testDate); err == nil {
		t.Fatal("expected error for empty private key")
	}
}

func TestDecryptPrivateKeyRejectsGarbage(t *testing.T) {
	if _, err := DecryptPrivateKey("not base64!", testAPIKey, testDate); err == nil {
		t.Fatal("expected base64 error")
	}
	if _, err := DecryptPrivateKey("AAAA", testAPIKey, testDate); err == nil {
		t.Fatal("expected block size error")
	}
}

func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 2*len(b))
	for i, v := range b {
		out[2*i] = digits[v>>4]
		out[2*i+1] = digits[v&0x0f]
	}
	return string(out)
}
