package keystore

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// File keeps the secret as base64url text in an owner-only file.
type File struct {
	Path string
}

// NewFile returns a file backend at path.
func NewFile(path string) *File { return &File{Path: path} }

// Name implements Backend.
func (*File) Name() string { return "file" }

// Get implements Backend. A file readable by anyone but the owner is refused
// on Unix, like ssh does for private keys.
func (f *File) Get() ([]byte, error) {
	if err := checkOwnerOnly(f.Path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("key file %s is not valid base64url", f.Path)
	}
	return b, nil
}

// Set implements Backend. The file appears atomically, already restricted.
func (f *File) Set(secret []byte) error {
	return WriteOwnerOnly(f.Path, []byte(base64.RawURLEncoding.EncodeToString(secret)+"\n"))
}

// WriteOwnerOnly atomically writes data to path, readable only by the current
// user (mode 0600, or an owner-only DACL on Windows).
func WriteOwnerOnly(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secret-*.tmp") // 0600 on Unix
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	// Restrict before any secret byte is written.
	if err = restrictToOwner(name); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
