package lambdaurl

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestHandlerRoundTrip(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/echo" || r.URL.Query().Get("x") != "1" {
			t.Errorf("bad request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-Test") != "yes" {
			t.Errorf("header lost")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		w.Write(b)
	})
	ev := events.LambdaFunctionURLRequest{
		RawPath:         "/v1/echo",
		RawQueryString:  "x=1",
		Headers:         map[string]string{"x-test": "yes"},
		Body:            base64.StdEncoding.EncodeToString([]byte("hello")),
		IsBase64Encoded: true,
	}
	ev.RequestContext.HTTP.Method = http.MethodPost
	resp, err := Handler(h)(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || resp.Body != "hello" || resp.Headers["Content-Type"] != "text/plain" {
		t.Fatalf("unexpected response %+v", resp)
	}
}
