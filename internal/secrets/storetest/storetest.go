// Package storetest is the conformance suite every secret store adapter
// passes. An adopter writing an adapter for another store runs it too:
//
//	func TestConformance(t *testing.T) {
//		storetest.Run(t, func(t *testing.T) secrets.Store { return newStore(t) })
//	}
//
// newStore must return an empty store; each subtest gets its own.
package storetest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/BrutalSystems/dray/internal/secrets"
)

// Run runs the conformance suite against stores made by newStore.
func Run(t *testing.T, newStore func(t *testing.T) secrets.Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("put then get", func(t *testing.T) {
		s := newStore(t)
		v := secrets.Values{
			"API_KEY":     "abc",
			"empty":       "",
			"multi.line":  "line one\nline two\n",
			"unicode-val": "café ✓ \"quoted\" 'single' $HOME ${X}",
		}
		must(t, s.Put(ctx, "shop/app", v))
		got, err := s.Get(ctx, "shop/app")
		must(t, err)
		if !reflect.DeepEqual(map[string]string(got), map[string]string(v)) {
			t.Fatalf("got %q, want %q", got, v)
		}
	})

	t.Run("missing secret is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Get(ctx, "shop/none"); !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("put replaces every key", func(t *testing.T) {
		s := newStore(t)
		must(t, s.Put(ctx, "shop/app", secrets.Values{"A": "1", "B": "2"}))
		must(t, s.Put(ctx, "shop/app", secrets.Values{"B": "3"}))
		got, err := s.Get(ctx, "shop/app")
		must(t, err)
		if !got.Equal(secrets.Values{"B": "3"}) {
			t.Fatalf("got %q, want only B=3", got)
		}
	})

	t.Run("get returns a copy", func(t *testing.T) {
		s := newStore(t)
		must(t, s.Put(ctx, "shop/app", secrets.Values{"A": "1"}))
		got, err := s.Get(ctx, "shop/app")
		must(t, err)
		got["A"] = "changed"
		again, err := s.Get(ctx, "shop/app")
		must(t, err)
		if again["A"] != "1" {
			t.Fatal("changing a returned secret changed the store")
		}
	})

	t.Run("list", func(t *testing.T) {
		s := newStore(t)
		for _, p := range []string{"shop/app", "shop/feeds/prod", "shop/feeds/dev", "shopping/app", "other/app"} {
			must(t, s.Put(ctx, p, secrets.Values{"K": p}))
		}
		cases := map[string][]string{
			"":           {"other/app", "shop/app", "shop/feeds/dev", "shop/feeds/prod", "shopping/app"},
			"shop":       {"shop/app", "shop/feeds/dev", "shop/feeds/prod"},
			"shop/feeds": {"shop/feeds/dev", "shop/feeds/prod"},
			"shop/app":   {"shop/app"},
			"none":       {},
		}
		for prefix, want := range cases {
			got, err := s.List(ctx, prefix)
			must(t, err)
			if len(got) == 0 && len(want) == 0 {
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("List(%q) = %v, want %v", prefix, got, want)
			}
		}
	})

	t.Run("delete", func(t *testing.T) {
		s := newStore(t)
		must(t, s.Put(ctx, "shop/app", secrets.Values{"A": "1"}))
		must(t, s.Put(ctx, "shop/app/nested", secrets.Values{"A": "2"}))
		must(t, s.Delete(ctx, "shop/app"))
		if _, err := s.Get(ctx, "shop/app"); !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("after delete: %v", err)
		}
		if _, err := s.Get(ctx, "shop/app/nested"); err != nil {
			t.Fatalf("deleting a secret removed one below it: %v", err)
		}
		must(t, s.Delete(ctx, "shop/app"))
	})

	t.Run("bad paths and keys are refused", func(t *testing.T) {
		s := newStore(t)
		for _, p := range []string{"", "app", "shop/", "/shop/app", "shop//app", "shop/../app", "shop/.hidden", "Shop/app", "shop/app#key"} {
			if err := s.Put(ctx, p, secrets.Values{"A": "1"}); err == nil {
				t.Errorf("Put(%q) succeeded", p)
			}
			if _, err := s.Get(ctx, p); err == nil || errors.Is(err, secrets.ErrNotFound) {
				t.Errorf("Get(%q): want a path error, got %v", p, err)
			}
		}
		for _, v := range []secrets.Values{{}, {"has space": "x"}, {"slash/key": "x"}} {
			if err := s.Put(ctx, "shop/app", v); err == nil {
				t.Errorf("Put with keys %v succeeded", v.Keys())
			}
		}
		if _, err := s.List(ctx, "../x"); err == nil {
			t.Error(`List("../x") succeeded`)
		}
	})

	t.Run("String names no credential", func(t *testing.T) {
		s := newStore(t)
		d := strings.ToLower(s.String())
		for _, bad := range []string{"token=", "password", "secret_id"} {
			if strings.Contains(d, bad) {
				t.Errorf("String() = %q mentions %q", s.String(), bad)
			}
		}
	})
}

// RunReadOnly checks a read-only store: every write fails with ErrReadOnly.
func RunReadOnly(t *testing.T, s secrets.Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.Put(ctx, "shop/app", secrets.Values{"A": "1"}); !errors.Is(err, secrets.ErrReadOnly) {
		t.Errorf("Put: want ErrReadOnly, got %v", err)
	}
	if err := s.Delete(ctx, "shop/app"); !errors.Is(err, secrets.ErrReadOnly) {
		t.Errorf("Delete: want ErrReadOnly, got %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
