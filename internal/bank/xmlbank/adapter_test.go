package xmlbank_test

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"numeral-payments/internal/bank/xmlbank"
	paymentEntity "numeral-payments/internal/entity/payment"
)

func TestDepositWritesThePaymentFile(t *testing.T) {
	folder := t.TempDir()
	adapter := xmlbank.NewAdapter(folder)
	p := samplePayment()

	if err := adapter.Deposit(context.Background(), p); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	path := filepath.Join(folder, "payment_7.xml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read deposited payment: %v", err)
	}

	content := string(data)
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`xmlns="urn:iso:std:iso:20022:tech:xsd:pain.008.002.02"`,
		`xsi:schemaLocation="urn:iso:std:iso:20022:tech:xsd:pain.008.002.02 pain.008.002.02.xsd"`,
		"<MsgId>JXJ984XXXZ</MsgId>",
		"<Nm>beneficiary</Nm>",
		"<IBAN>DE65500105179799248552</IBAN>",
		"<Nm>company1</Nm>",
		"<IBAN>FR1112739000504482744411A64</IBAN>",
		`<Amt Ccy="EUR">42.99</Amt>`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("payment file must contain %s\ngot:\n%s", want, content)
		}
	}

	if !strings.Contains(content, "<Dbtr>\n    <Nm>company1</Nm>\n    <CdtrAcct>") {
		t.Errorf("the debtor account element must be CdtrAcct, as the provided xsd defines it\ngot:\n%s", content)
	}
}

func TestDepositedPaymentRoundTrips(t *testing.T) {
	folder := t.TempDir()
	adapter := xmlbank.NewAdapter(folder)

	if err := adapter.Deposit(context.Background(), samplePayment()); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(folder, "payment_7.xml"))
	if err != nil {
		t.Fatalf("read deposited payment: %v", err)
	}

	var document struct {
		MessageID    string `xml:"GrpHdr>MsgId"`
		CreditorName string `xml:"Cdtr>Nm"`
		CreditorIBAN string `xml:"Cdtr>CdtrAcct>Id>IBAN"`
		DebtorName   string `xml:"Dbtr>Nm"`
		DebtorIBAN   string `xml:"Dbtr>CdtrAcct>Id>IBAN"`
		Amount       struct {
			Value    string `xml:",chardata"`
			Currency string `xml:"Ccy,attr"`
		} `xml:"Amt"`
	}
	if err := xml.Unmarshal(data, &document); err != nil {
		t.Fatalf("unmarshal deposited payment: %v", err)
	}

	if document.MessageID != "JXJ984XXXZ" {
		t.Errorf("MsgId must be the idempotency key, got %q", document.MessageID)
	}
	if document.CreditorIBAN != "DE65500105179799248552" || document.DebtorIBAN != "FR1112739000504482744411A64" {
		t.Errorf("unexpected ibans: creditor %q debtor %q", document.CreditorIBAN, document.DebtorIBAN)
	}
	if document.CreditorName != "beneficiary" || document.DebtorName != "company1" {
		t.Errorf("unexpected names: creditor %q debtor %q", document.CreditorName, document.DebtorName)
	}
	if document.Amount.Value != "42.99" {
		t.Errorf("amount must render as 42.99, got %q", document.Amount.Value)
	}
	if document.Amount.Currency != "EUR" {
		t.Errorf("currency must be EUR, got %q", document.Amount.Currency)
	}
}

// A payment key is client supplied and only length constrained, so it must not
// be able to steer the path the payment file is written to.
func TestDepositKeepsHostilePaymentKeysInsideTheBankFolder(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "bank")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("create bank folder: %v", err)
	}

	p := samplePayment()
	p.IdempotencyKey = "../../../a"

	if err := xmlbank.NewAdapter(folder).Deposit(context.Background(), p); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	inside, err := filepath.Glob(filepath.Join(folder, "*.xml"))
	if err != nil || len(inside) != 1 {
		t.Fatalf("expected exactly 1 payment file inside the bank folder, got %v (%v)", inside, err)
	}

	outside, err := filepath.Glob(filepath.Join(root, "*.xml"))
	if err != nil || len(outside) != 0 {
		t.Fatalf("no file may be written outside the bank folder, got %v (%v)", outside, err)
	}
	if _, err := os.Stat(filepath.Join(folder, "a.xml")); !os.IsNotExist(err) {
		t.Error("the key must not become the file name")
	}
}

func TestParseResponseReadsTheProvidedFormat(t *testing.T) {
	adapter := xmlbank.NewAdapter(t.TempDir())

	data, err := os.ReadFile("../../../resources/bank_response.csv")
	if err != nil {
		t.Fatalf("read provided bank response: %v", err)
	}

	responses, err := adapter.ParseResponse(data)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d: %+v", len(responses), responses)
	}
	if responses[0].IdempotencyKey != "JXJ984XXXZ" {
		t.Errorf("expected the id to be trimmed, got %q", responses[0].IdempotencyKey)
	}
	if responses[0].Status != paymentEntity.StatusProcessed {
		t.Errorf("expected PROCESSED, got %q", responses[0].Status)
	}
}

func TestParseResponseHandlesMultipleRowsAndRejectsBrokenFiles(t *testing.T) {
	adapter := xmlbank.NewAdapter(t.TempDir())

	responses, err := adapter.ParseResponse([]byte("ID, STATUS\nAAAAAAAAAA, PROCESSED\nBBBBBBBBBB, rejected\nCCCCCCCCCC, WHAT\n"))
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if len(responses) != 3 {
		t.Fatalf("every data row must be returned for the service to judge, got %+v", responses)
	}
	if responses[0].IdempotencyKey != "AAAAAAAAAA" || responses[2].Status != "WHAT" {
		t.Errorf("rows must be passed through untouched, got %+v", responses)
	}

	if _, err := adapter.ParseResponse([]byte("ID, STATUS\nA,B,C,D\n")); err == nil {
		t.Fatal("a structurally broken file must be an error")
	}
}

func samplePayment() *paymentEntity.Payment {
	return &paymentEntity.Payment{
		ID:             7,
		IdempotencyKey: "JXJ984XXXZ",
		DebtorIBAN:     "FR1112739000504482744411A64",
		DebtorName:     "company1",
		CreditorIBAN:   "DE65500105179799248552",
		CreditorName:   "beneficiary",
		AmountCents:    4299,
		Currency:       "EUR",
		Status:         paymentEntity.StatusPending,
		CreatedAt:      time.Date(2026, 8, 25, 9, 30, 47, 0, time.UTC),
	}
}
