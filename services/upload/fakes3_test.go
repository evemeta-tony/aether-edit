// services/upload/fakes3_test.go

package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// fakeS3 is a minimal S3 compatible httptest backend covering the
// operations S3BlobStore uses: multipart create, part upload, complete,
// abort, get, head, copy, delete. Signatures are not validated; this is
// test double territory. The real S3BlobStore client runs against it,
// so the request and response wiring is exercised for real.
type fakeS3 struct {
	mu          sync.Mutex
	bucket      string
	objects     map[string][]byte
	contentType map[string]string
	uploads     map[string]*fakeMultipart
	nextUpload  int
	partPuts    int // total UploadPart calls, for idempotency tests
	completes   int // total CompleteMultipartUpload calls
	copies      int // total CopyObject calls
}

type fakeMultipart struct {
	key   string
	parts map[int][]byte
	etags map[int]string
}

func newFakeS3(bucket string) *fakeS3 {
	return &fakeS3{
		bucket:      bucket,
		objects:     make(map[string][]byte),
		contentType: make(map[string]string),
		uploads:     make(map[string]*fakeMultipart),
	}
}

func (f *fakeS3) key(r *http.Request) string {
	path := strings.TrimPrefix(r.URL.Path, "/")
	return strings.TrimPrefix(path, f.bucket+"/")
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := f.key(r)
	q := r.URL.Query()

	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.nextUpload++
		id := fmt.Sprintf("mp-%d", f.nextUpload)
		f.uploads[id] = &fakeMultipart{key: key, parts: map[int][]byte{}, etags: map[int]string{}}
		f.contentType[key] = r.Header.Get("Content-Type")
		writeXML(w, http.StatusOK, fmt.Sprintf(
			`<InitiateMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`,
			f.bucket, key, id))

	case r.Method == http.MethodPut && q.Has("partNumber") && q.Has("uploadId"):
		up, ok := f.uploads[q.Get("uploadId")]
		if !ok || up.key != key {
			f.errXML(w, http.StatusNotFound, "NoSuchUpload")
			return
		}
		n, err := strconv.Atoi(q.Get("partNumber"))
		if err != nil || n < 1 {
			f.errXML(w, http.StatusBadRequest, "InvalidPart")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.errXML(w, http.StatusBadRequest, "IncompleteBody")
			return
		}
		f.partPuts++
		up.parts[n] = body
		etag := fmt.Sprintf(`"part-%s-%d-%d"`, q.Get("uploadId"), n, len(body))
		up.etags[n] = etag
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodPost && q.Has("uploadId"):
		f.completes++
		up, ok := f.uploads[q.Get("uploadId")]
		if !ok || up.key != key {
			f.errXML(w, http.StatusNotFound, "NoSuchUpload")
			return
		}
		var req struct {
			Parts []struct {
				PartNumber int    `xml:"PartNumber"`
				ETag       string `xml:"ETag"`
			} `xml:"Part"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := xml.Unmarshal(raw, &req); err != nil {
			f.errXML(w, http.StatusBadRequest, "MalformedXML")
			return
		}
		nums := make([]int, 0, len(req.Parts))
		for _, p := range req.Parts {
			stored, ok := up.etags[p.PartNumber]
			if !ok || strings.Trim(stored, `"`) != strings.Trim(p.ETag, `"`) {
				f.errXML(w, http.StatusBadRequest, "InvalidPart")
				return
			}
			nums = append(nums, p.PartNumber)
		}
		sort.Ints(nums)
		var assembled []byte
		for _, n := range nums {
			assembled = append(assembled, up.parts[n]...)
		}
		f.objects[key] = assembled
		delete(f.uploads, q.Get("uploadId"))
		writeXML(w, http.StatusOK, fmt.Sprintf(
			`<CompleteMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><ETag>"done"</ETag></CompleteMultipartUploadResult>`,
			f.bucket, key))

	case r.Method == http.MethodDelete && q.Has("uploadId"):
		if _, ok := f.uploads[q.Get("uploadId")]; !ok {
			f.errXML(w, http.StatusNotFound, "NoSuchUpload")
			return
		}
		delete(f.uploads, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != "":
		f.copies++
		src, err := url.PathUnescape(r.Header.Get("x-amz-copy-source"))
		if err != nil {
			f.errXML(w, http.StatusBadRequest, "InvalidRequest")
			return
		}
		src = strings.TrimPrefix(strings.TrimPrefix(src, "/"), f.bucket+"/")
		data, ok := f.objects[src]
		if !ok {
			f.errXML(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		cp := make([]byte, len(data))
		copy(cp, data)
		f.objects[key] = cp
		f.contentType[key] = f.contentType[src]
		writeXML(w, http.StatusOK,
			`<CopyObjectResult><ETag>"copy"</ETag><LastModified>2026-01-01T00:00:00Z</LastModified></CopyObjectResult>`)

	case r.Method == http.MethodGet:
		data, ok := f.objects[key]
		if !ok {
			f.errXML(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)

	case r.Method == http.MethodHead:
		data, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)

	default:
		f.errXML(w, http.StatusBadRequest, "NotImplemented")
	}
}

func (f *fakeS3) object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[key]
	return data, ok
}

func (f *fakeS3) uploadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.uploads)
}

func (f *fakeS3) partPutCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.partPuts
}

func (f *fakeS3) completeCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.completes
}

func (f *fakeS3) copyCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.copies
}

func (f *fakeS3) errXML(w http.ResponseWriter, status int, code string) {
	writeXML(w, status, fmt.Sprintf(
		`<Error><Code>%s</Code><Message>%s</Message></Error>`, code, code))
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, xml.Header+body)
}
