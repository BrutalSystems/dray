package githubapp

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifySignature(t *testing.T) {
	secret, body := []byte("s3cret"), []byte(`{"action":"completed"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifySignature(secret, body, good) {
		t.Error("a good signature was refused")
	}
	for _, bad := range []string{"", "sha1=" + good[7:], "sha256=zz", good[:len(good)-2] + "00"} {
		if VerifySignature(secret, body, bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
	if VerifySignature([]byte("other"), body, good) {
		t.Error("the wrong secret was accepted")
	}
}

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseKey(t *testing.T) {
	k := testKey(t)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	for _, p := range [][]byte{pkcs1, pkcs8} {
		if got, err := ParseKey(p); err != nil || !got.Equal(k) {
			t.Errorf("%v", err)
		}
	}
	if _, err := ParseKey([]byte("nope")); err == nil {
		t.Error("garbage accepted")
	}
}

// fakeGitHub checks the App JWT, mints tokens, and serves the few calls Dray makes.
func fakeGitHub(t *testing.T, key *rsa.PublicKey, minted *int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/installations/7/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 {
			http.Error(w, `{"message":"bad jwt"}`, http.StatusUnauthorized)
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
			http.Error(w, `{"message":"bad signature"}`, http.StatusUnauthorized)
			return
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c map[string]any
		_ = json.Unmarshal(claims, &c)
		if c["iss"] != "42" {
			http.Error(w, `{"message":"wrong app"}`, http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(minted, 1)
		_, _ = w.Write([]byte(`{"token":"ghs_inst","expires_at":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `"}`))
	})
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "token ghs_inst" {
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"name":"ci","head_sha":"` + r.URL.Query().Get("head_sha") + `","status":"completed","conclusion":"success"}]}`)) //nolint:gosec // test server
		}
	})
	mux.HandleFunc("POST /repos/o/r/check-runs", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["head_sha"] != "abc" || b["name"] != "dray / prod" || b["status"] != "in_progress" {
			http.Error(w, `{"message":"unexpected body"}`, http.StatusUnprocessableEntity)
			return
		}
		_, _ = w.Write([]byte(`{"id":99}`))
	})
	mux.HandleFunc("PATCH /repos/o/r/check-runs/99", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	})
	mux.HandleFunc("GET /repos/o/r/tarball/abc", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			http.Redirect(w, r, "/download/abc.tgz", http.StatusFound)
		}
	})
	mux.HandleFunc("GET /download/abc.tgz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("TARBALL"))
	})
	return httptest.NewServer(mux)
}

func TestInstallationCalls(t *testing.T) {
	key := testKey(t)
	var minted int32
	srv := fakeGitHub(t, &key.PublicKey, &minted)
	defer srv.Close()
	app := &App{ID: 42, Key: key, BaseURL: srv.URL}
	c := app.Installation(7)
	ctx := context.Background()

	runs, err := c.RunsForCommit(ctx, "o/r", "abc")
	if err != nil || len(runs) != 1 || runs[0].Conclusion != "success" {
		t.Fatalf("%+v %v", runs, err)
	}
	id, err := c.CreateCheck(ctx, "o/r", "abc", Check{Name: "dray / prod", Status: "in_progress", Title: "Deploying"})
	if err != nil || id != 99 {
		t.Fatalf("%d %v", id, err)
	}
	if err := c.UpdateCheck(ctx, "o/r", id, Check{Status: "completed", Conclusion: "success", Title: "Deployed", Summary: "ok", Text: "log"}); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := c.Tarball(ctx, "o/r", "abc", &b); err != nil || b.String() != "TARBALL" {
		t.Fatalf("%q %v", b.String(), err)
	}
	if minted != 1 {
		t.Errorf("minted %d tokens, want 1 reused", minted)
	}
}

func TestErrorsNeverCarryTheToken(t *testing.T) {
	key := testKey(t)
	var minted int32
	srv := fakeGitHub(t, &key.PublicKey, &minted)
	defer srv.Close()
	app := &App{ID: 1, Key: key, BaseURL: srv.URL}
	_, err := app.Installation(7).RunsForCommit(context.Background(), "o/r", "abc")
	if err == nil || !strings.Contains(err.Error(), "wrong app") || strings.Contains(err.Error(), "eyJ") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckTextKeepsTheEnd(t *testing.T) {
	long := strings.Repeat("x", 70000) + "THE ERROR"
	b := Check{Status: "completed", Conclusion: "failure", Title: "t", Text: long}.body()
	text := b["output"].(map[string]any)["text"].(string)
	if len(text) > 60010 || !strings.HasSuffix(text, "THE ERROR") {
		t.Errorf("len %d", len(text))
	}
	_ = io.Discard
}
