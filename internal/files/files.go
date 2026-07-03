// Package files provides a sandboxed file manager for a server's data
// directory, used by the HTTP API the panel's file manager talks to.
package files

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxReadBytes caps how much of a file the API will return/accept.
const maxReadBytes = 2 << 20 // 2 MiB

// Manager exposes file operations rooted at a base directory.
type Manager struct {
	Base string
}

// Entry describes a single file or directory.
type Entry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	Modified  int64  `json:"modified"`
}

// New returns a Manager rooted at base.
func New(base string) *Manager {
	return &Manager{Base: base}
}

// resolve maps a server uuid + relative path to an absolute path, guaranteeing
// the result stays inside the server's directory (no traversal).
func (m *Manager) resolve(uuid, rel string) (string, error) {
	root, err := filepath.Abs(filepath.Join(m.Base, uuid))
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.Clean("/"+rel)))
	if err != nil {
		return "", err
	}
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes server directory")
	}
	return target, nil
}

// List returns the entries in a server directory.
func (m *Manager) List(uuid, rel string) ([]Entry, error) {
	dir, err := m.resolve(uuid, rel)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(m.Base, uuid), 0o755); err != nil {
		return nil, err
	}

	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			continue
		}
		entries = append(entries, Entry{
			Name:      item.Name(),
			Directory: item.IsDir(),
			Size:      info.Size(),
			Modified:  info.ModTime().Unix(),
		})
	}
	return entries, nil
}

// Delete removes a file or directory (recursively) inside the server dir. It
// refuses to delete the server root itself.
func (m *Manager) Delete(uuid, rel string) error {
	path, err := m.resolve(uuid, rel)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(filepath.Join(m.Base, uuid))
	if err != nil {
		return err
	}
	if path == root {
		return fmt.Errorf("refusing to delete the server root")
	}
	return os.RemoveAll(path)
}

// Read returns the contents of a file (capped at maxReadBytes).
func (m *Manager) Read(uuid, rel string) (string, error) {
	path, err := m.resolve(uuid, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory")
	}
	if info.Size() > maxReadBytes {
		return "", fmt.Errorf("file too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Write creates or overwrites a file with the given contents.
func (m *Manager) Write(uuid, rel, contents string) error {
	if len(contents) > maxReadBytes {
		return fmt.Errorf("contents too large")
	}
	path, err := m.resolve(uuid, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(contents), 0o644)
}
