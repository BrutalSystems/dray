package pilet

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/execx"
)

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, name := range []string{"package/package.json", "package/dist/index.js"} {
		body, ok := files[name]
		if !ok {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func manifest(t *testing.T, data []byte) map[string]any {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("no package.json: %v", err)
		}
		if h.Name == "package/package.json" {
			var m map[string]any
			b, _ := io.ReadAll(tr)
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			return m
		}
	}
}

const pkgJSON = `{"name":"@shop/pilet-cart","version":"1.2.0","main":"dist/index.js","piral":{"name":"shell"}}`

func TestRestamp(t *testing.T) {
	in := tgz(t, map[string]string{"package/package.json": pkgJSON, "package/dist/index.js": "x"})
	name, version, out, err := Restamp(in, "dev.abc1234")
	if err != nil || name != "@shop/pilet-cart" || version != "1.2.0-dev.abc1234" {
		t.Fatalf("%s %s %v", name, version, err)
	}
	m := manifest(t, out)
	if m["version"] != "1.2.0-dev.abc1234" || m["main"] != "dist/index.js" || m["piral"].(map[string]any)["name"] != "shell" {
		t.Errorf("manifest %v", m)
	}
	if _, v, same, _ := Restamp(in, ""); v != "1.2.0" || !bytes.Equal(same, in) && manifest(t, same)["version"] != "1.2.0" {
		t.Errorf("no suffix changed the version: %s", v)
	}
	pre := tgz(t, map[string]string{"package/package.json": `{"name":"a","version":"1.0.0-beta.1"}`})
	if _, _, _, err := Restamp(pre, "dev.x"); err == nil {
		t.Error("a version that already has a prerelease was stamped")
	}
	if _, _, _, err := Restamp(in, "bad suffix!"); err == nil {
		t.Error("an invalid suffix was accepted")
	}
	if _, _, _, err := Restamp(tgz(t, map[string]string{"package/dist/index.js": "x"}), ""); err == nil {
		t.Error("a package without package.json was accepted")
	}
}

func TestPublishBuildsPacksStampsAndUploads(t *testing.T) {
	var got struct {
		key, file string
		version   any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.key = r.Header.Get("X-Api-Key")
		f, h, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b, _ := io.ReadAll(f)
		got.file = h.Filename
		got.version = manifest(t, b)["version"]
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	repo := t.TempDir()
	dir := filepath.Join(repo, "packages", "pilet-cart")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	run := func(_ context.Context, c execx.Cmd) ([]byte, error) {
		calls = append(calls, c.Dir+": "+c.String())
		if len(c.Args) >= 2 && c.Args[len(c.Args)-2] == "--target" {
			// pilet pack: leave the package in the target folder.
			data := tgz(t, map[string]string{"package/package.json": pkgJSON, "package/dist/index.js": "x"})
			return nil, os.WriteFile(filepath.Join(c.Args[len(c.Args)-1], "shop-pilet-cart-1.2.0.tgz"), data, 0o600)
		}
		return nil, nil
	}
	o := Options{
		RepoDir: repo, Pilets: config.Pilets{Names: []string{"cart"}, Path: "packages/pilet-{name}", Build: []string{"yarn", "build:{name}"}},
		FeedURL: srv.URL, APIKey: "k3y", Suffix: "dev.abc1234", Run: run,
	}
	res, err := Publish(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Version != "1.2.0-dev.abc1234" {
		t.Errorf("result %+v", res)
	}
	if got.key != "k3y" || got.file != "shop-pilet-cart-1.2.0.tgz" || got.version != "1.2.0-dev.abc1234" {
		t.Errorf("feed got %+v", got)
	}
	if len(calls) != 2 || !strings.HasSuffix(calls[0], ": yarn build:cart") || !strings.Contains(calls[1], "npx --no-install pilet pack --target") || !strings.HasPrefix(calls[0], dir) {
		t.Errorf("calls %v", calls)
	}

	// A refusal from the feed is an error that says why.
	o.FeedURL = srv.URL + "/nope"
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "version exists", http.StatusConflict) })
	if _, err := Publish(context.Background(), o); err == nil || !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "version exists") {
		t.Errorf("refusal: %v", err)
	}

	// A dry run builds and packs but sends nothing.
	got.key = ""
	o.DryRun, o.FeedURL = true, srv.URL
	if _, err := Publish(context.Background(), o); err != nil || got.key != "" {
		t.Errorf("dry run: %v, uploaded %v", err, got.key != "")
	}
}

func TestPublishRefuses(t *testing.T) {
	o := Options{RepoDir: t.TempDir(), Pilets: config.Pilets{Names: []string{"cart"}}}
	if _, err := Publish(context.Background(), Options{RepoDir: o.RepoDir, Pilets: o.Pilets, Names: []string{"other"}}); err == nil || !strings.Contains(err.Error(), `unknown pilet "other"`) {
		t.Errorf("unknown pilet: %v", err)
	}
	if _, err := Dir("/repo", config.Pilets{Path: "../{name}"}, "x"); err == nil {
		t.Error("a pilet folder outside the repo was accepted")
	}
	if d, err := Dir("/repo", config.Pilets{}, "cart"); err != nil || d != "/repo/packages/cart" {
		t.Errorf("default folder %s %v", d, err)
	}
}
