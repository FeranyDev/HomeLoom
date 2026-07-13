package homekit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type secureFSStore struct{ path string }

func newSecureFSStore(path string) (*secureFSStore, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, fmt.Errorf("create HomeKit identity directory: %w", err)
	}
	if err := filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("HomeKit identity path contains symlink %q", current)
		}
		mode := os.FileMode(0o600)
		if entry.IsDir() {
			mode = 0o700
		}
		return os.Chmod(current, mode)
	}); err != nil {
		return nil, fmt.Errorf("secure HomeKit identity directory: %w", err)
	}
	return &secureFSStore{path: path}, nil
}

func (s *secureFSStore) Set(key string, value []byte) error {
	path, err := s.keyPath(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, value, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (s *secureFSStore) Get(key string) ([]byte, error) {
	path, err := s.keyPath(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (s *secureFSStore) Delete(key string) error {
	path, err := s.keyPath(key)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s *secureFSStore) KeysWithSuffix(suffix string) ([]string, error) {
	entries, err := os.ReadDir(s.path)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && strings.HasSuffix(entry.Name(), suffix) {
			keys = append(keys, entry.Name())
		}
	}
	return keys, nil
}

func (s *secureFSStore) keyPath(key string) (string, error) {
	key = strings.ReplaceAll(key, ":", "")
	if key == "" || filepath.Base(key) != key || key == "." {
		return "", fmt.Errorf("invalid HomeKit identity key %q", key)
	}
	return filepath.Join(s.path, key), nil
}
