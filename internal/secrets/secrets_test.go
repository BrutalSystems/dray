package secrets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/secrets"
	"github.com/BrutalSystems/dray/internal/secrets/storetest"
)

func TestMemoryConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) secrets.Store { return secrets.NewMemory() })
}

func TestLookup(t *testing.T) {
	ctx := context.Background()
	s := secrets.NewMemory()
	if err := s.Put(ctx, "shop/app", secrets.Values{"A": "1", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	v, err := secrets.Lookup(ctx, s, "secret:shop/app")
	if err != nil || len(v) != 2 {
		t.Fatalf("whole secret: %v %v", v, err)
	}
	v, err = secrets.Lookup(ctx, s, "secret:shop/app#B")
	if err != nil || !v.Equal(secrets.Values{"B": "2"}) {
		t.Fatalf("one key: %v %v", v, err)
	}
	if _, err := secrets.Lookup(ctx, s, "secret:shop/app#C"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("missing key: %v", err)
	}
	if _, err := secrets.Lookup(ctx, s, config.SecretRef("kv/shop")); err == nil {
		t.Error("a store path is not a reference")
	}
}
