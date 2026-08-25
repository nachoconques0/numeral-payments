// Package xmlbank is the adapter for the bank in the exercise: pain.008.002.02
// XML payments out, CSV responses back.
package xmlbank

import (
	"context"
	"encoding/xml"
	"fmt"

	"numeral-payments/internal/bank"
	paymentEntity "numeral-payments/internal/entity/payment"
)

// Namespace declarations reproduced from the provided payment_sample.xml.
const (
	documentNamespace = "urn:iso:std:iso:20022:tech:xsd:pain.008.002.02"
	xsiNamespace      = "http://www.w3.org/2001/XMLSchema-instance"
	schemaLocation    = "urn:iso:std:iso:20022:tech:xsd:pain.008.002.02 pain.008.002.02.xsd"

	// creationTimeLayout matches the CreDtTm format in the provided sample.
	creationTimeLayout = "2006-01-02T15:04:05.000Z"
)

// Adapter deposits payments as XML into the bank folder.
type Adapter struct {
	folder string
}

// NewAdapter returns an adapter writing to and reading from folder.
func NewAdapter(folder string) *Adapter {
	return &Adapter{folder: folder}
}

// Name identifies the adapter in logs and configuration.
func (a *Adapter) Name() string { return "xml" }

// ResponsePattern is the glob of the files this bank answers with. We deposit
// .xml and the bank answers .csv, so the two can never be confused.
func (a *Adapter) ResponsePattern() string { return "*.csv" }

// Deposit writes the payment as a pain.008.002.02 file into the bank folder.
func (a *Adapter) Deposit(ctx context.Context, p *paymentEntity.Payment) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	payload, err := Marshal(p)
	if err != nil {
		return err
	}

	if _, err := bank.WriteFileAtomic(a.folder, fileName(p.ID), payload); err != nil {
		return fmt.Errorf("deposit payment %s: %w", p.IdempotencyKey, err)
	}

	return nil
}

// ParseResponse reads a bank response file.
func (a *Adapter) ParseResponse(data []byte) ([]bank.Response, error) {
	return bank.ParseResponseCSV(data)
}

// fileName uses the internal row id, never the client supplied key, so a key
// cannot steer the path.
func fileName(id int64) string {
	return fmt.Sprintf("payment_%d.xml", id)
}

// document mirrors the provided payment.xsd. The namespaces are plain string
// fields because Go's encoder will not reproduce the sample's xmlns layout.
type document struct {
	XMLName        xml.Name `xml:"Document"`
	Xmlns          string   `xml:"xmlns,attr"`
	XmlnsXSI       string   `xml:"xmlns:xsi,attr"`
	SchemaLocation string   `xml:"xsi:schemaLocation,attr"`
	GroupHeader    groupHeader
	Creditor       party  `xml:"Cdtr"`
	Debtor         party  `xml:"Dbtr"`
	Amount         amount `xml:"Amt"`
}

type groupHeader struct {
	XMLName xml.Name `xml:"GrpHdr"`
	// MsgId carries the idempotency key: it is the identifier the bank echoes
	// back in its response file, so it is what correlates the two.
	MessageID    string `xml:"MsgId"`
	CreationTime string `xml:"CreDtTm"`
}

// party serves both Cdtr and Dbtr. The debtor's account is CdtrAcct because the
// provided xsd defines it that way; ISO 20022 would call it DbtrAcct.
type party struct {
	Name    string  `xml:"Nm"`
	Account account `xml:"CdtrAcct"`
}

type account struct {
	ID accountID `xml:"Id"`
}

type accountID struct {
	IBAN string `xml:"IBAN"`
}

type amount struct {
	Currency string `xml:"Ccy,attr"`
	Value    string `xml:",chardata"`
}

// Marshal renders a payment as a pain.008.002.02 document.
func Marshal(p *paymentEntity.Payment) ([]byte, error) {
	doc := document{
		Xmlns:          documentNamespace,
		XmlnsXSI:       xsiNamespace,
		SchemaLocation: schemaLocation,
		GroupHeader: groupHeader{
			MessageID:    p.IdempotencyKey,
			CreationTime: p.CreatedAt.UTC().Format(creationTimeLayout),
		},
		Creditor: party{
			Name:    p.CreditorName,
			Account: account{ID: accountID{IBAN: p.CreditorIBAN}},
		},
		Debtor: party{
			Name:    p.DebtorName,
			Account: account{ID: accountID{IBAN: p.DebtorIBAN}},
		},
		Amount: amount{Currency: p.Currency, Value: p.FormattedAmount()},
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal payment xml: %w", err)
	}

	return append([]byte(xml.Header), append(body, '\n')...), nil
}
