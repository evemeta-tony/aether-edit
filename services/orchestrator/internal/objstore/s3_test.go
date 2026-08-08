// services/orchestrator/internal/objstore/s3_test.go
//
// S3 object store tests (DEFECT 2). The real Store client runs against a
// minimal S3-compatible httptest backend covering the operations the store
// uses: PutObject, GetObject, HeadObject. Request and response wiring is
// exercised for real. Key validation (S4) is tested independently of the
// backend.
package objstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is a tiny S3-compatible backend for PutObject/GetObject/HeadObject.
// Signatures are not validated; this is test-double territory.
type fakeS3 struct {
	mu      sync.Mutex
	bucket  string
	objects map[string][]byte
}

func newFakeS3(bucket string) *fakeS3 {
	return &fakeS3{bucket: bucket, objects: map[string][]byte{}}
}

func (f *fakeS3) key(r *http.Request) string {
	p := strings.TrimPrefix(r.URL.Path, "/")
	return strings.TrimPrefix(p, f.bucket+"/")
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(r)
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.objects[key] = body
		w.WriteHeader(http.StatusOK)
	case http.MethodHead:
		b, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		b, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newTestStore(t *testing.T) (*Store, *fakeS3) {
	t.Helper()
	fake := newFakeS3("bucket")
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	// Path-style so the bucket is in the URL path, matching f.key.
	s, err := NewS3(srv.URL, "us-east-1", "bucket", "ak", "sk", true)
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return s, fake
}

func TestExistsAndDownload(t *testing.T) {
	s, fake := newTestStore(t)
	ctx := context.Background()

	key := "assets/ws1/sha256/" + strings.Repeat("a", 64)
	ok, err := s.Exists(ctx, key)
	if err != nil || ok {
		t.Fatalf("Exists on absent key = (%v, %v), want (false, nil)", ok, err)
	}

	fake.objects[key] = []byte("video-bytes")
	ok, err = s.Exists(ctx, key)
	if err != nil || !ok {
		t.Fatalf("Exists on present key = (%v, %v), want (true, nil)", ok, err)
	}

	dst := filepath.Join(t.TempDir(), "nested", "source")
	if err := s.Download(ctx, key, dst); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != "video-bytes" {
		t.Fatalf("downloaded %q, want video-bytes", got)
	}
}

func TestPutDir(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	src := t.TempDir()
	want := map[string][]byte{
		"720p.mp4":            []byte("out"),
		"seg/chunk_00001.m4s": []byte("seg"),
	}
	if err := os.WriteFile(filepath.Join(src, "720p.mp4"), want["720p.mp4"], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "seg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "seg", "chunk_00001.m4s"), want["seg/chunk_00001.m4s"], 0o644); err != nil {
		t.Fatal(err)
	}

	prefix := "outputs/ws1/0190e3a0-3333-7abc-8def-0123456789ab/720p"
	keys, err := s.PutDir(ctx, prefix, src)
	if err != nil {
		t.Fatalf("PutDir: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("stored %d keys, want 2: %v", len(keys), keys)
	}
	// Assert the uploaded bytes round-trip through a real GetObject (via
	// Download), not merely that a key exists in the backend map. This makes
	// the upload path's correctness rest on this test, not on the mirror claim
	// alone (Argus PR#10 pass 2 finding C).
	for _, k := range keys {
		rel := strings.TrimPrefix(k, prefix+"/")
		exp, known := want[rel]
		if !known {
			t.Errorf("unexpected stored key %s", k)
			continue
		}
		dst := filepath.Join(t.TempDir(), "dl")
		if err := s.Download(ctx, k, dst); err != nil {
			t.Fatalf("Download uploaded key %s: %v", k, err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read downloaded %s: %v", k, err)
		}
		if string(got) != string(exp) {
			t.Errorf("key %s bytes = %q, want %q", k, got, exp)
		}
	}
}

func TestKeyValidationRejectsTraversal(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, bad := range []string{
		"", "/abs", "trailing/", "a//b", "../escape", "a/../b", "a/./b",
		"a/b\x00c", "sp ace/injected",
	} {
		if _, err := s.Exists(ctx, bad); err == nil {
			t.Errorf("Exists accepted unsafe key %q", bad)
		}
		if err := s.Download(ctx, bad, filepath.Join(t.TempDir(), "x")); err == nil {
			t.Errorf("Download accepted unsafe key %q", bad)
		}
	}
}

func TestNewS3RequiresBucket(t *testing.T) {
	if _, err := NewS3("http://example", "r", "", "ak", "sk", true); err == nil {
		t.Fatal("NewS3 must reject an empty bucket")
	}
}
