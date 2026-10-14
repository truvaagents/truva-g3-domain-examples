package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/truvaagents/truva-g3/core"
)

func decodeSourceRequest(request *http.Request, maximumBytes int64, destination any) error {
	if request.Body == nil {
		return errors.New("request body is required")
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, maximumBytes+1))
	if err != nil || int64(len(data)) > maximumBytes {
		return errors.New("request body is invalid or too large")
	}
	return decodeSourceStrictBytes(data, destination)
}

func decodeSourceStrictBytes(data []byte, destination any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("JSON value is required")
	}
	if err := rejectSourceDuplicateFields(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("exactly one JSON value is required")
	}
	return nil
}

func rejectSourceDuplicateFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeSourceJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func consumeSourceJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key must be a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeSourceJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeSourceJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func writeSourceSuccess(writer http.ResponseWriter, data any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(core.ToolResponse{Success: true, Data: data})
}

func writeSourceError(writer http.ResponseWriter, status int, code, message string, category core.ErrorCategory, retryable bool) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(core.ToolResponse{Success: false, Error: &core.ToolError{
		Code: code, Message: message, Category: category, Retryable: retryable,
	}})
}
