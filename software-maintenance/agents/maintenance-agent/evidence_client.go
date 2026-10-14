package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

const maximumEvidenceResponseBytes = 1 << 20

var evidenceErrorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)

func (agent *maintenanceAgent) callEvidenceCapability(ctx context.Context, capability string, payload any, destination any) error {
	return agent.callOwnedCapability(ctx, "maintenance-evidence-tool", capability, payload, destination)
}

// Deterministic evidence/bookkeeping calls discover their current endpoint.
// Issuer validation here does not restrict the autonomous planner's catalog.
func (agent *maintenanceAgent) callOwnedCapability(ctx context.Context, issuer, capability string, payload any, destination any) error {
	if agent.Discovery == nil {
		return errors.New("evidence discovery is not ready")
	}
	services, err := agent.Discovery.FindByCapability(ctx, capability)
	if err != nil {
		return fmt.Errorf("discover evidence capability: %w", err)
	}
	// This application boundary trusts the nominated passive issuer. It does
	// not filter orchestration discovery or choose investigation steps for the planner.
	services = filterOwnedServices(services, issuer, capability)
	if len(services) == 0 {
		return fmt.Errorf("evidence capability %s is unavailable", capability)
	}
	service := services[0]
	endpoint := ""
	for _, candidate := range service.Capabilities {
		if candidate.Name == capability {
			endpoint = candidate.Endpoint
			break
		}
	}
	if endpoint == "" || !strings.HasPrefix(endpoint, "/") || strings.ContainsAny(endpoint, "?#") ||
		strings.TrimSpace(service.Address) == "" || strings.ContainsAny(service.Address, "/\\?#@ \t\r\n") ||
		service.Port < 1 || service.Port > 65535 {
		return errors.New("discovered evidence endpoint is invalid")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("encode evidence request")
	}
	url := "http://" + net.JoinHostPort(service.Address, strconv.Itoa(service.Port)) + endpoint
	// #nosec G107 -- address and endpoint come from the authenticated framework discovery registry.
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return errors.New("create evidence request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+agent.serviceToken)
	request.Header.Set("X-TruvaG3-Agent-Name", maintenanceAgentName)
	if requestID := telemetry.GetBaggage(ctx)["request_id"]; requestID != "" {
		request.Header.Set("X-TruvaG3-Request-ID", requestID)
	}
	response, err := agent.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call evidence capability: %w", core.RedactSensitiveError(err))
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumEvidenceResponseBytes+1))
	if err != nil {
		return errors.New("read evidence response")
	}
	if len(data) > maximumEvidenceResponseBytes {
		return errors.New("evidence response exceeded the configured limit")
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *core.ToolError `json:"error"`
	}
	if err := decodeCoordinatorBytes(data, &envelope); err != nil {
		return errors.New("evidence response envelope is invalid")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success || len(envelope.Data) == 0 {
		code := "UNSUCCESSFUL_RESPONSE"
		if envelope.Error != nil && evidenceErrorCode.MatchString(envelope.Error.Code) {
			code = envelope.Error.Code
		}
		// Preserve bounded structured cause, not arbitrary upstream body text.
		return fmt.Errorf("evidence capability %s returned HTTP %d (%s)", capability, response.StatusCode, code)
	}
	if err := decodeCoordinatorPartialBytes(envelope.Data, destination); err != nil {
		return errors.New("evidence response data is invalid")
	}
	return nil
}

func filterOwnedServices(services []*core.ServiceInfo, issuer, capability string) []*core.ServiceInfo {
	result := make([]*core.ServiceInfo, 0, len(services))
	for _, service := range services {
		if service == nil || service.Name != issuer || service.Type != core.ComponentTypeTool {
			continue
		}
		found := false
		for _, candidate := range service.Capabilities {
			// Only the named post-completion writer is an internal application
			// call. This does not expose internal capabilities to the planner or
			// bypass the tool's credential/profile/completed-origin checks.
			internalWriter := issuer == "maintenance-assessment-tool" && capability == "record_assessment"
			if candidate.Name == capability && (!candidate.Internal || internalWriter) {
				found = true
				break
			}
		}
		if found {
			result = append(result, service)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		leftHealthy := result[i].Health == core.HealthHealthy
		rightHealthy := result[j].Health == core.HealthHealthy
		if leftHealthy != rightHealthy {
			return leftHealthy
		}
		return result[i].ID < result[j].ID
	})
	return result
}
