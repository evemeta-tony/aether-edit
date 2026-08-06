// services/orchestrator/internal/objstore/fs_test.go
package objstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyValidation(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"", "/abs", "trailing/", "a//b", "../escape", "a/../b", "a/./b",
		"a/b\x00c", "sp ace/injected",
	} {
		if _, err := s.Path(bad); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
	p, err := s.Path("assets/ws1/sha256/abcdef")
	if err != nil {
		t.Fatalf("good key rejected: %v", err)
	}
	if filepath.Dir(filepath.Dir(filepath.Dir(p))) != filepath.Join(dir, "assets") {
		t.Errorf("resolved path outside expected tree: %s", p)
	}
}

func TestPutFileAndPutDir(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "out.mp4"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "seg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "seg", "chunk_00001.m4s"), []byte("seg"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := s.PutDir("outputs/ws1/job1/720p", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("stored %d keys, want 2: %v", len(keys), keys)
	}
	for _, k := range keys {
		ok, err := s.Exists(k)
		if err != nil || !ok {
			t.Errorf("key %s missing after PutDir (err %v)", k, err)
		}
	}
}
