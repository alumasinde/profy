// Package storage stores media bytes outside MySQL (plan §13.3, §17). The filesystem store
// keeps public and private files in separate roots so private documents can never be served
// by the public media route. An S3-compatible store can implement the same interface later.
package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Store saves and serves blobs by key.
type Store interface {
	Put(key string, data []byte) error
	Open(key string) (*os.File, error)
	Delete(key string) error
}

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9/_.-]{0,199}$`)

// ErrBadKey is returned for keys that could escape the root.
var ErrBadKey = errors.New("storage: invalid key")

// FS is a filesystem store rooted at a directory.
type FS struct{ root string }

// NewFS creates the root directory (0750) and returns a store.
func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("storage: create %s: %w", root, err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &FS{root: abs}, nil
}

func (f *FS) path(key string) (string, error) {
	if !keyPattern.MatchString(key) || strings.Contains(key, "..") || strings.Contains(key, "//") {
		return "", ErrBadKey
	}
	p := filepath.Join(f.root, filepath.FromSlash(key))
	if !strings.HasPrefix(p, f.root+string(filepath.Separator)) {
		return "", ErrBadKey
	}
	return p, nil
}

// Put writes atomically (temp file + rename) with 0640 permissions.
func (f *FS) Put(key string, data []byte) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Open opens a stored file for reading.
func (f *FS) Open(key string) (*os.File, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Delete removes a file; a missing file is not an error.
func (f *FS) Delete(key string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ReadAll is a helper for tests.
func ReadAll(s Store, key string) ([]byte, error) {
	f, err := s.Open(key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
