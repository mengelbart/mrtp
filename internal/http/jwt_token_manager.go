package http

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var errInvalidToken = fmt.Errorf("invalid token")

type Claims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

type JWTTokenManager struct {
	secret []byte
}

func NewJWTTokenManager(secret []byte) *JWTTokenManager {
	if secret == nil {
		secret = []byte("secret_key")
	}
	return &JWTTokenManager{secret: secret}
}

func (j *JWTTokenManager) CreateToken(userID string) (string, error) {
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mrtp",
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(j.secret)
	return tokenString, err
}

func (j *JWTTokenManager) ValidateToken(tokenStr string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return j.secret, nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return "", errInvalidToken
	}
	return claims.UserID, nil
}
