// services/orchestrator/internal/objstore/fs.go
//
// Filesystem object store for the single-node farm. Objects are addressed by
// key (for example assets/<ws>/sha256/<hex64> for sources, and
// outputs/<workspaceId>/<jobId>/... for transcode outputs) rooted at a
// configured directory shared with the FT-2 upload service on this node.
// Keys are validated against strict patterns before touching the
// filesystem; path traversal is rejected structurally.
package objstore

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// keySegment validates each path segment of an object key.
var keySegment = regexp.MustCompile(`^[A-Za-z0-9._$%-]{1,128}$`)

// Store is a filesystem-backed object store.
type Store struct {
	root string
}

// New opens the store rooted at dir, which must already exist.
func New(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("object store root: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("object store root %s is not a directory", abs)
	}
	return &Store{root: abs}, nil
}

// validateKey checks an object key for structural safety.
func validateKey(key string) error {
	if key == "" || len(key) > 512 {
		return fmt.Errorf("object key must be 1..512 characters")
	}
	if strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return fmt.Errorf("object key must not start or end with /")
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "." || seg == ".." || !keySegment.MatchString(seg) {
			return fmt.Errorf("object key segment %q is not allowed", seg)
		}
	}
	return nil
}

// Path resolves a validated key to an absolute filesystem path.
func (s *Store) Path(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}

// Exists reports whether the object exists.
func (s *Store) Exists(key string) (bool, error) {
	p, err := s.Path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// PutFile copies a local file into the store under key.
func (s *Store) PutFile(key, srcPath string) error {
	dst, err := s.Path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// PutDir copies every regular file under srcDir into the store below
// keyPrefix, preserving relative paths. It returns the stored keys.
func (s *Store) PutDir(keyPrefix, srcDir string) ([]string, error) {
	if err := validateKey(keyPrefix); err != nil {
		return nil, err
	}
	var keys []string
	err := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		key := keyPrefix + "/" + filepath.ToSlash(rel)
		if err := s.PutFile(key, p); err != nil {
			return err
		}
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}
