// Package filestore keeps the owner state as a JSON file in DATA_DIR (used without a database).
package filestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

type Store struct{ file string }

func New(dataDir string) *Store { return &Store{file: filepath.Join(dataDir, "state.json")} }

func (s *Store) Load(context.Context) ([]byte, error) {
	b, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// Save writes atomically (temp file, then rename).
func (s *Store) Save(_ context.Context, doc []byte) error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, doc, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}
