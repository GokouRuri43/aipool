package modelcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/local/aipool/internal/api"
)

type Store struct {
	dir   string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("model cache directory is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, locks: map[string]*sync.Mutex{}}, nil
}

func (s *Store) Status(digest string, size int64) (api.ModelCacheStatus, error) {
	if err := validate(digest, size); err != nil {
		return api.ModelCacheStatus{}, err
	}
	unlock := s.lock(digest)
	defer unlock()
	return s.statusUnlocked(digest, size)
}

func (s *Store) Append(digest string, size, offset int64, src io.Reader, chunkLimit int64) (api.ModelCacheStatus, error) {
	if err := validate(digest, size); err != nil {
		return api.ModelCacheStatus{}, err
	}
	if offset < 0 || chunkLimit <= 0 {
		return api.ModelCacheStatus{}, fmt.Errorf("invalid upload offset or chunk size")
	}
	unlock := s.lock(digest)
	defer unlock()
	status, err := s.statusUnlocked(digest, size)
	if err != nil || status.Ready {
		return status, err
	}
	if status.Received != offset {
		return status, &OffsetError{Expected: status.Received}
	}
	part := s.partPath(digest)
	file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return status, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(src, chunkLimit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return status, copyErr
	}
	if closeErr != nil {
		return status, closeErr
	}
	if written > chunkLimit || offset+written > size {
		_ = os.Truncate(part, offset)
		return status, fmt.Errorf("upload chunk exceeds allowed model size")
	}
	if written == 0 {
		return status, fmt.Errorf("upload chunk is empty")
	}
	status.Received = offset + written
	if status.Received == size {
		if err := verifyFile(part, digest); err != nil {
			_ = os.Remove(part)
			return api.ModelCacheStatus{Digest: digest, Size: size}, err
		}
		if err := os.Rename(part, s.Path(digest)); err != nil {
			return status, err
		}
		status.Ready = true
	}
	return status, nil
}

func (s *Store) Path(digest string) string     { return filepath.Join(s.dir, digest+".gguf") }
func (s *Store) partPath(digest string) string { return filepath.Join(s.dir, digest+".part") }

func (s *Store) statusUnlocked(digest string, size int64) (api.ModelCacheStatus, error) {
	status := api.ModelCacheStatus{Digest: digest, Size: size}
	if info, err := os.Stat(s.Path(digest)); err == nil {
		if info.Size() == size {
			status.Received, status.Ready = size, true
			return status, nil
		}
		return status, fmt.Errorf("cached model size does not match lease")
	} else if !os.IsNotExist(err) {
		return status, err
	}
	if info, err := os.Stat(s.partPath(digest)); err == nil {
		status.Received = info.Size()
		if info.Size() == size {
			if err := verifyFile(s.partPath(digest), digest); err != nil {
				_ = os.Remove(s.partPath(digest))
				status.Received = 0
				return status, nil
			}
			if err := os.Rename(s.partPath(digest), s.Path(digest)); err != nil {
				return status, err
			}
			status.Ready = true
		}
		if info.Size() > size {
			_ = os.Remove(s.partPath(digest))
			status.Received = 0
		}
	} else if !os.IsNotExist(err) {
		return status, err
	}
	return status, nil
}

func (s *Store) lock(digest string) func() {
	s.mu.Lock()
	lock := s.locks[digest]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[digest] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func validate(digest string, size int64) error {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size || size <= 0 {
		return fmt.Errorf("invalid model digest or size")
	}
	return nil
}

func verifyFile(path, digest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return fmt.Errorf("uploaded model SHA-256 mismatch")
	}
	return nil
}

type OffsetError struct{ Expected int64 }

func (e *OffsetError) Error() string {
	return fmt.Sprintf("upload offset mismatch; expected %d", e.Expected)
}
