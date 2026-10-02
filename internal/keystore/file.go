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

// Location implements Locator.
func (f *File) Location() string { return f.Path }

// Get implements Backend. A file accessible by anyone but the owner is
// refused, like ssh does for private keys (on Windows also one owned by
// another user; review 55 R55-089).
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

// WriteOwnerOnly atomically and durably writes data to path, readable only by
// the current user (mode 0600, or an owner-only DACL on Windows).
func WriteOwnerOnly(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	// Restricted from creation, before any secret byte is written.
	tmp, err := createPrivateTemp(dir)
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
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(dir)
}

// Delete removes the file. A missing file is not an error.
func (f *File) Delete() error {
	if err := os.Remove(f.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
