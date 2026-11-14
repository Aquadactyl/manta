package main

import (
	"crypto/sha256"
	"errors"
	"sync/atomic"
	"testing"
)

func TestPerformanceAssertions(t *testing.T) {
	var next atomic.Int64
	if idx, ok := nextPerformanceIndex(&next, 1); !ok || idx != 0 {
		t.Fatalf("first performance index = %d, %v; want 0, true", idx, ok)
	}
	if idx, ok := nextPerformanceIndex(&next, 1); ok || idx != 1 {
		t.Fatalf("exhausted performance index = %d, %v; want 1, false", idx, ok)
	}

	if performanceRequestFailed(nil, 200, 200) {
		t.Fatal("successful request was marked failed")
	}
	if !performanceRequestFailed(nil, 500, 200) {
		t.Fatal("unexpected response status was accepted")
	}
	if !performanceRequestFailed(errors.New("request failed"), 200, 200) {
		t.Fatal("request error was accepted")
	}
	var failures atomic.Int64
	recordPerformanceFailure(&failures, nil, 200, 200)
	recordPerformanceFailure(&failures, nil, 500, 200)
	recordPerformanceFailure(&failures, errors.New("request failed"), 200, 200)
	if failures.Load() != 2 {
		t.Fatalf("recorded %d performance failures, want 2", failures.Load())
	}

	checkPerformanceStatus(204, 204, "unused")
	assertPanicsWith(t, "bad status", func() { checkPerformanceStatus(500, 204, "bad status") })

	data := []byte("verified")
	requirePerformanceHash(data, sha256.Sum256(data), "unused")
	assertPanicsWith(t, "hash mismatch", func() {
		requirePerformanceHash(data, sha256.Sum256([]byte("different")), "hash mismatch")
	})

	requireArchiveName("archive.tar.gz")
	assertPanicsWith(t, "archive name missing", func() { requireArchiveName("") })
}

func assertPanicsWith(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	fn()
}
