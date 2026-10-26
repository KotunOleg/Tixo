package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

var keyPattern = regexp.MustCompile(`^[a-f0-9]{32}\.[a-z0-9]{1,8}$`)

var _ Store = (*Local)(nil)

type Local struct {
	root *os.Root
}

func New(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("storage: create dir: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("storage: open dir: %w", err)
	}
	return &Local{root: root}, nil
}

func (s *Local) Save(r io.Reader, ext string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("storage: random name: %w", err)
	}
	key := hex.EncodeToString(raw) + "." + ext
	f, err := s.root.OpenFile(key, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("storage: create file: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = s.root.Remove(key)
		return "", fmt.Errorf("storage: write file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = s.root.Remove(key)
		return "", fmt.Errorf("storage: close file: %w", err)
	}
	return key, nil
}

func (s *Local) Open(key string) (io.ReadCloser, error) {
	if !keyPattern.MatchString(key) {
		return nil, ErrNotFound
	}
	f, err := s.root.Open(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage: open file: %w", err)
	}
	return f, nil
}

func (s *Local) Delete(key string) error {
	if key == "" {
		return nil
	}
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("storage: invalid key %q", key)
	}
	err := s.root.Remove(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
