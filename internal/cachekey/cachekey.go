// Package cachekey builds parts of job cache keys.
package cachekey

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// Path returns a short token for an absolute path. A cache entry restores to
// the path it was saved from, so jobs that keep a cache at different paths,
// such as under different home directories, must not share a key.
func Path(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	sum := sha256.Sum256([]byte(filepath.ToSlash(p)))
	return hex.EncodeToString(sum[:4])
}
