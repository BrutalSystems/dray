package githubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDrayManifestAsksForLittle(t *testing.T) {
	m := DrayManifest("Dray", "https://example.com", "https://dray.example.com/webhooks/github", "http://127.0.0.1:1/callback", false)
	b, _ := json.Marshal(m)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	perms := got["default_permissions"].(map[string]any)
	if len(perms) != 4 || perms["contents"] != "read" || perms["checks"] != "write" || perms["actions"] != "read" || perms["metadata"] != "read" {
		t.Errorf("permissions %v", perms)
	}
	if got["public"] != false || got["hook_attributes"].(map[string]any)["active"] != false {
		t.Errorf("manifest %s", b)
	}
	if u := NewAppURL("Acme Co", "s t"); u != "https://github.com/organizations/Acme%20Co/settings/apps/new?state=s+t" {
		t.Errorf("url %s", u)
	}
}

func TestConvert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app-manifests/c0de/conversions" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12,"slug":"dray","html_url":"https://github.com/apps/dray","pem":"-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----\n","webhook_secret":"w"}`))
	}))
	defer srv.Close()
	c, err := Convert(context.Background(), nil, srv.URL, "c0de")
	if err != nil || c.ID != 12 || c.Slug != "dray" || !strings.Contains(c.PEM, "BEGIN") || c.WebhookSecret != "w" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Convert(context.Background(), nil, srv.URL, "used"); err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("a spent code: %v", err)
	}
}
