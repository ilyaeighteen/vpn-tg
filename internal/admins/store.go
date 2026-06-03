package admins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var ErrLastAdmin = errors.New("cannot remove the last admin")
var ErrNoAdmins = errors.New("at least one admin is required; set INITIAL_ADMIN_IDS")

type Store struct {
	path string
	mu   sync.RWMutex
	ids  map[int64]struct{}
}

func NewStore(path string, initialIDs []int64) (*Store, error) {
	store := &Store{
		path: path,
		ids:  make(map[int64]struct{}),
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	changed := false
	for _, id := range initialIDs {
		if id == 0 {
			continue
		}
		if _, exists := store.ids[id]; !exists {
			store.ids[id] = struct{}{}
			changed = true
		}
	}

	if len(store.ids) == 0 {
		return nil, ErrNoAdmins
	}

	if changed {
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	}

	return store, nil
}

func (s *Store) IsAdmin(id int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.ids[id]
	return exists
}

func (s *Store) List() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]int64, 0, len(s.ids))
	for id := range s.ids {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *Store) Add(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ids[id] = struct{}{}
	return s.saveLocked()
}

func (s *Store) Remove(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.ids) <= 1 {
		return ErrLastAdmin
	}

	delete(s.ids, id)
	return s.saveLocked()
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var ids []int64
	if err := json.Unmarshal(data, &ids); err != nil {
		return err
	}
	for _, id := range ids {
		s.ids[id] = struct{}{}
	}
	return nil
}

func (s *Store) saveLocked() error {
	if dir := filepath.Dir(s.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	ids := make([]int64, 0, len(s.ids))
	for id := range s.ids {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	data, err := json.MarshalIndent(ids, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o600)
}
