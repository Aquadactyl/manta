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

var transferNewRequest = http.NewRequestWithContext
var transferGetArchive = func(t *Transfer) (*Archive, error) { return t.Archive() }
var transferProgressInterval = 5 * time.Second

func watchArchiveStreamCancellation(ctx context.Context, src *io.PipeReader, streamFinished <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			// Archive.Stream closes its gzip writer before returning. If the
			// destination has stopped reading, that final flush can otherwise
			// block forever on this pipe after cancellation.
			_ = src.CloseWithError(ctx.Err())
		case <-streamFinished:
		}
	}()
	return done
}

// IsValidRedirectURL validates that the target URL uses http or https, has a valid
// host, and does not contain illegal characters or ambiguous authority components.
func IsValidRedirectURL(raw string) bool {
	_, ok := parseValidRedirectURL(raw)
	return ok
}

func parseValidRedirectURL(raw string) (*url.URL, bool) {
	if !validTransferURLRegexp.MatchString(raw) {
		return nil, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, false
	}
	return parsed, true
}

func isValidRedirectUrl(raw string) bool {
	return IsValidRedirectURL(raw)
}

// PushArchiveToTarget POSTs the archive to the target node and returns the
// response body.
func (t *Transfer) PushArchiveToTarget(targetURL, token string) ([]byte, error) {
	parsedURL, ok := parseValidRedirectURL(targetURL)
	if !ok {
		return nil, fmt.Errorf("transfer: invalid target URL %q", targetURL)
	}

	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()

	t.SendMessage("Preparing to stream server data to destination...")
	t.SetStatus(StatusProcessing)

	a, err := transferGetArchive(t)
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
	}(ctx2, a.Progress(), time.NewTicker(transferProgressInterval))

	// Create a new request using the pipe as the body.
	body, writer := io.Pipe()
	defer body.Close()
	defer writer.Close()
	req, err := transferNewRequest(ctx, http.MethodPost, parsedURL.String(), body)
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
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
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

		streamFinished := make(chan struct{})
		cancelWatcherDone := watchArchiveStreamCancellation(ctx, src, streamFinished)
		streamErr := a.Stream(ctx, pw)
		close(streamFinished)
		<-cancelWatcherDone
		if streamErr != nil {
			_ = pw.CloseWithError(streamErr)
			_ = src.CloseWithError(streamErr)
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
		finishTransferMultipart(mp, <-ch, hex.EncodeToString(h.Sum(nil)), errChan, func() {
			cancel2()
			t.SendMessage("Finished streaming archive to destination.")

			logTransferMultipartClose(t, mp.Close())
			t.Log().Debug("closed multipart writer")
		})
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
		cancel()
		_ = body.Close()
		<-producerDone
		t.Log().Debug("error while sending archive to destination")
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		cancel()
		_ = body.Close()
		<-producerDone
		return nil, fmt.Errorf("unexpected status code from destination: %d", res.StatusCode)
	}
	t.Log().Debug("waiting for stream to complete")
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err2 := <-errChan:
		t.Log().Debug("stream completed")
		if err2 != nil {
			return nil, transferStreamResultError(t, err2)
		}
		t.Log().Debug("received response from destination")

		v, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		return v, nil
	}
}

func finishTransferMultipart(mp interface{ WriteField(string, string) error }, copyErr error, checksum string, errChan chan<- error, onSuccess func()) {
	if copyErr != nil {
		errChan <- copyErr
		return
	}
	if err := mp.WriteField("checksum", checksum); err != nil {
		errChan <- errors.New("failed to stream checksum")
		return
	}
	onSuccess()
}

func logTransferMultipartClose(t *Transfer, err error) {
	if err != nil {
		t.Log().WithError(err).Error("error while closing multipart writer")
	}
}

func isTransferContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

func transferStreamResultError(t *Transfer, err error) error {
	if isTransferContextCanceled(err) {
		return err
	}
	t.Log().WithError(err).Debug("failed to send archive to destination")
	return fmt.Errorf("multipart error: %w", err)
}
