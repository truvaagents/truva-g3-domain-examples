// automatic-pilot produces ordinary application traffic and retains read-only
// acceptance evidence. It does not submit maintenance tasks or call an AI API.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	mode := flag.String("mode", "state", "state, order, case, task, lookup, assessment, execution, or debug")
	id := flag.String("id", "", "case/task/observation/assessment identity")
	quantity := flag.Int("quantity", 1, "ordinary checkout quantity")
	promotion := flag.String("promotion", "", "ordinary checkout promotion")
	directory := flag.String("output", "", "required credential-free evidence directory")
	flag.Parse()
	if *directory == "" {
		fail("-output is required")
	}
	if err := os.MkdirAll(*directory, 0700); err != nil {
		fail(err.Error())
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	client := &http.Client{Timeout: 30 * time.Second}
	call := func(name, method, url, token string, payload any) {
		var body []byte
		if payload != nil {
			var err error
			body, err = json.Marshal(payload)
			if err != nil {
				fail(err.Error())
			}
		}
		request, err := http.NewRequest(method, url, bytes.NewReader(body))
		if err != nil {
			fail(err.Error())
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-TruvaG3-Request-ID", "automatic-pilot-"+stamp)
		// #nosec G704 -- URLs are fixed local ingress endpoints selected below, not remote input.
		response, err := client.Do(request)
		if err != nil {
			fail("request failed; no maintenance task was submitted by this diagnostic")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(data) > 8<<20 {
			fail("evidence response unavailable or over byte limit")
		}
		var content json.RawMessage
		if json.Valid(data) {
			content = data
		} else {
			content, err = json.Marshal(string(data))
			if err != nil {
				fail(err.Error())
			}
		}
		observation := response.Header.Get("X-Request-ID")
		record := struct {
			Status        int             `json:"http_status"`
			At            string          `json:"captured_at"`
			ObservationID string          `json:"observation_id,omitempty"`
			Body          json.RawMessage `json:"body"`
		}{response.StatusCode, stamp, observation, content}
		encoded, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			fail(err.Error())
		}
		filename := filepath.Join(*directory, name+"-"+stamp+".json")
		// #nosec G703 -- operator-selected evidence directory, not a remotely controlled path.
		if err := os.WriteFile(filename, encoded, 0600); err != nil {
			fail(err.Error())
		}
		fmt.Printf("%s status=%d observation=%s evidence=%s\n", name, response.StatusCode, observation, filename)
		if *mode != "order" && *mode != "debug" && *mode != "execution" {
			fmt.Println(string(content))
		}
	}
	operator := os.Getenv("MAINTENANCE_AGENT_API_TOKEN")
	coordinator := "http://maintenance-agent.localhost"
	switch *mode {
	case "state":
		call("health", "GET", coordinator+"/maintenance/health", "", nil)
		call("admission", "GET", coordinator+"/api/v1/admission", operator, nil)
	case "order":
		token := os.Getenv("ORDER_API_TOKEN")
		if token == "" {
			fail("ORDER_API_TOKEN is required")
		}
		call("order", "POST", "http://retail-order-service.localhost/orders", token, map[string]any{"order_id": "auto-" + stamp, "sku": "COFFEE-250", "quantity": *quantity, "promotion_code": *promotion})
	case "case", "task", "assessment", "execution", "debug":
		if *id == "" || filepath.Base(*id) != *id {
			fail("a single bounded path identity is required")
		}
		url := coordinator + "/api/v1/cases/" + *id
		if *mode == "task" {
			url = coordinator + "/api/v1/tasks/" + *id
		}
		if *mode == "execution" {
			url = "http://registry.localhost/api/executions/" + *id
		}
		if *mode == "debug" {
			url = "http://registry.localhost/api/llm-debug/" + *id
		}
		if *mode == "assessment" {
			call("assessment", "POST", "http://maintenance-assessment-tool.localhost/maintenance/assessments/get", operator, map[string]string{"profile_id": "retail-order-maintenance", "assessment_id": *id})
		} else {
			if *mode == "execution" || *mode == "debug" {
				call(*mode, "GET", url, "", nil)
			} else {
				call(*mode, "GET", url, operator, nil)
			}
		}
	case "lookup":
		if *id == "" {
			fail("an observation identity is required")
		}
		call("lookup", "POST", "http://maintenance-assessment-tool.localhost/maintenance/assessments/find", operator, map[string]string{"profile_id": "retail-order-maintenance", "observation_id": *id, "check_id": "checkout-outcome"})
	default:
		fail("unsupported mode")
	}
}

func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
