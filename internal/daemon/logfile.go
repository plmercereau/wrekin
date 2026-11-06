package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// rotatingWriter appends to a file and rotates it by size, keeping a fixed
// number of older files (path.1 is the newest).
type rotatingWriter struct {
	path    string
	maxSize int64
	keep    int

	mu   sync.Mutex
	file *os.File
	size int64
}

func newRotatingWriter(path string, maxSize int64, keep int) (*rotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	w := &rotatingWriter{path: path, maxSize: maxSize, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file, w.size = f, info.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	for i := w.keep; i >= 1; i-- {
		from := w.path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", w.path, i-1)
		}
		to := fmt.Sprintf("%s.%d", w.path, i)
		if _, err := os.Stat(from); err == nil {
			if err := os.Rename(from, to); err != nil {
				return err
			}
		}
	}
	return w.open()
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
