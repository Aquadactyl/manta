package filesystem

import (
	"io"
	"sync"

	"github.com/gabriel-vasile/mimetype"
)

// Keep the detector's existing 4 KiB content probe. Reuse only scratch storage,
// never MIME results: container processes can change files between requests.
const mimeProbeBytes = 4096

var mimeProbePool = sync.Pool{New: func() any { return new([mimeProbeBytes]byte) }}

func detectFileMIME(r io.Reader) (*mimetype.MIME, error) {
	buf := mimeProbePool.Get().(*[mimeProbeBytes]byte)
	defer mimeProbePool.Put(buf)
	n, err := io.ReadFull(r, buf[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return mimetype.Detect(buf[:n]), nil
}
