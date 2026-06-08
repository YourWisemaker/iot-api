package store

import (
	"sort"
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// CreateUser stores a new user, rejecting duplicate usernames.
func (s *MemoryStore) CreateUser(u models.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.users {
		if existing.Username == u.Username {
			return ErrDuplicateUser
		}
	}
	s.users[u.ID] = u
	return nil
}

// GetUserByUsername looks up a user by username.
func (s *MemoryStore) GetUserByUsername(username string) (models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Username == username {
			return u, nil
		}
	}
	return models.User{}, ErrNotFound
}

// GetUserByID looks up a user by ID.
func (s *MemoryStore) GetUserByID(id string) (models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return models.User{}, ErrNotFound
	}
	return u, nil
}

// ListUsers returns all users sorted by creation time.
func (s *MemoryStore) ListUsers() []models.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]models.User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// UpdateUser replaces a user's mutable fields (roles, password hash).
func (s *MemoryStore) UpdateUser(u models.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[u.ID]; !ok {
		return ErrNotFound
	}
	s.users[u.ID] = u
	return nil
}

// DeleteUser removes a user and any refresh tokens they hold.
func (s *MemoryStore) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return ErrNotFound
	}
	delete(s.users, id)
	for hash, rt := range s.refresh {
		if rt.UserID == id {
			delete(s.refresh, hash)
		}
	}
	return nil
}

// StoreRefreshToken persists a hashed refresh token.
func (s *MemoryStore) StoreRefreshToken(rt models.RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh[rt.TokenHash] = rt
	return nil
}

// GetRefreshToken returns a refresh token by its hash, treating expired tokens
// as not found.
func (s *MemoryStore) GetRefreshToken(tokenHash string) (models.RefreshToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rt, ok := s.refresh[tokenHash]
	if !ok {
		return models.RefreshToken{}, ErrNotFound
	}
	if !rt.ExpiresAt.IsZero() && time.Now().After(rt.ExpiresAt) {
		return models.RefreshToken{}, ErrNotFound
	}
	return rt, nil
}

// DeleteRefreshToken removes a single refresh token.
func (s *MemoryStore) DeleteRefreshToken(tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refresh, tokenHash)
	return nil
}

// DeleteUserRefreshTokens removes all refresh tokens for a user.
func (s *MemoryStore) DeleteUserRefreshTokens(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, rt := range s.refresh {
		if rt.UserID == userID {
			delete(s.refresh, hash)
		}
	}
	return nil
}
