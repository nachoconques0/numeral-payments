// Package middleware holds the HTTP middleware shared by the routes.
package middleware

import (
	"crypto/subtle"

	"github.com/gin-gonic/gin"

	apperrors "numeral-payments/internal/errors"
)

const authRealm = `Basic realm="numeral"`

// BasicAuth rejects any request that does not carry the configured credentials.
// Both fields are compared in constant time so the handler does not leak how
// much of a guessed credential was correct.
func BasicAuth(username, password string) gin.HandlerFunc {
	expectedUser := []byte(username)
	expectedPassword := []byte(password)

	return func(ctx *gin.Context) {
		user, pass, ok := ctx.Request.BasicAuth()

		userMatches := subtle.ConstantTimeCompare([]byte(user), expectedUser) == 1
		passwordMatches := subtle.ConstantTimeCompare([]byte(pass), expectedPassword) == 1

		if !ok || !userMatches || !passwordMatches {
			ctx.Header("WWW-Authenticate", authRealm)
			appErr := apperrors.Unauthorized("invalid credentials", nil)
			ctx.AbortWithStatusJSON(appErr.Code, appErr)
			return
		}

		ctx.Next()
	}
}
