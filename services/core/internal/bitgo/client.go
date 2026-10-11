// Package bitgo is a minimal BitGo v2 REST client.
//
// Decision D5: VaultOps never initiates transfers or holds keys. The client
// therefore exposes read methods only (plus pending-approval decisions in
// Phase 2), and every outgoing request passes through guard(), which rejects
// any path that could build, sign or send a transaction.
package bitgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	http       *http.Client
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }
func WithMaxRetries(n int) Option          { return func(c *Client) { c.maxRetries = n } }

func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		http:       &http.Client{Timeout: 10 * time.Second},
		maxRetries: 3,
		sleep:      sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Error classification lets callers (the SQS worker) decide between retrying
// and dead-lettering.
type Error struct {
	Status    int
	Message   string
	Retryable bool
}

func (e *Error) Error() string { return fmt.Sprintf("bitgo: %d %s", e.Status, e.Message) }

var ErrForbiddenOperation = errors.New("bitgo: operation forbidden by VaultOps custody policy (D5)")

// IsRetryable reports whether err is a transient BitGo/network failure.
func IsRetryable(err error) bool {
	var be *Error
	if errors.As(err, &be) {
		return be.Retryable
	}
	return err != nil && !errors.Is(err, ErrForbiddenOperation) && !errors.Is(err, context.Canceled)
}

// forbiddenPath matches BitGo endpoints that move funds or touch key material.
var forbiddenPath = regexp.MustCompile(`(?i)/(sendcoins|sendmany|tx/build|tx/send|tx/initiate|signtx|signtransaction|consolidate|consolidateunspents|fanoutunspents|sweep|accelerate|keychain|key|keys|encrypt|decrypt)(/|$)`)

func guard(method, path string) error {
	if forbiddenPath.MatchString(path) {
		return ErrForbiddenOperation
	}
	switch method {
	case http.MethodGet:
		return nil
	case http.MethodPut:
		// Phase 2: approving/rejecting a pending approval is the only write.
		if strings.HasPrefix(path, "/api/v2/pendingapprovals/") {
			return nil
		}
	}
	return ErrForbiddenOperation
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	if err := guard(method, strings.ToLower(path)); err != nil {
		return err
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = b
	}
	var err error
	var wait time.Duration
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1))*200*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond
			if sErr := c.sleep(ctx, max(backoff, wait)); sErr != nil {
				return sErr
			}
		}
		wait, err = c.once(ctx, method, u, payload, out)
		if err == nil || !IsRetryable(err) {
			return err
		}
	}
	return err
}

// once performs a single request. The duration is a server-requested
// Retry-After delay, if any.
func (c *Client) once(ctx context.Context, method, u string, payload []byte, out any) (time.Duration, error) {
	var rdr io.Reader
	if payload != nil {
		rdr = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err // network errors are retryable
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode >= 300 {
		be := &Error{Status: resp.StatusCode, Message: errorMessage(data)}
		be.Retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		var wait time.Duration
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			wait = time.Duration(secs) * time.Second
		}
		return wait, be
	}
	if out == nil {
		return 0, nil
	}
	return 0, json.Unmarshal(data, out)
}

func errorMessage(body []byte) string {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil {
		if e.Error != "" {
			return e.Error
		}
		if e.Message != "" {
			return e.Message
		}
	}
	return ""
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
