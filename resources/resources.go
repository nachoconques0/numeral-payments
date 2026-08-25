// Package resources exposes the files supplied with the exercise so they can be
// embedded in the binary instead of being read from disk at runtime.
package resources

import _ "embed"

// RequestSchema is the JSON schema the payment request is validated against.
// It is the exact file provided with the exercise, embedded verbatim.
//
//go:embed request_schema.json
var RequestSchema []byte
