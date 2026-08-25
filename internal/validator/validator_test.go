package validator_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"numeral-payments/internal/validator"
)

func TestValidateAcceptsTheProvidedSample(t *testing.T) {
	v := newValidator(t)

	sample, err := os.ReadFile("../../resources/request_sample.json")
	if err != nil {
		t.Fatalf("read provided sample: %v", err)
	}

	if violations := v.Validate(sample); len(violations) > 0 {
		t.Fatalf("provided sample must be valid, got violations: %v", violations)
	}
}

func TestValidateRejectsInvalidRequests(t *testing.T) {
	v := newValidator(t)

	tests := []struct {
		name        string
		mutate      func(map[string]any)
		wantMessage string
	}{
		{
			name:        "missing required field",
			mutate:      func(body map[string]any) { delete(body, "creditor_iban") },
			wantMessage: "creditor_iban",
		},
		{
			name:        "iban does not match the pattern",
			mutate:      func(body map[string]any) { body["debtor_iban"] = "not-an-iban" },
			wantMessage: "debtor_iban",
		},
		{
			name:        "idempotency key of the wrong length",
			mutate:      func(body map[string]any) { body["idempotency_unique_key"] = "TOOSHORT" },
			wantMessage: "idempotency_unique_key",
		},
		{
			name:        "unknown property",
			mutate:      func(body map[string]any) { body["surprise"] = true },
			wantMessage: "surprise",
		},
		{
			name:        "amount is not a number",
			mutate:      func(body map[string]any) { body["ammount"] = "42.99" },
			wantMessage: "ammount",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := validBody(t)
			test.mutate(body)

			violations := v.Validate(encode(t, body))
			if len(violations) == 0 {
				t.Fatal("expected the request to be rejected")
			}
			if !strings.Contains(strings.Join(violations, " "), test.wantMessage) {
				t.Fatalf("expected a violation mentioning %q, got %v", test.wantMessage, violations)
			}
		})
	}
}

func TestValidateRejectsMalformedJSON(t *testing.T) {
	v := newValidator(t)

	violations := v.Validate([]byte("{not json"))
	if len(violations) != 1 || !strings.Contains(violations[0], "not valid JSON") {
		t.Fatalf("expected a single malformed JSON violation, got %v", violations)
	}
}

func newValidator(t *testing.T) *validator.Validator {
	t.Helper()

	v, err := validator.New()
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return v
}

func validBody(t *testing.T) map[string]any {
	t.Helper()

	return map[string]any{
		"debtor_iban":            "FR1112739000504482744411A64",
		"debtor_name":            "company1",
		"creditor_iban":          "DE65500105179799248552",
		"creditor_name":          "beneficiary",
		"ammount":                42.99,
		"idempotency_unique_key": "JXJ984XXXZ",
	}
}

func encode(t *testing.T, body map[string]any) []byte {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return data
}
