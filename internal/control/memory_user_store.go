package control

import (
	"fmt"
)

var (
	errUserNotFound      = fmt.Errorf("user not found")
	errUserAlreadyExists = fmt.Errorf("user already exists")
)

type MemoryUserStore struct {
	users map[string]*User
}

func NewMemoryUserStore(users map[string]*User) *MemoryUserStore {
	return &MemoryUserStore{
		users: users,
	}
}

func (s *MemoryUserStore) GetUserByID(id string) (*User, error) {
	if user, ok := s.users[id]; ok {
		return user, nil
	}
	return nil, errUserNotFound
}

func (s *MemoryUserStore) AddUser(user *User) error {
	if _, exists := s.users[user.ID]; exists {
		return errUserAlreadyExists
	}
	s.users[user.ID] = user
	return nil
}
