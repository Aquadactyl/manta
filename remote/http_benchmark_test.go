package remote

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

type benchmarkTransport struct{}

func (benchmarkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: r}, nil
}

// Measure request handling for an already serialized 1 MiB body without
// network latency, so the cost of copying the body is visible.
func BenchmarkRequestBody(b *testing.B) {
	body := bytes.NewBuffer(make([]byte, 1<<20))
	c := New("http://panel.example", WithHttpClient(&http.Client{Transport: benchmarkTransport{}})).(*client)
	ctx := context.Background()
	b.SetBytes(int64(body.Len()))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := c.request(ctx, http.MethodPost, "/activity", body)
		if err != nil {
			b.Fatal(err)
		}
		_ = res.Body.Close()
	}
}
