package model_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	paymentEntity "numeral-payments/internal/entity/payment"
	"numeral-payments/internal/model"
)

func TestCreatePaymentRequestDecodesTheProvidedSample(t *testing.T) {
	sample, err := os.ReadFile("../../resources/request_sample.json")
	if err != nil {
		t.Fatalf("read provided sample: %v", err)
	}

	var request model.CreatePaymentRequest
	if err := json.Unmarshal(sample, &request); err != nil {
		t.Fatalf("decode provided sample: %v", err)
	}

	want := model.CreatePaymentRequest{
		DebtorIBAN:     "FR1112739000504482744411A64",
		DebtorName:     "company1",
		CreditorIBAN:   "DE65500105179799248552",
		CreditorName:   "beneficiary",
		Amount:         json.Number("42.99"),
		IdempotencyKey: "JXJ984XXXZ",
	}
	if request != want {
		t.Errorf("the json tags must match the provided sample\ngot:  %+v\nwant: %+v", request, want)
	}
}

func TestToEntityInputMapsEveryField(t *testing.T) {
	request := model.CreatePaymentRequest{
		DebtorIBAN:     "FR1112739000504482744411A64",
		DebtorName:     "company1",
		CreditorIBAN:   "DE65500105179799248552",
		CreditorName:   "beneficiary",
		Amount:         json.Number("42.99"),
		IdempotencyKey: "JXJ984XXXZ",
	}

	got, err := request.ToEntityInput()
	if err != nil {
		t.Fatalf("map to entity input: %v", err)
	}

	want := paymentEntity.Input{
		IdempotencyKey: "JXJ984XXXZ",
		DebtorIBAN:     "FR1112739000504482744411A64",
		DebtorName:     "company1",
		CreditorIBAN:   "DE65500105179799248552",
		CreditorName:   "beneficiary",
		AmountCents:    4299,
	}
	if got != want {
		t.Errorf("unexpected entity input\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestToEntityInputRejectsAnAmountItCannotStoreExactly(t *testing.T) {
	request := model.CreatePaymentRequest{
		DebtorIBAN:     "FR1112739000504482744411A64",
		DebtorName:     "company1",
		CreditorIBAN:   "DE65500105179799248552",
		CreditorName:   "beneficiary",
		Amount:         json.Number("42.999"),
		IdempotencyKey: "JXJ984XXXZ",
	}

	if _, err := request.ToEntityInput(); !errors.Is(err, paymentEntity.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestNewPaymentResponseRendersTheEntity(t *testing.T) {
	p := &paymentEntity.Payment{
		IdempotencyKey: "JXJ984XXXZ",
		AmountCents:    4299,
		Currency:       "EUR",
		Status:         paymentEntity.StatusPending,
		CreatedAt:      time.Date(2026, 8, 25, 9, 30, 47, 0, time.UTC),
	}

	got := model.NewPaymentResponse(p)

	want := model.PaymentResponse{
		IdempotencyKey: "JXJ984XXXZ",
		Status:         "PENDING",
		Amount:         "42.99",
		Currency:       "EUR",
		CreatedAt:      "2026-08-25T09:30:47Z",
	}
	if got != want {
		t.Errorf("unexpected response\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestPaymentResponseJSONShape(t *testing.T) {
	response := model.PaymentResponse{
		IdempotencyKey: "JXJ984XXXZ",
		Status:         "PENDING",
		Amount:         "42.99",
		Currency:       "EUR",
		CreatedAt:      "2026-08-25T09:30:47Z",
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}

	const want = `{"idempotency_unique_key":"JXJ984XXXZ","status":"PENDING","amount":"42.99","currency":"EUR","created_at":"2026-08-25T09:30:47Z"}`
	if string(encoded) != want {
		t.Errorf("unexpected json\ngot:  %s\nwant: %s", encoded, want)
	}
}
