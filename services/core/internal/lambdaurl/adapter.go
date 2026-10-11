// Package lambdaurl adapts a net/http handler to Lambda Function URL events,
// so the same router runs on Lambda and as a plain local HTTP server.
package lambdaurl

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

// Handler returns a Lambda handler function that serves h.
func Handler(h http.Handler) func(context.Context, events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	return func(ctx context.Context, ev events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
		req, err := toRequest(ctx, ev)
		if err != nil {
			return events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest}, nil
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return toResponse(rec), nil
	}
}

func toRequest(ctx context.Context, ev events.LambdaFunctionURLRequest) (*http.Request, error) {
	body := []byte(ev.Body)
	if ev.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(ev.Body)
		if err != nil {
			return nil, err
		}
		body = b
	}
	u := url.URL{Path: ev.RawPath, RawQuery: ev.RawQueryString}
	req, err := http.NewRequestWithContext(ctx, ev.RequestContext.HTTP.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range ev.Headers {
		req.Header.Set(k, v)
	}
	if len(ev.Cookies) > 0 {
		req.Header.Set("Cookie", strings.Join(ev.Cookies, "; "))
	}
	req.RemoteAddr = ev.RequestContext.HTTP.SourceIP
	req.Host = ev.RequestContext.DomainName
	return req, nil
}

func toResponse(rec *httptest.ResponseRecorder) events.LambdaFunctionURLResponse {
	headers := make(map[string]string, len(rec.Header()))
	for k, v := range rec.Header() {
		headers[k] = strings.Join(v, ", ")
	}
	return events.LambdaFunctionURLResponse{
		StatusCode: rec.Code,
		Headers:    headers,
		Body:       rec.Body.String(),
	}
}
