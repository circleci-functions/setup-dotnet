package dotnetdist

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const downloadAttempts = 3

// retryDelay is the wait before the first retry; each later one waits longer.
var retryDelay = 2 * time.Second

// ErrNotFound is returned for an archive the server does not have.
var ErrNotFound = errors.New("not found")

// download streams url to dest, retrying failures other than a 404, and
// fails unless the file's SHA-512 is wantSHA512.
func download(ctx context.Context, client *http.Client, url, dest, wantSHA512 string) error {
	var err error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		if err = downloadOnce(ctx, client, url, dest, wantSHA512); err == nil || errors.Is(err, ErrNotFound) {
			return err
		}
		if attempt < downloadAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * retryDelay):
			}
		}
	}
	return err
}

func downloadOnce(ctx context.Context, client *http.Client, url, dest, wantSHA512 string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("download %s: %w", url, ErrNotFound)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil { //nolint:gosec // a download cache is not secret
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	h := sha512.New()
	_, copyErr := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	closeErr := tmp.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(wantSHA512) {
		return fmt.Errorf("download %s: SHA-512 is %s, want %s", url, got, wantSHA512)
	}
	return os.Rename(tmp.Name(), dest)
}

// verify reports whether path exists with the given SHA-512.
func verify(path, wantSHA512 string) bool {
	f, err := os.Open(path) //nolint:gosec // the caller names the file
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == strings.ToLower(wantSHA512)
}
