package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
)

func TestPickEnv(t *testing.T) {
	c, err := config.ParseRepoConfig([]byte(`name: shop
targets:
  t1: {cloud: aws, account: "123456789012", region: us-east-2, cluster: c1}
environments:
  dev: {target: t1, namespace: shop-dev, release: direct, approval: none, allow_dirty: true}
  prod: {target: t1, namespace: shop, release: direct, approval: passkey}
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pickEnv(c, ""); err == nil || !strings.Contains(err.Error(), "dev, prod: name one with --env") {
		t.Errorf("two environments, none named: %v", err)
	}
	name, env, err := pickEnv(c, "dev")
	if err != nil || name != "dev" || !env.AllowDirty {
		t.Errorf("%s %+v %v", name, env, err)
	}
	if _, _, err := pickEnv(c, "qa"); err == nil {
		t.Error("an unknown environment was accepted")
	}
}

func TestUserConfigIsOptionalAndPersonal(t *testing.T) {
	dir := t.TempDir()
	u, _, err := loadUserConfig(filepath.Join(dir, "none.yaml"))
	if err != nil || len(u.AWSProfiles) != 0 {
		t.Fatalf("a missing settings file: %+v %v", u, err)
	}
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("aws_profiles:\n  \"123456789012\": deploy\nkube_contexts:\n  c1: my-c1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u, _, err = loadUserConfig(p)
	if err != nil || u.AWSProfiles["123456789012"] != "deploy" || u.KubeContexts["c1"] != "my-c1" {
		t.Fatalf("%+v %v", u, err)
	}
	if err := os.WriteFile(p, []byte("registry: ~/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadUserConfig(p); err == nil {
		t.Error("an unknown setting was accepted")
	}
}

func TestRepoStoreFillsTheClient(t *testing.T) {
	c := &config.RepoConfig{Name: "shop", SecretStore: &config.SecretStore{Type: "openbao", OpenBao: &config.OpenBaoStore{
		Address: "https://bao.example.com", Mount: "kv", Layout: "{client}/{app}/{env}/{name}", Client: "acme",
	}}}
	st := repoStore(c)
	if st.Type != "openbao" || st.OpenBao.Clients["shop"] != "acme" || st.OpenBao.Mount != "kv" {
		t.Errorf("%+v", st.OpenBao)
	}
}

func TestNeedsStore(t *testing.T) {
	c := &config.RepoConfig{Workloads: []config.Workload{
		{Name: "a"}, {Name: "b", Secrets: []string{"s"}, Manual: true},
	}}
	if needsStore(c, nil) {
		t.Error("only a manual workload uses secrets")
	}
	if !needsStore(c, []string{"b"}) {
		t.Error("b named, and it uses a secret")
	}
}

func TestPickFeedEnv(t *testing.T) {
	c, err := config.ParseRepoConfig([]byte(`name: shop
pilets: {names: [cart]}
targets:
  t1: {cloud: aws, account: "123456789012", region: us-east-2, cluster: c1}
environments:
  prod: {target: t1, namespace: shop, release: direct, approval: none}
  feed-dev: {release: feed, approval: none, feed: {url: "https://f.example.com/p", api_key: "secret:shop/feed#K"}}
`))
	if err != nil {
		t.Fatal(err)
	}
	if n, _, err := pickFeedEnv(c, ""); err != nil || n != "feed-dev" {
		t.Errorf("the only feed environment: %s %v", n, err)
	}
	if _, _, err := pickFeedEnv(c, "prod"); err == nil {
		t.Error("a cluster environment was taken as a feed")
	}
}
