// Package backups creates, restores and removes gzip-compressed tar archives of
// a server's data directory. Standard library only.
package backups

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Result describes a created backup.
type Result struct {
	Bytes    int64  `json:"bytes"`
	Checksum string `json:"checksum"`
}

// file is the on-disk path of a backup archive.
func file(backupRoot, serverUUID, backupUUID string) string {
	return filepath.Join(backupRoot, serverUUID, backupUUID+".tar.gz")
}

// Create archives a server's data directory into <backupRoot>/<server>/<uuid>.tar.gz
// and returns its size and SHA-256 checksum.
func Create(dataRoot, backupRoot, serverUUID, backupUUID string) (Result, error) {
	src := filepath.Join(dataRoot, serverUUID)
	dst := file(backupRoot, serverUUID, backupUUID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Result{}, err
	}

	out, err := os.Create(dst)
	if err != nil {
		return Result{}, err
	}
	defer out.Close()

	hash := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(out, hash))
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		// Skip the root itself and symlinks (which could escape the directory).
		if rel == "." || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)

		return err
	})
	if walkErr != nil {
		_ = tw.Close()
		_ = gz.Close()
		_ = os.Remove(dst)

		return Result{}, walkErr
	}

	if err := tw.Close(); err != nil {
		_ = gz.Close()

		return Result{}, err
	}
	if err := gz.Close(); err != nil {
		return Result{}, err
	}

	st, err := os.Stat(dst)
	if err != nil {
		return Result{}, err
	}

	return Result{Bytes: st.Size(), Checksum: hex.EncodeToString(hash.Sum(nil))}, nil
}

// Path returns the archive path if it exists.
func Path(backupRoot, serverUUID, backupUUID string) (string, error) {
	p := file(backupRoot, serverUUID, backupUUID)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}

	return p, nil
}

// Delete removes a backup archive.
func Delete(backupRoot, serverUUID, backupUUID string) error {
	err := os.Remove(file(backupRoot, serverUUID, backupUUID))
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

// Restore extracts a backup back into the server's data directory (overwriting
// existing files). Entries that would escape the directory are skipped.
func Restore(dataRoot, backupRoot, serverUUID, backupUUID string) error {
	dst := filepath.Join(dataRoot, serverUUID)
	root, err := filepath.Abs(dst)
	if err != nil {
		return err
	}

	f, err := os.Open(file(backupRoot, serverUUID, backupUUID))
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target, err := filepath.Abs(filepath.Join(dst, filepath.Clean("/"+hdr.Name)))
		if err != nil {
			return err
		}
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil { //nolint:gosec // trusted own archive
				_ = w.Close()

				return err
			}
			_ = w.Close()
		}
	}

	return nil
}
