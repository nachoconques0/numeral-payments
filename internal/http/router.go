// Package http builds the HTTP router.
package http

import (
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"numeral-payments/internal/config"
	paymentController "numeral-payments/internal/controller/payment"
	apperrors "numeral-payments/internal/errors"
	"numeral-payments/internal/http/middleware"
)

// NewRouter returns the router with every route of the service.
func NewRouter(auth config.Auth, paymentCtrl *paymentController.Controller) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	router.HandleMethodNotAllowed = true
	router.NoMethod(func(ctx *gin.Context) {
		appErr := apperrors.MethodNotAllowed("method not allowed for this path", nil)
		ctx.JSON(appErr.Code, appErr)
	})
	router.NoRoute(func(ctx *gin.Context) {
		appErr := apperrors.NotFound("route not found", nil)
		ctx.JSON(appErr.Code, appErr)
	})

	router.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(nethttp.StatusOK, gin.H{"status": "ok"})
	})

	// Everything below requires HTTP basic auth.
	secured := router.Group("/", middleware.BasicAuth(auth.Username, auth.Password))
	secured.POST("/payments", paymentCtrl.Create)

	return router
}
