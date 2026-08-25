package payment_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"numeral-payments/internal/config"
	paymentController "numeral-payments/internal/controller/payment"
	paymentEntity "numeral-payments/internal/entity/payment"
	apperrors "numeral-payments/internal/errors"
	httprouter "numeral-payments/internal/http"
	"numeral-payments/internal/validator"
)

const (
	username = "CALCAGNO"
	password = "xxxx"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestCreateRequiresCredentials(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		password string
		withAuth bool
	}{
		{name: "no credentials"},
		{name: "wrong password", user: username, password: "guess", withAuth: true},
		{name: "wrong user", user: "someone", password: password, withAuth: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, service := newRouter(t)

			request := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(sampleBody))
			request.Header.Set("Content-Type", "application/json")
			if test.withAuth {
				request.SetBasicAuth(test.user, test.password)
			}

			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", response.Code, response.Body)
			}
			if got := response.Header().Get("WWW-Authenticate"); got != `Basic realm="numeral"` {
				t.Errorf("unexpected WWW-Authenticate header: %q", got)
			}
			if service.calls != 0 {
				t.Error("an unauthenticated request must never reach the service")
			}
		})
	}
}

func TestCreateRejectsInvalidPayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: "{"},
		{name: "missing field", body: `{"debtor_name":"company1"}`},
		{name: "bad iban", body: strings.Replace(sampleBody, "FR1112739000504482744411A64", "nope", 1)},
		{name: "unknown property", body: strings.Replace(sampleBody, `{`, `{"surprise":1,`, 1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, service := newRouter(t)

			response := do(router, test.body, "application/json")

			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", response.Code, response.Body)
			}

			var appErr apperrors.AppError
			if err := json.Unmarshal(response.Body.Bytes(), &appErr); err != nil {
				t.Fatalf("error body must be an AppError: %v", err)
			}
			if appErr.Code != http.StatusBadRequest {
				t.Errorf("expected code 400 in the body, got %d", appErr.Code)
			}
			if service.calls != 0 {
				t.Error("an invalid request must never reach the service")
			}
		})
	}
}

func TestCreateRejectsUnsupportedMediaType(t *testing.T) {
	router, _ := newRouter(t)

	response := do(router, sampleBody, "text/xml")

	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d: %s", response.Code, response.Body)
	}
}

func TestCreateReturnsTheStoredPayment(t *testing.T) {
	router, service := newRouter(t)

	response := do(router, sampleBody, "application/json")

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}

	var body struct {
		IdempotencyKey string `json:"idempotency_unique_key"`
		Status         string `json:"status"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.IdempotencyKey != "JXJ984XXXZ" || body.Status != "PENDING" {
		t.Errorf("unexpected response body: %+v", body)
	}
	if body.Amount != "42.99" || body.Currency != "EUR" {
		t.Errorf("unexpected amount: %+v", body)
	}
	if service.received.AmountCents != 4299 {
		t.Errorf("the amount must reach the service as 4299 cents, got %d", service.received.AmountCents)
	}
}

func TestCreateMapsServiceFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "deposit failure", err: apperrors.InternalError("could not deposit", errors.New("boom")), want: http.StatusInternalServerError},
		{name: "reused idempotency key", err: apperrors.Conflict("idempotency key already used for a different payment", nil), want: http.StatusConflict},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, service := newRouter(t)
			service.err = test.err

			response := do(router, sampleBody, "application/json")

			if response.Code != test.want {
				t.Fatalf("expected %d, got %d: %s", test.want, response.Code, response.Body)
			}
		})
	}
}

func TestCreateRejectsAnAmountItCannotStoreExactly(t *testing.T) {
	router, service := newRouter(t)

	response := do(router, strings.Replace(sampleBody, "42.99", "42.999", 1), "application/json")

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body)
	}
	if service.calls != 0 {
		t.Error("an unstorable amount must never reach the service")
	}
}

const sampleBody = `{"debtor_iban":"FR1112739000504482744411A64","debtor_name":"company1","creditor_iban":"DE65500105179799248552","creditor_name":"beneficiary","ammount":42.99,"idempotency_unique_key":"JXJ984XXXZ"}`

func do(router http.Handler, body, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	request.SetBasicAuth(username, password)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func newRouter(t *testing.T) (http.Handler, *fakeService) {
	t.Helper()

	requestValidator, err := validator.New()
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	service := &fakeService{}
	controller := paymentController.NewController(service, requestValidator)

	return httprouter.NewRouter(config.Auth{Username: username, Password: password}, controller), service
}

type fakeService struct {
	calls    int
	received paymentEntity.Input
	err      error
}

func (s *fakeService) CreatePayment(_ context.Context, in paymentEntity.Input) (*paymentEntity.Payment, error) {
	s.calls++
	s.received = in

	if s.err != nil {
		return nil, s.err
	}

	return &paymentEntity.Payment{
		IdempotencyKey: in.IdempotencyKey,
		AmountCents:    in.AmountCents,
		Currency:       paymentEntity.DefaultCurrency,
		Status:         paymentEntity.StatusPending,
	}, nil
}
