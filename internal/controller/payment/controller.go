// Package payment is the HTTP delivery layer for payments.
package payment

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	paymentEntity "numeral-payments/internal/entity/payment"
	apperrors "numeral-payments/internal/errors"
	"numeral-payments/internal/model"
)

// maxBodyBytes bounds how much of a request body is read into memory.
const maxBodyBytes = 1 << 20

// Service is the behaviour this controller needs from the payment service.
type Service interface {
	CreatePayment(ctx context.Context, in paymentEntity.Input) (*paymentEntity.Payment, error)
}

// Validator checks a raw request body against the payment schema, returning
// one message per violation.
type Validator interface {
	Validate(body []byte) []string
}

// Controller handles payment requests.
type Controller struct {
	service   Service
	validator Validator
}

// NewController returns a controller backed by service and validator.
func NewController(service Service, validator Validator) *Controller {
	return &Controller{service: service, validator: validator}
}

// Create handles POST /payments: it validates the request against the payment
// schema, stores it and deposits it with the bank.
func (c *Controller) Create(ctx *gin.Context) {
	if err := requireJSON(ctx); err != nil {
		respondError(ctx, err)
		return
	}

	body, err := readBody(ctx)
	if err != nil {
		respondError(ctx, err)
		return
	}

	if violations := c.validator.Validate(body); len(violations) > 0 {
		respondError(ctx, apperrors.BadRequest("the request does not match the payment schema", nil).WithDetails(violations...))
		return
	}

	var request model.CreatePaymentRequest
	if err := json.Unmarshal(body, &request); err != nil {
		respondError(ctx, apperrors.BadRequest("the request body could not be read as a payment", err))
		return
	}

	input, err := request.ToEntityInput()
	if err != nil {
		respondError(ctx, apperrors.BadRequest("invalid amount", err).WithDetails(err.Error()))
		return
	}

	payment, err := c.service.CreatePayment(ctx.Request.Context(), input)
	if err != nil {
		respondError(ctx, err)
		return
	}

	ctx.JSON(nethttp.StatusOK, model.NewPaymentResponse(payment))
}

func requireJSON(ctx *gin.Context) error {
	contentType := ctx.ContentType()
	if contentType == "" || contentType == "application/json" {
		return nil
	}
	return apperrors.UnsupportedMediaType("content type must be application/json", nil)
}

func readBody(ctx *gin.Context) ([]byte, error) {
	ctx.Request.Body = nethttp.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxBodyBytes)

	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		return nil, apperrors.BadRequest("the request body could not be read", err)
	}

	return body, nil
}

func respondError(ctx *gin.Context, err error) {
	appErr := apperrors.From(err)
	ctx.AbortWithStatusJSON(appErr.Code, appErr)
}
