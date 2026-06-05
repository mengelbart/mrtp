package http

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/mengelbart/mrtp/internal/control"
)

const userKey ctxKey = "user"

type UserStore interface {
	GetUserByID(userID string) (*control.User, error)
}

func (a *API) User(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := getUserID(r)
		if userID == "" {
			slog.Error("user not found in context")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		user, err := a.userStore.GetUserByID(userID)
		if err != nil {
			slog.Error("failed to get user from store", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		next(w, r.WithContext(ctx))
	}
}

func getUser(r *http.Request) control.User {
	return r.Context().Value(userKey).(control.User)
}
