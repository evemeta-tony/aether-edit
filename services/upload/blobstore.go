// services/upload/blobstore.go

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// CompletedPart pairs a part number with the ETag S3 returned for it.
type CompletedPart struct {
	PartNumber int32
	ETag       string
}

// BlobStore is the object storage surface the service needs. The
// production implementation is S3BlobStore against an S3 compatible
// endpoint (OVH Object Storage in production). Tests run the same
// S3BlobStore against a local fake endpoint.
type BlobStore interface {
	CreateMultipart(ctx context.Context, key, mime string) (uploadID string, err error)
	UploadPart(ctx context.Context, key, uploadID string, partNumber int32, body []byte) (etag string, err error)
	CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error
	AbortMultipart(ctx context.Context, key, uploadID string) error
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
	// HeadObject reports (sizeBytes, exists).
	HeadObject(ctx context.Context, key string) (int64, bool, error)
	// Copy performs a server side copy of srcKey to dstKey. sizeBytes
	// selects single or multipart copy.
	Copy(ctx context.Context, srcKey, dstKey string, sizeBytes int64) error
	Delete(ctx context.Context, key string) error
}

// copyPartSize is the part size used for multipart server side copies
// of objects above the single CopyObject limit.
const copyPartSize int64 = 512 << 20

// singleCopyLimit is the largest object S3 copies with one CopyObject.
const singleCopyLimit int64 = 5 << 30

// S3BlobStore implements BlobStore with the AWS SDK for Go v2.
type S3BlobStore struct {
	client *s3.Client
	bucket string
}

var _ BlobStore = (*S3BlobStore)(nil)

// NewS3BlobStore builds a client for an S3 compatible endpoint with
// static credentials. Checksum calculation is left at when required so
// non AWS endpoints are not forced to understand CRC trailers.
func NewS3BlobStore(endpoint, region, bucket, accessKey, secretKey string, pathStyle bool) (*S3BlobStore, error) {
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
	return &S3BlobStore{client: client, bucket: bucket}, nil
}

// CreateMultipart starts a multipart upload at key.
func (b *S3BlobStore) CreateMultipart(ctx context.Context, key, mime string) (string, error) {
	out, err := b.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(mime),
	})
	if err != nil {
		return "", fmt.Errorf("create multipart: %w", err)
	}
	return aws.ToString(out.UploadId), nil
}

// UploadPart writes one part and returns its ETag.
func (b *S3BlobStore) UploadPart(ctx context.Context, key, uploadID string, partNumber int32, body []byte) (string, error) {
	out, err := b.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:        aws.String(b.bucket),
		Key:           aws.String(key),
		UploadId:      aws.String(uploadID),
		PartNumber:    aws.Int32(partNumber),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		return "", fmt.Errorf("upload part %d: %w", partNumber, err)
	}
	return aws.ToString(out.ETag), nil
}

// CompleteMultipart finalizes the multipart upload with parts in
// ascending part number order.
func (b *S3BlobStore) CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	sorted := make([]CompletedPart, len(parts))
	copy(sorted, parts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PartNumber < sorted[j].PartNumber })
	completed := make([]types.CompletedPart, 0, len(sorted))
	for _, p := range sorted {
		completed = append(completed, types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(p.ETag),
		})
	}
	_, err := b.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(b.bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		return fmt.Errorf("complete multipart: %w", err)
	}
	return nil
}

// AbortMultipart cancels a multipart upload and its stored parts. A
// missing upload id is treated as already aborted.
func (b *S3BlobStore) AbortMultipart(ctx context.Context, key, uploadID string) error {
	_, err := b.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(b.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		var nsu *types.NoSuchUpload
		if errors.As(err, &nsu) {
			return nil
		}
		return fmt.Errorf("abort multipart: %w", err)
	}
	return nil
}

// GetObject streams the object at key.
func (b *S3BlobStore) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	return out.Body, nil
}

// HeadObject reports size and existence.
func (b *S3BlobStore) HeadObject(ctx context.Context, key string) (int64, bool, error) {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("head object: %w", err)
	}
	return aws.ToInt64(out.ContentLength), true, nil
}

// Copy performs a server side copy. Objects above singleCopyLimit use
// multipart copy with UploadPartCopy ranges.
func (b *S3BlobStore) Copy(ctx context.Context, srcKey, dstKey string, sizeBytes int64) error {
	source := url.PathEscape(b.bucket + "/" + srcKey)
	if sizeBytes <= singleCopyLimit {
		_, err := b.client.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     aws.String(b.bucket),
			Key:        aws.String(dstKey),
			CopySource: aws.String(source),
		})
		if err != nil {
			return fmt.Errorf("copy object: %w", err)
		}
		return nil
	}

	create, err := b.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(dstKey),
	})
	if err != nil {
		return fmt.Errorf("copy create multipart: %w", err)
	}
	uploadID := aws.ToString(create.UploadId)

	var parts []types.CompletedPart
	var partNumber int32 = 1
	for offset := int64(0); offset < sizeBytes; offset += copyPartSize {
		end := offset + copyPartSize - 1
		if end >= sizeBytes {
			end = sizeBytes - 1
		}
		out, err := b.client.UploadPartCopy(ctx, &s3.UploadPartCopyInput{
			Bucket:          aws.String(b.bucket),
			Key:             aws.String(dstKey),
			UploadId:        aws.String(uploadID),
			PartNumber:      aws.Int32(partNumber),
			CopySource:      aws.String(source),
			CopySourceRange: aws.String(fmt.Sprintf("bytes=%d-%d", offset, end)),
		})
		if err != nil {
			_ = b.AbortMultipart(ctx, dstKey, uploadID)
			return fmt.Errorf("copy part %d: %w", partNumber, err)
		}
		parts = append(parts, types.CompletedPart{
			PartNumber: aws.Int32(partNumber),
			ETag:       out.CopyPartResult.ETag,
		})
		partNumber++
	}
	_, err = b.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(b.bucket),
		Key:             aws.String(dstKey),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		_ = b.AbortMultipart(ctx, dstKey, uploadID)
		return fmt.Errorf("copy complete multipart: %w", err)
	}
	return nil
}

// Delete removes the object at key.
func (b *S3BlobStore) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}
