package config

import (
	"strings"
	"testing"
	"time"
)

const goodConfig = `
name: shop
images:
  - name: api
    build_args:
      env_file: .env.production
      prefix: VITE_
    build_per_environment: true
uses:
  - component: queue-service
    version: 1.8.0
    as: queue
workloads:
  - name: api
    kind: deployment
    images: [api, queue]
    manifests: [k8s/api.yaml]
    depends_on: [migrate]
    secrets: [app]
    rollout_timeout: 15m
  - name: migrate
    kind: cronjob
    images: [api]
    manifests: [k8s/migrate.yaml]
secrets:
  - name: app
    from: secret:shop/app
pilets:
  names: [cart]
  command: [npm, run, publish, "{name}"]
  env: secret:shop/pilets
`

func TestParseGoodConfig(t *testing.T) {
	c, err := ParseRepoConfig([]byte(goodConfig))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Workloads[0].RolloutTimeout.Duration; got != 15*time.Minute {
		t.Errorf("rollout_timeout = %v, want 15m", got)
	}
	if c.Uses[0].LocalName() != "queue" {
		t.Errorf("LocalName = %q, want queue", c.Uses[0].LocalName())
	}
}

func TestParseIsStrict(t *testing.T) {
	cases := map[string]string{
		"unknown key":           "name: shop\nimage: []\n",
		"string for a boolean":  "name: shop\nworkloads:\n  - {name: a, kind: deployment, manifests: [a.yaml], manual: \"false\"}\n",
		"duplicate key":         "name: shop\nname: other\n",
		"bad duration":          "name: shop\nworkloads:\n  - {name: a, kind: deployment, manifests: [a.yaml], rollout_timeout: soon}\n",
		"empty file":            "   \n",
		"unknown nested key":    "name: shop\nimages:\n  - {name: a, buildargs: {}}\n",
		"list where a map goes": "name: shop\npilets: [a, b]\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRepoConfig([]byte(doc)); err == nil {
				t.Fatalf("want an error for:\n%s", doc)
			}
		})
	}
}

func TestValidateFindsEveryProblem(t *testing.T) {
	doc := `
name: Shop_App
images:
  - name: api
    context: ../shared
uses:
  - component: queue
  - component: cache
    version: latest
  - component: api
    commit: abc1234
workloads:
  - name: api
    kind: statefulset
    images: [missing]
    manifests: [/etc/passwd]
    depends_on: [api, ghost]
    secrets: [nope]
    manual: true
    disabled: true
  - name: a
    kind: deployment
    manifests: [a.yaml]
    depends_on: [b]
  - name: b
    kind: deployment
    manifests: [b.yaml]
    depends_on: [a]
secrets:
  - name: app
    from: kv/apps/shop
`
	c, err := ParseRepoConfig([]byte(doc))
	if err == nil {
		t.Fatalf("want errors, got %+v", c)
	}
	want := []string{
		`name: "Shop_App"`,
		`images[0].context: "../shared" must stay inside the repo`,
		`uses[0]: pin queue with version or commit`,
		`uses[1].version: "latest" is not a version`,
		`uses[2]: image name "api" is already used by images[0]`,
		`workloads[0].kind: "statefulset"`,
		`workloads[0].manifests[0]: "/etc/passwd" must be relative`,
		`workloads[0].images[0]: unknown image "missing"`,
		`workloads[0].secrets[0]: unknown secret "nope"`,
		`workloads[0]: a workload is manual or disabled, not both`,
		`workloads[0].depends_on[0]: a workload cannot depend on itself`,
		`workloads[0].depends_on[1]: unknown workload "ghost"`,
		`depends_on forms a cycle: a -> b -> a`,
		`secrets[0].from: "kv/apps/shop" is not a secret reference`,
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("missing problem %q in:\n%v", w, err)
		}
	}
}

func TestBuildSources(t *testing.T) {
	ok := "name: shop\nimages:\n  - name: api\n    context: ..\n    sources:\n      - {repo: example/lib, path: lib, commit: abc1234}\n"
	if _, err := ParseRepoConfig([]byte(ok)); err != nil {
		t.Fatalf("a parent context with sources should be valid: %v", err)
	}
	// The build folder holds only the repo's checkout, named after its repository.
	if _, err := ParseRepoConfig([]byte("name: shop\nimages:\n  - {name: api, context: ..}\n")); err != nil {
		t.Fatalf("a parent context without sources should be valid: %v", err)
	}
	cases := map[string]struct{ doc, want string }{
		"grandparent context": {"name: shop\nimages:\n  - name: api\n    context: ../..\n    sources: [{repo: example/lib, path: lib, commit: abc1234}]\n", "must stay inside the repo"},
		"unpinned source":     {"name: shop\nimages:\n  - name: api\n    context: ..\n    sources: [{repo: example/lib, path: lib, commit: main}]\n", "pin a commit hash"},
		"nested source path":  {"name: shop\nimages:\n  - name: api\n    context: ..\n    sources: [{repo: example/lib, path: a/b, commit: abc1234}]\n", "single folder name"},
		"same folder twice":   {"name: shop\nimages:\n  - name: api\n    context: ..\n    sources: [{repo: example/a, path: lib, commit: abc1234}, {repo: example/b, path: lib, commit: abc1234}]\n", "used twice"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRepoConfig([]byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestSecretRef(t *testing.T) {
	good := []string{"secret:shop/app", "secret:shop/feeds/prod", "secret:shop/app#API_KEY"}
	bad := []string{"shop/app", "secret:shop", "secret:/app", "secret:Shop/app", "secret:shop/app#", "vault:kv/x"}
	for _, s := range good {
		if err := SecretRef(s).Validate(); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := SecretRef(s).Validate(); err == nil {
			t.Errorf("%q: want an error", s)
		}
	}
}

const goodDeploy = `
name: shop
environments:
  staging:
    target: main
    namespace: shop-staging
    release: gitops
    approval: none
    on_merge: deploy
  prod:
    target: main
    namespace: shop
    namespace_mode: shared
    release: gitops
    approval: passkey
    ci_required: true
    promote_from: staging
targets:
  main:
    cloud: aws
    account: "123456789012"
    region: us-east-2
    cluster: main
    platform: linux/arm64
secret_store:
  type: openbao
  openbao:
    address: https://bao.example.com
    mount: kv
    layout: "{client}/{app}/{env}/{name}"
    client: acme
third_party:
  - docker.io/searxng/searxng
`

func TestParseGoodDeploy(t *testing.T) {
	c, err := ParseRepoConfig([]byte(goodDeploy))
	if err != nil {
		t.Fatal(err)
	}
	if c.Targets["main"].Cluster != "main" || c.SecretStore.OpenBao.Client != "acme" {
		t.Errorf("%+v", c)
	}
}

func TestFeedEnvironment(t *testing.T) {
	doc := "name: a\npilets:\n  names: [cart]\n  path: web/{name}\nenvironments:\n  dev: {release: feed, approval: none, feed: {url: \"https://f.example.com/api/pilet/publish/a-dev\", api_key: \"secret:a/feed#FEED_API_KEY\"}}\n"
	if _, err := ParseRepoConfig([]byte(doc)); err != nil {
		t.Fatal(err)
	}
}

func TestDeployProblems(t *testing.T) {
	tg := "targets:\n  t: {cloud: aws, account: \"123456789012\", region: us-east-2, cluster: c}\n"
	env := "environments:\n  prod: {target: t, namespace: a, release: gitops, approval: none}\n"
	cases := map[string]struct{ doc, want string }{
		"unknown target":       {"name: a\n" + env, `"t" is not in targets`},
		"bad release":          {"name: a\n" + tg + "environments:\n  prod: {target: t, namespace: a, release: ftp, approval: none}\n", `"ftp" must be one of direct, gitops, feed`},
		"promote loop":         {"name: a\n" + tg + "environments:\n  x: {target: t, namespace: a, release: gitops, approval: none, promote_from: y}\n  y: {target: t, namespace: a, release: gitops, approval: none, promote_from: x}\n", "promotion forms a loop"},
		"third party with tag": {"name: a\n" + tg + env + "third_party: [docker.io/library/postgres:16]\n", "without tag or digest"},
		"bad account":          {"name: a\ntargets:\n  t: {cloud: aws, account: \"12\", region: us-east-2, cluster: c}\n" + env, "12-digit"},
		"no cluster":           {"name: a\ntargets:\n  t: {cloud: aws, account: \"123456789012\", region: us-east-2}\n" + env, "name the Kubernetes cluster"},
		"file store no dir":    {"name: a\nsecret_store: {type: file}\n", "name the folder"},
		"store type":           {"name: a\nsecret_store: {type: s3}\n", `"s3" must be one of openbao, file`},
		"feed without feed":    {"name: a\nenvironments:\n  dev: {release: feed, approval: none}\n", "needs a feed"},
		"feed key without key": {"name: a\nenvironments:\n  dev: {release: feed, approval: none, feed: {url: \"https://f.example.com/p\", api_key: \"secret:a/feed\"}}\n", "name the key within the secret"},
		"feed with namespace":  {"name: a\nenvironments:\n  dev: {release: feed, approval: none, namespace: a, feed: {url: \"https://f.example.com/p\", api_key: \"secret:a/feed#K\"}}\n", "no target or namespace"},
		"feed bad url":         {"name: a\nenvironments:\n  dev: {release: feed, approval: none, feed: {url: \"ftp://x\", api_key: \"secret:a/feed#K\"}}\n", "not an http(s) URL"},
		"credential field":     {"name: a\n" + "targets:\n  t: {cloud: aws, account: \"123456789012\", region: us-east-2, cluster: c, profile: me}\n", "unknown field"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRepoConfig([]byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestEncodeYAMLComments(t *testing.T) {
	c := RepoConfig{Name: "shop", Workloads: []Workload{
		{Name: "a", Kind: KindDeployment, Manifests: []string{"a.yaml"}},
		{Name: "b", Kind: KindDeployment, Manifests: []string{"b.yaml"}},
	}}
	var cm Comments
	cm.Head = []string{"top note"}
	cm.AddItem("workloads", 1, "second workload\nhas two lines")
	out, err := EncodeYAML(c, &cm)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"# top note\nname: shop", "  # second workload\n  # has two lines\n  - name: b"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if _, err := ParseRepoConfig(out); err != nil {
		t.Errorf("encoded config does not read back: %v", err)
	}
}
