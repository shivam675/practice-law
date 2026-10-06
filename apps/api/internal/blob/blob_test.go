package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSRoundTrip(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	ctx := context.Background()

	body := "memorial contents"
	key := "org/123/doc/456.pdf"
	if err := store.Put(ctx, key, strings.NewReader(body), int64(len(body)), "application/pdf"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body {
		t.Fatalf("got %q, want %q", got, body)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteOfMissingKeyIsNotAnError(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	if err := store.Delete(context.Background(), "never/written"); err != nil {
		t.Fatalf("expected deleting a missing key to succeed, got %v", err)
	}
}

// A key is built from identifiers, but a traversal check is one comparison and
// the failure mode it prevents is arbitrary file write.
func TestKeysCannotEscapeTheRoot(t *testing.T) {
	root := t.TempDir()
	store, err := NewFS(root)
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	ctx := context.Background()

	for _, key := range []string{
		"../escaped.txt",
		"a/../../escaped.txt",
		"..\\escaped.txt",
		"",
	} {
		if err := store.Put(ctx, key, strings.NewReader("x"), 1, ""); err == nil {
			t.Errorf("Put(%q) should have been refused", key)
		}
		if _, err := store.Get(ctx, key); err == nil {
			t.Errorf("Get(%q) should have been refused", key)
		}
	}

	parent := filepath.Dir(root)
	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file was written outside the storage root")
	}
}

func TestPutRejectsASizeMismatch(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	// Claiming more bytes than the reader holds must fail rather than quietly
	// storing a truncated document.
	err = store.Put(context.Background(), "short.pdf", strings.NewReader("ab"), 10, "")
	if err == nil {
		t.Fatal("expected a size mismatch to be refused")
	}
}

func TestOpenUnknownDriver(t *testing.T) {
	if _, err := Open("gcs", ""); err == nil {
		t.Fatal("expected an unknown driver to be refused")
	}
	if _, err := Open("s3", ""); err == nil {
		t.Fatal("expected the unimplemented s3 driver to be refused at start-up")
	}
}
