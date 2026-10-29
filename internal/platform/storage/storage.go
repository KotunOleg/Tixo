package storage

import (
	"errors"
	"io"
)

var ErrNotFound = errors.New("storage: file not found")

type Store interface {
	Save(r io.Reader, ext string) (string, error)
	Open(key string) (io.ReadCloser, error)
	Delete(key string) error
}
