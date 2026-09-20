package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/httperr"
)

// bindJSON decodes and validates a request body, writing a per-field error
// response and returning false when it fails.
//
// Replaces the previous `WriteValidation(c, "body", err.Error())`, which leaked
// the validator's internal text straight to the client:
//
//	Key: 'loginRequest.Email' Error:Field validation for 'Email' failed on the
//	'email' tag
//
// That names a Go struct, a package-internal tag, and nothing a user can act
// on. Worse, filing everything under "body" meant a form could never attach a
// message to the input that caused it - a password-too-short error appeared as
// a form-level generic rather than under the password field.
//
// Pass a pointer to the request struct.
func bindJSON(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		httperr.WriteError(c, translateBindError(err, req))
		return false
	}
	return true
}

// bindOptionalJSON is bindJSON for endpoints where the body may be omitted
// entirely (POST /chats with no options). An absent body leaves req at its
// zero value and succeeds; a present but malformed body still fails.
func bindOptionalJSON(c *gin.Context, req any) bool {
	if c.Request == nil || c.Request.ContentLength == 0 {
		return true
	}
	return bindJSON(c, req)
}

// translateBindError turns a binder failure into field-keyed, human-readable
// messages. Keys are JSON field names, so a client can map them straight onto
// its inputs.
func translateBindError(err error, req any) error {
	var vErrs validator.ValidationErrors
	if errors.As(err, &vErrs) {
		names := jsonFieldNames(req)
		fields := make(map[string]string, len(vErrs))
		for _, fe := range vErrs {
			key := fe.Field()
			if jsonName, ok := names[key]; ok {
				key = jsonName
			}
			fields[key] = describeRule(fe)
		}
		return &domain.ValidationError{Fields: fields}
	}

	// A type mismatch already knows which JSON field it was reading.
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		return &domain.ValidationError{Fields: map[string]string{
			field: fmt.Sprintf("must be %s", friendlyKind(typeErr.Type.Kind())),
		}}
	}

	// Anything below here has no field to attach to, so "body" is honest
	// rather than lazy - and a client needs a bucket for it either way.
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return domain.NewValidationError("body", "a JSON body is required")
	default:
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return domain.NewValidationError("body",
				fmt.Sprintf("is not valid JSON (at character %d)", syntaxErr.Offset))
		}
		return domain.NewValidationError("body", "could not be read as JSON")
	}
}

// describeRule renders one failed constraint as a sentence fragment that reads
// correctly after the field name: "password must be at least 10 characters".
func describeRule(fe validator.FieldError) string {
	isText := fe.Kind() == reflect.String

	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "min":
		if isText {
			return "must be at least " + fe.Param() + " characters"
		}
		return "must be at least " + fe.Param()
	case "max":
		if isText {
			return "must be at most " + fe.Param() + " characters"
		}
		return "must be at most " + fe.Param()
	case "len":
		if isText {
			return "must be exactly " + fe.Param() + " characters"
		}
		return "must have exactly " + fe.Param() + " items"
	case "gt":
		return "must be greater than " + fe.Param()
	case "gte":
		return "must be " + fe.Param() + " or more"
	case "lt":
		return "must be less than " + fe.Param()
	case "lte":
		return "must be " + fe.Param() + " or less"
	case "oneof":
		return "must be one of: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	case "uuid", "uuid4":
		return "must be a UUID"
	case "url", "uri":
		return "must be a valid URL"
	default:
		// Deliberately vague rather than exposing the tag name. A new
		// constraint without a case here degrades to something harmless
		// instead of leaking implementation detail.
		return "is not valid"
	}
}

// jsonFieldNames maps Go field names to their JSON names.
//
// The validator reports the struct field ("Email"); a client knows the wire
// name ("email"). Gin can be told this globally via RegisterTagNameFunc, but
// doing it here keeps the behavior with the code that depends on it rather
// than in router setup where it is easy to drop.
func jsonFieldNames(req any) map[string]string {
	t := reflect.TypeOf(req)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}

	out := make(map[string]string, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			out[f.Name] = name
		}
	}
	return out
}

func friendlyKind(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "text"
	case reflect.Bool:
		return "true or false"
	case reflect.Float32, reflect.Float64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return "a different type"
	}
}
