// Package file is a secret store kept as JSON files in a folder: one file per
// secret, at <dir>/<path>.json, readable only by its owner. It is meant for
// local development and for adopters with no secret server; values sit on
// disk unencrypted.
package file

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/BrutalSystems/dray/internal/secrets"
)

// Store is a folder of secret files. All access goes through an os.Root, so
// a symlink inside the folder cannot lead a read or write outside it.
type Store struct {
	dir  string
	root *os.Root
	mu   sync.Mutex
}

// Open opens (creating it if needed) the store in dir.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("file store: no folder set")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("file store: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("file store: %w", err)
	}
	return &Store{dir: dir, root: root}, nil
}

// Close releases the folder.
func (s *Store) Close() error { return s.root.Close() }

func (s *Store) String() string { return "file " + s.dir }

func fileFor(p string) string { return p + ".json" }

// Get implements secrets.Store.
func (s *Store) Get(_ context.Context, p string) (secrets.Values, error) {
	if err := secrets.CheckPath(p); err != nil {
		return nil, err
	}
	data, err := s.root.ReadFile(fileFor(p))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", p, secrets.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var v secrets.Values
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return v, nil
}

// Put implements secrets.Store. The file is written beside its final name
// and renamed into place, so a reader never sees half a secret.
func (s *Store) Put(_ context.Context, p string, v secrets.Values) error {
	if err := secrets.CheckPath(p); err != nil {
		return err
	}
	if err := secrets.CheckValues(v); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.root.MkdirAll(path.Dir(p), 0o700); err != nil {
		return err
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	tmp := path.Join(path.Dir(p), ".tmp-"+hex.EncodeToString(rnd[:]))
	if err := s.root.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := s.root.Rename(tmp, fileFor(p)); err != nil {
		_ = s.root.Remove(tmp)
		return err
	}
	return nil
}

// Delete implements secrets.Store.
func (s *Store) Delete(_ context.Context, p string) error {
	if err := secrets.CheckPath(p); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.root.Remove(fileFor(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// List implements secrets.Store.
func (s *Store) List(_ context.Context, prefix string) ([]string, error) {
	if err := secrets.CheckPrefix(prefix); err != nil {
		return nil, err
	}
	out := []string{}
	err := fs.WalkDir(s.root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(name, ".json") {
			return nil
		}
		p := strings.TrimSuffix(name, ".json")
		if secrets.CheckPath(p) == nil && secrets.UnderPrefix(p, prefix) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
