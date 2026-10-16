package filesystem

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

import "github.com/gabriel-vasile/mimetype"

func TestMIMEProbeMatchesPreviousDetector(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"text", []byte("server-port=25565\nonline-mode=false\n")},
		{"unicode", []byte("\u96ea \U0001f41f")},
		{"json", []byte(`{"motd":"Hello","players":20}`)},
		{"html", []byte("<!DOCTYPE html><html><body>test</body></html>")},
		{"xml", []byte(`<?xml version="1.0"?><server/>`)},
		{"png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...)},
		{"gif", append([]byte("GIF89a"), make([]byte, 40)...)},
		{"zip", append([]byte("PK\x03\x04"), make([]byte, 100)...)},
		{"gzip", append([]byte("\x1f\x8b\x08"), make([]byte, 100)...)},
		{"binary", bytes.Repeat([]byte{0, 255, 7, 1}, 2048)},
		{"probe_boundary", append(bytes.Repeat([]byte("x"), mimeProbeBytes), bytes.Repeat([]byte{0}, 1024)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want, err := mimetype.DetectReader(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			for range 30 {
				reader := bytes.NewReader(tc.data)
				got, err := detectFileMIME(reader)
				if err != nil || got.String() != want.String() {
					t.Fatalf("got %v, %v; want %s", got, err, want)
				}
				if read := len(tc.data) - reader.Len(); read != min(len(tc.data), mimeProbeBytes) {
					t.Fatalf("probe consumed %d bytes", read)
				}
			}
		})
	}
}

type shortMIMEReader struct{ io.Reader }

func (r shortMIMEReader) Read(b []byte) (int, error) { return r.Reader.Read(b[:min(len(b), 3)]) }

func TestMIMEProbeShortReadsAndReuse(t *testing.T) {
	for _, data := range [][]byte{bytes.Repeat([]byte{0, 255}, 4096), []byte("short text"), nil, []byte("<html>text</html>")} {
		want, err := mimetype.DetectReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		got, err := detectFileMIME(shortMIMEReader{bytes.NewReader(data)})
		if err != nil || got.String() != want.String() {
			t.Fatalf("got %v, %v; want %s", got, err, want)
		}
	}
}

type failingMIMEReader struct{ err error }

func (r failingMIMEReader) Read([]byte) (int, error) { return 0, r.err }

func TestMIMEProbePropagatesReadFailure(t *testing.T) {
	want := errors.New("probe read failed")
	if _, err := detectFileMIME(failingMIMEReader{want}); !errors.Is(err, want) {
		t.Fatalf("read failure lost: %v", err)
	}
}
