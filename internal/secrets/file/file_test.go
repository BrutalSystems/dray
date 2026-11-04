package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/BrutalSystems/dray/internal/secrets"
	"github.com/BrutalSystems/dray/internal/secrets/storetest"
)

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) secrets.Store { return open(t, t.TempDir()) })
}

func TestFilesAreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "store"))
	if err := s.Put(context.Background(), "shop/app", secrets.Values{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"store", "store/shop", "store/shop/app.json"} {
		fi, err := os.Stat(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v, want no group or other access", p, fi.Mode().Perm())
		}
	}
}

func TestSymlinkOutOfTheStoreIsRefused(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "app.json"), []byte(`{"A":"stolen"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "shop")); err != nil {
		t.Fatal(err)
	}
	s := open(t, dir)
	if _, err := s.Get(context.Background(), "shop/app"); err == nil {
		t.Fatal("read a secret through a symlink out of the store")
	}
}
