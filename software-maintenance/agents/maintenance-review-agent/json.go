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

func decodeReviewRequest(request *http.Request, maximumBytes int64, destination any) error {
	if request.Body == nil {
		return errors.New("request body is required")
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, maximumBytes+1))
	if err != nil || int64(len(data)) > maximumBytes {
		return errors.New("request body is invalid or too large")
	}
	return decodeReviewStrictBytes(data, destination)
}

func decodeReviewStrictBytes(data []byte, destination any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("JSON value is required")
	}
	if err := rejectReviewDuplicateFields(data); err != nil {
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

func decodeReviewPartialBytes(data []byte, destination any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("JSON value is required")
	}
	if err := rejectReviewDuplicateFields(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("exactly one JSON value is required")
	}
	return nil
}

func rejectReviewDuplicateFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeReviewJSONValue(decoder); err != nil {
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

func consumeReviewJSONValue(decoder *json.Decoder) error {
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
			if err := consumeReviewJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeReviewJSONValue(decoder); err != nil {
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

func writeReviewSuccess(writer http.ResponseWriter, status int, data any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(core.ToolResponse{Success: true, Data: data})
}

func writeReviewError(writer http.ResponseWriter, status int, code, message string, category core.ErrorCategory, retryable bool, details ...map[string]string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	var fields map[string]string
	if len(details) > 0 {
		fields = details[0]
	}
	_ = json.NewEncoder(writer).Encode(core.ToolResponse{Success: false, Error: &core.ToolError{Code: code, Message: message, Category: category, Retryable: retryable, Details: fields}})
}
