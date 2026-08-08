// services/orchestrator/internal/objstore/s3.go
//
// OVH S3 (S3-compatible) object store for the transcode farm, via the AWS SDK
// for Go v2, mirroring services/upload/blobstore.go. Objects are addressed by
// key: sources land at assets/<workspaceId>/sha256/<hex64> (written by FT-2)
// and the worker WRITES ladder outputs to outputs/<workspaceId>/<jobId>/...
// in the SAME bucket. ffprobe and ffmpeg run on a LOCAL temp file downloaded
// from S3, and their outputs are uploaded back; the store never hands a
// key-derived path to a subprocess. Keys are validated against strict
// patterns before any S3 call; path traversal is rejected structurally (S4).
package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// keySegment validates each path segment of an object key.
var keySegment = regexp.MustCompile(`^[A-Za-z0-9._$%-]{1,128}$`)

// Store is an S3-backed object store against an S3-compatible endpoint
// (OVH Object Storage in production).
type Store struct {
	client *s3.Client
	bucket string
}

// NewS3 builds a client for an S3-compatible endpoint with static
// credentials, mirroring services/upload/blobstore.go. bucket must be
// non-empty. Checksum calculation is left at when-required so non-AWS
// endpoints are not forced to understand CRC trailers.
func NewS3(endpoint, region, bucket, accessKey, secretKey string, pathStyle bool) (*Store, error) {
	if bucket == "" {
		return nil, fmt.Errorf("object store bucket is required")
	}
	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("s3 endpoint: %w", err)
	}
	awsCfg := aws.Config{
		Region:                     region,
		Credentials:                credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = pathStyle
	})
	return &Store{client: client, bucket: bucket}, nil
}

// validateKey checks an object key for structural safety (S4).
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

// Exists reports whether the object exists.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return false, nil
		}
		return false, fmt.Errorf("head object %s: %w", key, err)
	}
	return true, nil
}

// Download streams the object at key to a local file at dstPath, creating
// parent directories as needed. dstPath is a caller-owned scratch path (a
// local temp file), never derived from the key; the subprocess only ever
// sees dstPath.
func (s *Store) Download(ctx context.Context, key, dstPath string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("get object %s: %w", key, err)
	}
	defer out.Body.Close()
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, out.Body); err != nil {
		f.Close()
		os.Remove(dstPath)
		return fmt.Errorf("download object %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(dstPath)
		return err
	}
	return nil
}

// PutFile uploads a local file into the store under key.
func (s *Store) PutFile(ctx context.Context, key, srcPath, mime string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(st.Size()),
	}
	if mime != "" {
		in.ContentType = aws.String(mime)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}

// PutDir uploads every regular file under srcDir into the store below
// keyPrefix, preserving relative paths, and returns the stored keys.
func (s *Store) PutDir(ctx context.Context, keyPrefix, srcDir string) ([]string, error) {
	if err := validateKey(keyPrefix); err != nil {
		return nil, err
	}
	var keys []string
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
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
		if err := s.PutFile(ctx, key, p, ""); err != nil {
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
