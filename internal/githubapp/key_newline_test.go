package githubapp

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

// Vault drops a value's final newline, so a stored PEM key has none.
func TestKeyWithoutFinalNewline(t *testing.T) {
	k := testKey(t)
	p := strings.TrimRight(string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})), "\n")
	if got, err := ParseKey([]byte(p)); err != nil || !got.Equal(k) {
		t.Fatal(err)
	}
}
