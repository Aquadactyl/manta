package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/pterodactyl/wings/internal/progress"
)

var validTransferURLRegexp = regexp.MustCompile(`^https?://[a-zA-Z0-9.\-_~%:\[\]]+(/[^?#\s]*)?(\?[^#\s]*)?$`)

var transferCopyBuffers = sync.Pool{
	New: func() any {
		b := make([]byte, 64*1024)
		return &b
	},
}

// IsValidRedirectURL validates that the target URL uses http or https, has a valid
// host, and does not contain illegal characters or ambiguous authority components.
func IsValidRedirectURL(raw string) bool {
	if !validTransferURLRegexp.MatchString(raw) {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return true
}

func isValidRedirectUrl(raw string) bool {
	return IsValidRedirectURL(raw)
}

// PushArchiveToTarget POSTs the archive to the target node and returns the
// response body.
func (t *Transfer) PushArchiveToTarget(targetURL, token string) ([]byte, error) {
	if !isValidRedirectUrl(targetURL) {
		return nil, fmt.Errorf("transfer: invalid target URL %q", targetURL)
	}

	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("transfer: failed to parse target URL: %w", err)
	}

	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()

	t.SendMessage("Preparing to stream server data to destination...")
	t.SetStatus(StatusProcessing)

	a, err := t.Archive()
	if err != nil {
		t.Error(err, "Failed to get archive for transfer.")
		return nil, errors.New("failed to get archive for transfer")
	}

	t.SendMessage("Streaming archive to destination...")

	// Send the upload progress to the websocket every 5 seconds.
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	go func(ctx context.Context, p *progress.Progress, tc *time.Ticker) {
		defer tc.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-tc.C:
				t.SendMessage("Uploading " + p.Progress(25))
			}
		}
	}(ctx2, a.Progress(), time.NewTicker(5*time.Second))

	// Create a new request using the pipe as the body.
	body, writer := io.Pipe()
	defer body.Close()
	defer writer.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)

	// Create a new multipart writer that writes the archive to the pipe.
	mp := multipart.NewWriter(writer)
	defer mp.Close()
	req.Header.Set("Content-Type", mp.FormDataContentType())

	// Create a new goroutine to write the archive to the pipe used by the
	// multipart writer.
	errChan := make(chan error, 1)
	go func() {
		defer close(errChan)
		defer writer.Close()
		defer mp.Close()

		src, pw := io.Pipe()
		defer src.Close()
		defer pw.Close()

		h := sha256.New()
		tee := io.TeeReader(src, h)

		dest, err := mp.CreateFormFile("archive", "archive.tar.gz")
		if err != nil {
			errChan <- errors.New("failed to create form file")
			return
		}

		ch := make(chan error, 1)
		go func() {
			defer close(ch)

			bp := transferCopyBuffers.Get().(*[]byte)
			defer transferCopyBuffers.Put(bp)

			if _, err := io.CopyBuffer(dest, tee, *bp); err != nil {
				ch <- fmt.Errorf("failed to stream archive to destination: %w", err)
				return
			}

			t.Log().Debug("finished copying dest to tee")
		}()

		if err := a.Stream(ctx, pw); err != nil {
			_ = pw.CloseWithError(err)
			_ = src.CloseWithError(err)
			if copyErr := <-ch; copyErr != nil {
				t.Log().WithError(copyErr).Debug("copy ended with error during stream abort")
			}
			errChan <- errors.New("failed to stream archive to pipe")
			return
		}
		t.Log().Debug("finished streaming archive to pipe")

		// Close the pipe writer early to release resources and ensure that the data gets flushed.
		_ = pw.Close()

		// Wait for the copy to finish before we continue.
		t.Log().Debug("waiting on copy to finish")
		if err := <-ch; err != nil {
			errChan <- err
			return
		}

		if err := mp.WriteField("checksum", hex.EncodeToString(h.Sum(nil))); err != nil {
			errChan <- errors.New("failed to stream checksum")
			return
		}

		cancel2()
		t.SendMessage("Finished streaming archive to destination.")

		if err := mp.Close(); err != nil {
			t.Log().WithError(err).Error("error while closing multipart writer")
		}
		t.Log().Debug("closed multipart writer")
	}()

	t.Log().Debug("sending archive to destination")
	client := http.Client{
		Timeout: 0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := client.Do(req)
	if err != nil {
		t.Log().Debug("error while sending archive to destination")
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from destination: %d", res.StatusCode)
	}
	t.Log().Debug("waiting for stream to complete")
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err2 := <-errChan:
		t.Log().Debug("stream completed")
		if err2 != nil {
			if errors.Is(err2, context.Canceled) {
				return nil, err2
			}

			t.Log().WithError(err2).Debug("failed to send archive to destination")
			return nil, fmt.Errorf("multipart error: %w", err2)
		}
		defer res.Body.Close()
		t.Log().Debug("received response from destination")

		v, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		if res.StatusCode != http.StatusOK {
			return nil, errors.New(string(v))
		}

		return v, nil
	}
}
