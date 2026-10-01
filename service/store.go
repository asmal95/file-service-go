// Package service — файловое хранилище с квотой.
//
// Формат на диске (в FILES_DIRECTORY):
//  1. <id>      — байты файла (id — 32 hex-символа из crypto/rand)
//  2. <id>.meta — JSON с метаданными (имя, тип, размер, время загрузки)
//
// При превышении квоты самые старые файлы удаляются автоматически.
package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound       = errors.New("file not found")
	ErrTooLarge       = errors.New("file exceeds size limit")
	ErrQuotaExceeded  = errors.New("file exceeds storage quota")
	ErrNoFileUploaded = errors.New("no file uploaded")
)

const metaSuffix = ".meta"

// Metadata — то же, что отдаёт GET /file/{id}/info (плюс created_at).
type Metadata struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Size        int64     `json:"size"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	dir   string
	quota int64

	mu    sync.RWMutex
	files map[string]*Metadata
	total int64
}

func New(dir string, quota int64) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir storage: %w", err)
	}
	s := &Store{dir: dir, quota: quota, files: map[string]*Metadata{}}
	if err := s.rescan(); err != nil {
		return nil, err
	}
	return s, nil
}

// rescan перестраивает индекс по файлам *.meta (вызывать под lock или до старта).
func (s *Store) rescan() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read storage dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, metaSuffix) {
			continue
		}
		id := strings.TrimSuffix(name, metaSuffix)
		raw, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		var m Metadata
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		// Сирота без тела — чистим.
		if _, err := os.Stat(filepath.Join(s.dir, id)); err != nil {
			_ = os.Remove(filepath.Join(s.dir, name))
			continue
		}
		m.ID = id
		s.files[id] = &m
		s.total += m.Size
	}
	return nil
}

func (s *Store) Stat(id string) (*Metadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.files[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (s *Store) Open(id string) (*os.File, *Metadata, error) {
	m, err := s.Stat(id)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(filepath.Join(s.dir, id))
	if err != nil {
		return nil, nil, ErrNotFound
	}
	return f, m, nil
}

func (s *Store) Usage() (total, quota int64, count int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total, s.quota, len(s.files)
}

// SaveStream пишет тело из src во временный файл (не более maxSize+1 байт
// для детекции переполнения) и возвращает путь времянки и реальный размер.
func (s *Store) SaveStream(src io.Reader, maxSize int64) (tmpPath string, size int64, err error) {
	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp: %w", err)
	}
	tmpPath = tmp.Name()
	size, err = io.Copy(tmp, io.LimitReader(src, maxSize+1))
	closeErr := tmp.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("write temp: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("close temp: %w", closeErr)
	}
	if size > maxSize {
		_ = os.Remove(tmpPath)
		return "", 0, ErrTooLarge
	}
	if size == 0 {
		_ = os.Remove(tmpPath)
		return "", 0, ErrNoFileUploaded
	}
	return tmpPath, size, nil
}

// Commit переносит времянку в хранилище: чистит старые файлы под квоту,
// выдаёт id, пишет метаданные. Атомарно насколько позволяет FS (rename).
func (s *Store) Commit(tmpPath, name, contentType, desc string, size int64) (*Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Освобождаем место: удаляем самые старые, пока не влезет новый файл.
	for s.total+size > s.quota {
		oldest := ""
		for id, m := range s.files {
			if oldest == "" || m.CreatedAt.Before(s.files[oldest].CreatedAt) {
				oldest = id
			}
		}
		if oldest == "" {
			_ = os.Remove(tmpPath)
			return nil, ErrQuotaExceeded
		}
		s.removeLocked(oldest)
	}

	id, err := newID()
	if err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("new id: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(s.dir, id)); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("commit file: %w", err)
	}
	m := &Metadata{
		ID: id, Name: name, Type: contentType, Size: size,
		Description: desc, CreatedAt: time.Now().UTC(),
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(s.dir, id+metaSuffix), raw, 0o644); err != nil {
		_ = os.Remove(filepath.Join(s.dir, id))
		return nil, fmt.Errorf("write metadata: %w", err)
	}
	s.files[id] = m
	s.total += size
	return m, nil
}

// DiscardTemp удаляет незакоммиченную времянку (например, при обрыве загрузки).
func (s *Store) DiscardTemp(tmpPath string) {
	_ = os.Remove(tmpPath)
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[id]; !ok {
		return ErrNotFound
	}
	s.removeLocked(id)
	return nil
}

func (s *Store) removeLocked(id string) {
	_ = os.Remove(filepath.Join(s.dir, id))
	_ = os.Remove(filepath.Join(s.dir, id+metaSuffix))
	if m, ok := s.files[id]; ok {
		s.total -= m.Size
		delete(s.files, id)
	}
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
