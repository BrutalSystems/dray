package validate

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// online skips when the schema sources cannot be reached.
func online(t *testing.T) {
	t.Helper()
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Head("https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v1.36.0-standalone-strict/deployment-apps-v1.json")
	if err != nil {
		t.Skip("schemas not reachable")
	}
	_ = resp.Body.Close()
}

func TestCheck(t *testing.T) {
	online(t)
	s, err := New(Options{KubernetesVersion: "1.36.0", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	good := `apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec:
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      containers:
        - name: api
          image: reg.example.com/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: {name: api}
spec:
  secretName: api-tls
  dnsNames: [api.example.com]
  issuerRef: {name: letsencrypt, kind: ClusterIssuer}
`
	if f := s.Check("k8s/api.yaml", []byte(good)); len(f) != 0 {
		t.Errorf("a valid Deployment and Certificate: %v", f)
	}
	bad := `apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec:
  replicas: "two"
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      containers:
        - name: api
          image: x
          imagePullPolicy: Always
          resoures: {}
`
	f := s.Check("k8s/api.yaml", []byte(bad))
	text := ""
	for _, x := range f {
		if !x.Error {
			t.Errorf("not an error: %v", x)
		}
		text += x.String() + "\n"
	}
	if !strings.Contains(text, "replicas") || !strings.Contains(text, "resoures") || !strings.Contains(text, "Deployment/api") {
		t.Errorf("want the wrong type and the misspelled field, got:\n%s", text)
	}
	unknown := "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec: {}\n"
	if f := s.Check("k8s/w.yaml", []byte(unknown)); len(f) != 1 || f[0].Error || !strings.Contains(f[0].Message, "no schema") {
		t.Errorf("an unknown kind should be a warning: %v", f)
	}
}
