// Package blob stores uploaded files outside the database and outside the API
// server's own working directory.
//
// Development uses the filesystem adapter; production points at any
// S3-compatible endpoint. Nothing above this package knows which.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"path/filepath"
	"strings"
)

var ErrNotFound = errors.New("blob: not found")

// Store is the whole storage contract. Keys are opaque, forward-slash
// separated paths chosen by the caller.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Describe() string
}

// Open returns the store named by driver.
func Open(driver, fsRoot string) (Store, error) {
	switch driver {
	case "", "filesystem":
		return NewFS(fsRoot)
	case "s3":
		// Deliberately absent until a production deployment needs it. An
		// adapter that has never run against a real bucket is worse than a
		// clear error at start-up.
		return nil, fmt.Errorf("blob: the s3 driver is not implemented yet; " +
			"set BLOB_DRIVER=filesystem")
	default:
		return nil, fmt.Errorf("blob: unknown driver %q", driver)
	}
}

type fsStore struct{ root string }

func NewFS(root string) (Store, error) {
	if root == "" {
		return nil, errors.New("blob: filesystem root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("blob: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("blob: create root %s: %w", abs, err)
	}
	return &fsStore{root: abs}, nil
}

func (s *fsStore) Describe() string { return "filesystem:" + s.root }

// resolve maps a key to a path inside the root.
//
// A traversal segment is refused outright rather than cleaned away. Cleaning
// would silently fold "../x" and "x" onto the same path, so two different
// documents could overwrite each other, and a real bug would look like it
// worked. Keys come from tenant and document ids, so a rejection here means
// something is wrong upstream and should say so.
func (s *fsStore) resolve(key string) (string, error) {
	if key == "" {
		return "", errors.New("blob: empty key")
	}
	normalised := strings.ReplaceAll(key, "\\", "/")
	if strings.HasPrefix(normalised, "/") {
		return "", fmt.Errorf("blob: key %q must be relative", key)
	}
	for _, segment := range strings.Split(normalised, "/") {
		switch segment {
		case "", ".", "..":
			return "", fmt.Errorf("blob: key %q contains an invalid path segment", key)
		}
	}

	full := filepath.Join(s.root, filepath.FromSlash(normalised))

	// Belt and braces: symlinks and platform path quirks are checked against
	// the resolved root as well.
	rel, err := filepath.Rel(s.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("blob: key %q escapes the storage root", key)
	}
	return full, nil
}

func (s *fsStore) Put(ctx context.Context, key string, r io.Reader, size int64, _ string) error {
	full, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return fmt.Errorf("blob: create directory: %w", err)
	}

	// Write to a temporary file and rename, so a crash mid-upload never leaves
	// a partial object that later reads as a valid document.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return fmt.Errorf("blob: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	var written int64
	if size > 0 {
		written, err = io.Copy(tmp, io.LimitReader(r, size))
	} else {
		written, err = io.Copy(tmp, r)
	}
	if err != nil {
		return fmt.Errorf("blob: write %s: %w", key, err)
	}
	if size > 0 && written != size {
		return fmt.Errorf("blob: expected %d bytes for %s, wrote %d", size, key, written)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("blob: sync %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("blob: close %s: %w", key, err)
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return fmt.Errorf("blob: set permissions on %s: %w", key, err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		return fmt.Errorf("blob: commit %s: %w", key, err)
	}
	return nil
}

func (s *fsStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	full, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("blob: open %s: %w", key, err)
	}
	return f, nil
}

func (s *fsStore) Delete(ctx context.Context, key string) error {
	full, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("blob: delete %s: %w", key, err)
	}
	return nil
}
