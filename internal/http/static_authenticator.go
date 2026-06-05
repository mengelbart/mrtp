package http

import "fmt"

var errInvalidCredentials = fmt.Errorf("invalid credentials")

type StaticAuthenticator struct {
	users map[string]string // username -> password
}

func NewStaticAuthenticator(users map[string]string) *StaticAuthenticator {
	return &StaticAuthenticator{users: users}
}

func (a *StaticAuthenticator) authenticate(username, password string) error {
	if pass, ok := a.users[username]; ok && pass == password {
		return nil
	}
	return errInvalidCredentials
}
