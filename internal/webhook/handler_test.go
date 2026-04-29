package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	secret := []byte("s3cret")
	body := []byte(`{"hello":"world"}`)

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	cases := []struct {
		name   string
		secret []byte
		header string
		body   []byte
		want   bool
	}{
		{"valid", secret, good, body, true},
		{"missing header", secret, "", body, false},
		{"missing prefix", secret, hex.EncodeToString(mac.Sum(nil)), body, false},
		{"wrong digest", secret, "sha256=deadbeef", body, false},
		{"non-hex", secret, "sha256=zzzz", body, false},
		{"wrong secret", []byte("nope"), good, body, false},
		{"tampered body", secret, good, append([]byte{}, append(body, '!')...), false},
		{"empty secret", []byte{}, good, body, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verifySignature(tc.secret, tc.header, tc.body)
			if got != tc.want {
				t.Errorf("verifySignature() = %v, want %v", got, tc.want)
			}
		})
	}
}
