// Package intakehttp owns the existing HTTP creation boundary. It supplies only
// authenticated identity and wire mapping; application authorization owns replay.
package intakehttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"itsm-backend/common"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func Fail(c *gin.Context, err error) {
	var typed *creation.IntakeError
	if !errors.As(err, &typed) {
		typed = creation.NewInternalFailure("creation failed", err)
	}
	code := common.InternalErrorCode
	switch typed.HTTPStatus {
	case 400:
		code = common.ParamErrorCode
	case 401:
		code = common.AuthFailedCode
	case 403:
		code = common.ForbiddenCode
	case 404:
		code = common.NotFoundCode
	case 409:
		code = common.ConflictCode
	case 503:
		code = common.ServiceUnavailableCode
	}
	common.FailWithData(c, code, typed.Message, gin.H{"errorCode": typed.Code, "retryable": typed.Retryable, "fieldErrors": typed.FieldErrors})
}
func Invalid(field, message string) error {
	return creation.NewInvalidCommand(message, creation.FieldError{Field: field, Message: message}, nil)
}

func Execute(c *gin.Context, app creation.Application, tenantID, requesterID int, command creation.CreateWorkItemCommand) {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		Fail(c, Invalid("Idempotency-Key", "Idempotency-Key header is required"))
		return
	}
	actorID := c.GetInt("user_id")
	if requesterID == 0 {
		requesterID = actorID
	}
	identity := creation.Identity{TenantID: tenantID, ActorID: actorID, RequesterID: requesterID, Role: c.GetString("role"), Channel: "http"}
	if app == nil {
		Fail(c, creation.NewInternalFailure("creation application is unavailable", nil))
		return
	}
	command.IdempotencyKey = key
	command.Confirmation = "confirmed"
	result, err := app.Create(c.Request.Context(), identity, command)
	if err != nil {
		Fail(c, err)
		return
	}
	if result == nil {
		Fail(c, creation.NewInternalFailure("creation returned no result", nil))
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	c.JSON(status, common.Response{Code: common.SuccessCode, Message: "success", Data: result})
}

// Bind rejects ambiguous/unknown members without changing global Gin decoding.
// Dynamic values remain json.Number all the way into the canonical command.
func Bind(c *gin.Context, target any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err == nil {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var value any
		value, err = readValue(d)
		if err == nil {
			if _, next := d.Token(); next != io.EOF {
				err = fmt.Errorf("exactly one JSON object is required")
			}
		}
		if err == nil {
			if _, ok := value.(map[string]any); !ok {
				err = fmt.Errorf("JSON object is required")
			}
		}
		if err == nil {
			err = checkNames(value, reflect.TypeOf(target))
		}
		if err == nil {
			c.Set("intake.http.body", value)
		}
	}
	if err == nil {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		d.DisallowUnknownFields()
		err = d.Decode(target)
	}
	if err == nil {
		err = binding.Validator.ValidateStruct(target)
	}
	if err != nil {
		Fail(c, Invalid("body", "invalid creation body: "+err.Error()))
		return false
	}
	return true
}
func readValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid member")
			}
			if _, exists := object[name]; exists {
				return nil, fmt.Errorf("duplicate member %q", name)
			}
			value, err := readValue(d)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err = d.Token()
		return object, err
	case '[':
		values := []any{}
		for d.More() {
			value, err := readValue(d)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err = d.Token()
		return values, err
	default:
		return nil, fmt.Errorf("unexpected delimiter")
	}
}
func checkNames(value any, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if value == nil {
		return nil
	}
	if reflect.PointerTo(typ).Implements(reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()) {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		fields := map[string]reflect.Type{}
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		for key, nested := range object {
			field, ok := fields[key]
			if !ok {
				return fmt.Errorf("unknown member %q", key)
			}
			if err := checkNames(nested, field); err != nil {
				return err
			}
		}
	case reflect.Map:
		if object, ok := value.(map[string]any); ok {
			for _, nested := range object {
				if err := checkNames(nested, typ.Elem()); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Array:
		if values, ok := value.([]any); ok {
			for _, nested := range values {
				if err := checkNames(nested, typ.Elem()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func FieldPresent(c *gin.Context, name string) bool {
	value, ok := c.Get("intake.http.body")
	if !ok {
		return false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	_, ok = object[name]
	return ok
}
