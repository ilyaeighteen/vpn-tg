package users

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username,omitempty"`
	FirstName string    `json:"firstName,omitempty"`
	LastName  string    `json:"lastName,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	byID map[int64]User
}

func NewStore(path string) (*Store, error) {
	store := &Store{
		path: path,
		byID: make(map[int64]User),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Save(user User) error {
	if user.ID <= 0 {
		return nil
	}

	user.Username = normalizeUsername(user.Username)
	user.UpdatedAt = time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID[user.ID] = user
	return s.saveLocked()
}

func (s *Store) FindByUsername(username string) (User, error) {
	username = normalizeUsername(username)
	if username == "" {
		return User{}, ErrUserNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, user := range s.byID {
		if user.Username == username {
			return user, nil
		}
	}
	return User{}, ErrUserNotFound
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

	var loaded []User
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	for _, user := range loaded {
		if user.ID > 0 {
			user.Username = normalizeUsername(user.Username)
			s.byID[user.ID] = user
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	if dir := filepath.Dir(s.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	users := make([]User, 0, len(s.byID))
	for _, user := range s.byID {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o600)
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
}
