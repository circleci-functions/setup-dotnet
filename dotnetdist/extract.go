package dotnetdist

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/circleci-functions/toolkit/archive"
)

// extract unpacks a .tar.gz or .zip into dir.
func extract(archivePath, dir string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZip(archivePath, dir)
	}
	return extractTarGz(archivePath, dir)
}

func extractTarGz(archivePath, dir string) error {
	f, err := os.Open(archivePath) //nolint:gosec // the caller names the archive
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %w", archivePath, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", archivePath, err)
		}
		target, ok, err := entryPath(dir, hdr.Name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755) //nolint:gosec // an SDK is world-readable
		case tar.TypeReg:
			err = writeFile(target, tr, fs.FileMode(hdr.Mode).Perm())
		case tar.TypeSymlink:
			err = symlink(dir, target, hdr.Linkname)
		}
		if err != nil {
			return err
		}
	}
}

func extractZip(archivePath, dir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("%s: %w", archivePath, err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target, ok, err := entryPath(dir, zf.Name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil { //nolint:gosec // an SDK is world-readable
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("%s: %w", zf.Name, err)
		}
		err = writeFile(target, rc, 0o644)
		if cerr := rc.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// entryPath maps an archive entry to its path under dir, refusing one that
// would land outside it. ok is false for the archive's root entry.
func entryPath(dir, name string) (string, bool, error) {
	clean := path.Clean(strings.ReplaceAll(name, `\`, "/"))
	switch {
	case clean == ".":
		return "", false, nil
	case path.IsAbs(clean), clean == "..", strings.HasPrefix(clean, "../"):
		return "", false, fmt.Errorf("illegal path in archive: %s", name)
	}
	return filepath.Join(dir, filepath.FromSlash(clean)), true, nil
}

func writeFile(target string, r io.Reader, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // an SDK is world-readable
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	return archive.WriteFile(target, r, mode)
}

func symlink(dir, target, linkname string) error {
	resolved := filepath.Join(filepath.Dir(target), filepath.FromSlash(linkname))
	if filepath.IsAbs(linkname) || !within(dir, resolved) {
		return fmt.Errorf("illegal symlink in archive: %s -> %s", target, linkname)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // an SDK is world-readable
		return err
	}
	_ = os.Remove(target)
	return os.Symlink(linkname, target)
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// merge moves what staging holds into root. Directories root lacks move
// whole, so an SDK or runtime version appears complete or not at all. Files
// root already has are left alone, except top-level ones when replaceTop is
// set: the dotnet host and its licence files belong to the newest install.
// It returns the top-level files it could not replace, which on Windows is a
// dotnet.exe that is running.
func merge(staging, root string, replaceTop bool) ([]string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil { //nolint:gosec // an SDK is world-readable
		return nil, err
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return nil, err
	}
	var kept []string
	for _, e := range entries {
		src, dst := filepath.Join(staging, e.Name()), filepath.Join(root, e.Name())
		if e.IsDir() {
			if err := mergeDir(src, dst); err != nil {
				return kept, err
			}
			continue
		}
		if _, err := os.Lstat(dst); err == nil && !replaceTop {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			kept = append(kept, e.Name())
		}
	}
	return kept, nil
}

func mergeDir(src, dst string) error {
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		return os.Rename(src, dst)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := mergeDir(s, d); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Lstat(d); errors.Is(err, fs.ErrNotExist) {
			if err := os.Rename(s, d); err != nil {
				return err
			}
		}
	}
	return nil
}
