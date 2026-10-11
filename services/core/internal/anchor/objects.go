package anchor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ---- local directory -----------------------------------------------------------

// Put refuses to overwrite, mirroring the R2 bucket lock.
func (d Dir) Put(_ context.Context, key string, body []byte) error {
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o440)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(body)
	return err
}

func (d Dir) Get(_ context.Context, key string) ([]byte, error) { return os.ReadFile(d.path(key)) }

func (d Dir) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(d.Root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return err
		}
		if e.IsDir() {
			return nil
		}
		k := strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(p, d.Root)), "/")
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
		return nil
	})
	return keys, err
}

// ---- Cloudflare R2 (S3-compatible API; not AWS S3, see principle 0) -----------

type R2 struct {
	client *s3.Client
	bucket string
}

func NewR2(endpoint, bucket, accessKey, secretKey string) *R2 {
	c := s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		UsePathStyle: true,
	})
	return &R2{client: c, bucket: bucket}
}

func (r *R2) Put(ctx context.Context, key string, body []byte) error {
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(r.bucket), Key: aws.String(key), Body: bytes.NewReader(body),
		ContentType: aws.String("application/json"), IfNoneMatch: aws.String("*"),
	})
	return err
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
