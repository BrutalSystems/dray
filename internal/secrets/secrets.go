// Package secrets is Dray's view of a secret store. Core code depends only on
// the Store interface; adapters (file, openbao, sops, and later others) live in
// their own packages, so any store can replace the default (D5).
//
// Secrets are addressed by logical paths such as shop/app, the part of a
// secret:shop/app reference after the prefix. Each adapter decides where a
// path lives in its store; no store location ever appears in a repo.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/BrutalSystems/dray/internal/config"
)

// Values are one secret's keys and values, as in a Kubernetes Secret.
type Values map[string]string

// Keys returns the secret's key names, sorted. Use it to show a secret
// without its values.
func (v Values) Keys() []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Equal reports whether two secrets hold the same keys and values.
func (v Values) Equal(o Values) bool {
	if len(v) != len(o) {
		return false
	}
	for k, x := range v {
		if y, ok := o[k]; !ok || x != y {
			return false
		}
	}
	return true
}

// Store holds secrets by logical path.
type Store interface {
	// Get returns a secret's values, or an error wrapping ErrNotFound.
	Get(ctx context.Context, path string) (Values, error)
	// Put replaces a secret's values as a whole.
	Put(ctx context.Context, path string, v Values) error
	// Delete removes a secret. Deleting one that does not exist is not an error.
	Delete(ctx context.Context, path string) error
	// List returns the paths of every secret under prefix ("" for all),
	// sorted. A prefix is a path or a folder of paths, without a trailing slash.
	List(ctx context.Context, prefix string) ([]string, error)
	// String describes the store for messages, never including a credential.
	String() string
}

// Errors stores return, wrapped.
var (
	ErrNotFound = errors.New("secret not found")
	ErrReadOnly = errors.New("secret store is read-only")
)

var (
	segmentRE = regexp.MustCompile(`^[a-z0-9][-a-z0-9_.]*$`)
	keyRE     = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)
)

// CheckPath reports whether path is a valid logical path: two or more
// segments of lower-case letters, digits, '-', '_' and '.', each starting with
// a letter or digit, so no segment can be "." or "..".
func CheckPath(path string) error {
	segs := strings.Split(path, "/")
	if len(segs) < 2 {
		return fmt.Errorf("secret path %q: want scope/name", path)
	}
	for _, s := range segs {
		if !segmentRE.MatchString(s) {
			return fmt.Errorf("secret path %q: bad segment %q", path, s)
		}
	}
	return nil
}

// CheckPrefix is CheckPath for a List prefix, which may be empty or a single
// segment.
func CheckPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	for _, s := range strings.Split(prefix, "/") {
		if !segmentRE.MatchString(s) {
			return fmt.Errorf("secret prefix %q: bad segment %q", prefix, s)
		}
	}
	return nil
}

// CheckValues reports whether every key is a valid Kubernetes Secret key.
func CheckValues(v Values) error {
	if len(v) == 0 {
		return errors.New("a secret needs at least one key")
	}
	for _, k := range v.Keys() {
		if !keyRE.MatchString(k) {
			return fmt.Errorf("secret key %q: use letters, digits, '-', '_' and '.'", k)
		}
	}
	return nil
}

// UnderPrefix reports whether path is prefix itself or inside it.
func UnderPrefix(path, prefix string) bool {
	return prefix == "" || path == prefix || strings.HasPrefix(path, prefix+"/")
}

// SplitRef splits a secret: reference into its path and optional key.
func SplitRef(ref config.SecretRef) (path, key string, err error) {
	if err := ref.Validate(); err != nil {
		return "", "", err
	}
	path, key, _ = strings.Cut(strings.TrimPrefix(string(ref), "secret:"), "#")
	return path, key, nil
}

// Lookup reads a reference. A reference with a #key returns only that key.
func Lookup(ctx context.Context, s Store, ref config.SecretRef) (Values, error) {
	path, key, err := SplitRef(ref)
	if err != nil {
		return nil, err
	}
	v, err := s.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return v, nil
	}
	val, ok := v[key]
	if !ok {
		return nil, fmt.Errorf("%s: no key %q: %w", path, key, ErrNotFound)
	}
	return Values{key: val}, nil
}

// Memory is a Store held in memory, for tests and dry runs.
type Memory struct {
	mu sync.Mutex
	m  map[string]Values
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{m: map[string]Values{}} }

// Get implements Store.
func (s *Memory) Get(_ context.Context, path string) (Values, error) {
	if err := CheckPath(path); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[path]
	if !ok {
		return nil, fmt.Errorf("%s: %w", path, ErrNotFound)
	}
	return clone(v), nil
}

// Put implements Store.
func (s *Memory) Put(_ context.Context, path string, v Values) error {
	if err := CheckPath(path); err != nil {
		return err
	}
	if err := CheckValues(v); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[path] = clone(v)
	return nil
}

// Delete implements Store.
func (s *Memory) Delete(_ context.Context, path string) error {
	if err := CheckPath(path); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, path)
	return nil
}

// List implements Store.
func (s *Memory) List(_ context.Context, prefix string) ([]string, error) {
	if err := CheckPrefix(prefix); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for p := range s.m {
		if UnderPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Memory) String() string { return "memory" }

func clone(v Values) Values {
	out := make(Values, len(v))
	for k, x := range v {
		out[k] = x
	}
	return out
}
