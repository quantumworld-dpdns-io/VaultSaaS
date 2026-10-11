// Package objects is the small object-store API used for audit anchors and
// reports: Cloudflare R2 through its S3-compatible API (not AWS S3 —
// principle 0), or a local directory in development.
package objects

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Store is implemented by Dir and R2.
type Store interface {
	Put(ctx context.Context, key string, body []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
	// PresignGet returns a short-lived download URL, or "" when the store
	// cannot sign URLs (local directory) and the caller should stream instead.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

var ErrNotFound = errors.New("objects: not found")

// ---- local directory -----------------------------------------------------------

// Dir stores objects as files. WriteOnce mirrors an R2 bucket lock.
type Dir struct {
	Root      string
	WriteOnce bool
}

func (d Dir) path(key string) string {
	return filepath.Join(d.Root, filepath.FromSlash(strings.TrimPrefix(key, "/")))
}

func (d Dir) Put(_ context.Context, key string, body []byte) error {
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if d.WriteOnce {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(p, flags, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(body)
	return err
}

func (d Dir) Get(_ context.Context, key string) ([]byte, error) {
	b, err := os.ReadFile(d.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

func (d Dir) PresignGet(context.Context, string, time.Duration) (string, error) { return "", nil }

func (d Dir) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	// WalkDir yields cleaned paths, so the root must be cleaned too.
	root := filepath.Clean(d.Root)
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return err
		}
		if e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		k := filepath.ToSlash(rel)
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
		return nil
	})
	return keys, err
}

// ---- Cloudflare R2 (S3-compatible API; not AWS S3, see principle 0) -----------

type R2 struct {
	client    *s3.Client
	bucket    string
	writeOnce bool
}

// NewR2 connects to one bucket. writeOnce adds If-None-Match: * so an
// existing object is never replaced (the audit-anchor bucket also has a lock).
func NewR2(endpoint, bucket, accessKey, secretKey string, writeOnce bool) *R2 {
	c := s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		UsePathStyle: true,
	})
	return &R2{client: c, bucket: bucket, writeOnce: writeOnce}
}

func (r *R2) Put(ctx context.Context, key string, body []byte) error {
	in := &s3.PutObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key), Body: bytes.NewReader(body),
		ContentType: aws.String(contentType(key))}
	if r.writeOnce {
		in.IfNoneMatch = aws.String("*")
	}
	_, err := r.client.PutObject(ctx, in)
	return err
}

func contentType(key string) string {
	switch {
	case strings.HasSuffix(key, ".csv"):
		return "text/csv; charset=utf-8"
	case strings.HasSuffix(key, ".json"):
		return "application/json"
	}
	return "application/octet-stream"
}

func (r *R2) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s3.NewPresignClient(r.client).PresignGetObject(ctx,
		&s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func (r *R2) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(io.LimitReader(out.Body, 1<<20))
}

func (r *R2) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	p := s3.NewListObjectsV2Paginator(r.client, &s3.ListObjectsV2Input{Bucket: aws.String(r.bucket), Prefix: aws.String(prefix)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	return keys, nil
}
