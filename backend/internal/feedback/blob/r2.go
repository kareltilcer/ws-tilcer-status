package blob

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// r2Region is what Cloudflare R2 expects in a SigV4 credential scope; R2 has no
// regions of its own.
const r2Region = "auto"

// R2Config configures the S3 client against a Cloudflare R2 bucket.
//
// ⚠ The credentials must be scoped to the attachments bucket ALONE and must not
// reach the Litestream bucket (V3-D21). status holds object-storage credentials
// for the first time here; they are the narrow ones.
type R2Config struct {
	Endpoint  string // account endpoint, https://<account>.r2.cloudflarestorage.com
	Bucket    string
	AccessKey string
	SecretKey string
}

// R2 is the Store implementation over Cloudflare R2's S3 API.
type R2 struct {
	client   *s3.Client
	presign  *s3.PresignClient
	bucket   string
	endpoint string
}

// NewR2 builds an R2-backed store. Path-style addressing keeps every request on
// the account endpoint that was configured, rather than on a per-bucket
// virtual host derived from it.
func NewR2(cfg R2Config) (*R2, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("blob: R2 endpoint, bucket and credentials are all required")
	}
	client := s3.New(s3.Options{
		Region:       r2Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	})
	return &R2{
		client:   client,
		presign:  s3.NewPresignClient(client),
		bucket:   cfg.Bucket,
		endpoint: cfg.Endpoint,
	}, nil
}

// PresignPut signs Content-Type and Content-Length at the exact size given.
//
// ⚠ Both header values are part of the signature, and that is the only size
// enforcement a presigned PUT can have (V3-D08, measured 2026-09-02). A PUT that
// declares a different length is refused outright with 403 SignatureDoesNotMatch;
// one that declares the signed length and sends more is TRUNCATED to it. Drop
// ContentLength from this call — as a simplification, or through an SDK bump —
// and the bucket becomes an open upload endpoint with no error anywhere, which is
// why TestPresignPutSignsContentLength asserts the signed-header set before any
// upload is attempted.
func (r *R2) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (Upload, error) {
	req, err := r.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(r.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return Upload{}, fmt.Errorf("blob: presign put %q: %w", key, err)
	}
	return Upload{
		URL: req.URL,
		Headers: map[string]string{
			"Content-Type":   contentType,
			"Content-Length": fmt.Sprintf("%d", size),
		},
		ExpiresAt: time.Now().UTC().Add(ttl),
	}, nil
}

// PresignGet mints a short-lived view URL. R2 serves range requests against it,
// so a long clip seeks correctly without this service implementing ranges — and
// no attachment byte passes through the droplet in either direction.
func (r *R2) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	req, err := r.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("blob: presign get %q: %w", key, err)
	}
	return req.URL, time.Now().UTC().Add(ttl), nil
}

// Head returns the object's stored size, or ErrNotFound. Any other failure is
// returned as-is: the claim step and the sweep both treat "absent" as a decision
// to act on, so a transport error must never be mistaken for one.
func (r *R2) Head(ctx context.Context, key string) (int64, error) {
	out, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return 0, ErrNotFound
		}
		var noKey *types.NoSuchKey
		if errors.As(err, &noKey) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("blob: head %q: %w", key, err)
	}
	if out.ContentLength == nil {
		return 0, fmt.Errorf("blob: head %q: no content length", key)
	}
	return *out.ContentLength, nil
}

// Delete removes an object; deleting one that is already gone is not an error
// (S3 DELETE is idempotent), which is what makes the sweep safe to re-run.
func (r *R2) Delete(ctx context.Context, key string) error {
	if _, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("blob: delete %q: %w", key, err)
	}
	return nil
}

// List returns every object under prefix, following continuation tokens to the
// end. A paging failure returns an error rather than the partial result: the
// sweep deletes what a listing does not explain, so a truncated listing that
// looked complete would delete live attachments.
func (r *R2) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	p := s3.NewListObjectsV2Paginator(r.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(r.bucket),
		Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("blob: list %q: %w", prefix, err)
		}
		for _, o := range page.Contents {
			obj := Object{Key: aws.ToString(o.Key)}
			if o.Size != nil {
				obj.Size = *o.Size
			}
			if o.LastModified != nil {
				obj.LastModified = o.LastModified.UTC()
			}
			out = append(out, obj)
		}
	}
	return out, nil
}
