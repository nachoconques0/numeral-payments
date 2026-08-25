// Package validator validates raw request bodies against the JSON schema
// supplied with the exercise.
package validator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"numeral-payments/resources"
)

const schemaURL = "request_schema.json"

// Validator validates payment requests. It is safe for concurrent use.
type Validator struct {
	schema *jsonschema.Schema
}

// New compiles the embedded payment request schema. The schema declares
// draft-06, which the compiler detects from its $schema keyword.
func New() (*Validator, error) {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaURL, bytes.NewReader(resources.RequestSchema)); err != nil {
		return nil, fmt.Errorf("add payment request schema: %w", err)
	}

	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("compile payment request schema: %w", err)
	}

	return &Validator{schema: schema}, nil
}

// Validate reports every violation in body, or nil when it is valid. The
// messages are safe to return so a rejection is diagnosable.
func (v *Validator) Validate(body []byte) []string {
	var document any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return []string{fmt.Sprintf("body is not valid JSON: %v", err)}
	}

	err := v.schema.Validate(document)
	if err == nil {
		return nil
	}

	var validationErr *jsonschema.ValidationError
	if !isValidationError(err, &validationErr) {
		return []string{err.Error()}
	}

	violations := flatten(validationErr, nil)
	sort.Strings(violations)
	return violations
}

func isValidationError(err error, target **jsonschema.ValidationError) bool {
	validationErr, ok := err.(*jsonschema.ValidationError)
	if ok {
		*target = validationErr
	}
	return ok
}

// flatten collects the leaf causes, which are the messages that actually name
// the offending field.
func flatten(err *jsonschema.ValidationError, out []string) []string {
	if len(err.Causes) == 0 {
		location := err.InstanceLocation
		if location == "" {
			location = "/"
		}
		return append(out, fmt.Sprintf("%s: %s", location, err.Message))
	}

	for _, cause := range err.Causes {
		out = flatten(cause, out)
	}
	return out
}
