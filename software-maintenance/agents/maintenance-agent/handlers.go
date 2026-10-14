package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

func (agent *maintenanceAgent) registerHTTPHandlers(taskAPI *orchestration.TaskAPIHandler) error {
	if taskAPI == nil {
		return errors.New("task API handler is required")
	}
	if err := agent.HandleFunc("/api/v1/tasks", agent.authorizeHTTP(agent.validateTaskSubmit(taskAPI))); err != nil {
		return err
	}
	if err := agent.HandleFunc("/api/v1/tasks/", agent.authorizeHTTP(func(writer http.ResponseWriter, request *http.Request) {
		path := strings.TrimPrefix(request.URL.Path, "/api/v1/tasks/")
		taskID := strings.TrimSuffix(path, "/cancel")
		if !coordinatorCaseIDPattern.MatchString(taskID) {
			writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_TASK_ID", "Task identity is invalid", core.CategoryInputError, false)
			return
		}
		task, err := agent.taskStore.Get(request.Context(), taskID)
		if errors.Is(err, core.ErrTaskNotFound) || (err == nil && task.Type != investigationTaskType) {
			writeCoordinatorError(writer, http.StatusNotFound, "TASK_NOT_FOUND", "Coordinator investigation task was not found", core.CategoryInputError, false)
			return
		}
		if err != nil {
			writeCoordinatorError(writer, http.StatusServiceUnavailable, "TASK_STORE_UNAVAILABLE", "Coordinator task could not be read", core.CategoryServiceError, true)
			return
		}
		if strings.HasSuffix(request.URL.Path, "/cancel") && request.Method == http.MethodPost {
			if task.Status != core.TaskStatusQueued {
				writeCoordinatorError(writer, http.StatusConflict, "CANCELLATION_NOT_SAFE", "Only a queued coordinator task can be cancelled; active work must finish within its bounded timeout", core.CategoryInputError, false)
				return
			}
			taskAPI.HandleCancel(writer, request)
			return
		}
		if request.Method == http.MethodGet {
			taskAPI.HandleGetTask(writer, request)
			return
		}
		writeCoordinatorError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET for task status or POST for task cancellation", core.CategoryInputError, false)
	})); err != nil {
		return err
	}
	if err := agent.HandleFunc("/api/v1/observations", agent.authorizeHTTP(agent.handleObservationEvent)); err != nil {
		return err
	}
	if err := agent.HandleFunc("/api/v1/cases/", agent.authorizeHTTP(agent.handleCaseGet)); err != nil {
		return err
	}
	if err := agent.HandleFunc("/api/v1/admission", agent.authorizeHTTP(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writeCoordinatorError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Admission inspection requires GET", core.CategoryInputError, false)
			return
		}
		if !agent.admission.Enabled {
			writeCoordinatorSuccess(writer, http.StatusOK, map[string]bool{"enabled": false})
			return
		}
		data, err := agent.cases.client.Get(request.Context(), agent.admissionBudgetKey()).Bytes()
		var budget admissionBudget
		if err != nil || decodeCoordinatorBytes(data, &budget) != nil {
			writeCoordinatorError(writer, http.StatusServiceUnavailable, "ADMISSION_STORE_UNAVAILABLE", "Admission run could not be read", core.CategoryServiceError, true)
			return
		}
		writeCoordinatorSuccess(writer, http.StatusOK, budget)
	})); err != nil {
		return err
	}
	return nil
}

func (agent *maintenanceAgent) authorizeHTTP(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		caller, ok := agent.auth.authenticate(request.Header.Get("Authorization"))
		if !ok {
			writeCoordinatorError(writer, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid coordinator API credential is required", core.CategoryAuthError, false)
			return
		}
		if !agent.profile.Allows(caller) {
			writeCoordinatorError(writer, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized for this coordinator profile", core.CategoryAuthError, false)
			return
		}
		next(writer, request)
	}
}

func (agent *maintenanceAgent) validateTaskSubmit(taskAPI *orchestration.TaskAPIHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writeCoordinatorError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Task submission requires POST", core.CategoryInputError, false)
			return
		}
		if agent.admission.Enabled {
			writeCoordinatorError(writer, http.StatusConflict, "BOUNDED_OBSERVATION_INGRESS_REQUIRED", "Use /api/v1/observations for bounded admission; direct task submission bypasses the run ledger", core.CategoryInputError, false)
			return
		}
		data, err := io.ReadAll(io.LimitReader(request.Body, agent.profile.Limits.MaximumRequestBytes+1))
		if err != nil || int64(len(data)) > agent.profile.Limits.MaximumRequestBytes {
			writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Task request exceeds the configured JSON limit", core.CategoryInputError, false)
			return
		}
		var submission orchestration.TaskSubmitRequest
		if err := decodeCoordinatorBytes(data, &submission); err != nil || submission.Type != investigationTaskType {
			writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_TASK", "Only maintenance.investigate tasks with the supported schema are accepted", core.CategoryInputError, false)
			return
		}
		input, err := agent.validateInvestigationTaskInput(submission.Input)
		if err != nil {
			writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_TASK_INPUT", "Investigation task input is outside the coordinator profile", core.CategoryInputError, false)
			return
		}
		if input.ProfileID != agent.profile.CoordinatorProfileID {
			writeCoordinatorError(writer, http.StatusForbidden, "PROFILE_FORBIDDEN", "Investigation profile is not authorized", core.CategoryAuthError, false)
			return
		}
		if submission.Timeout != "" {
			timeout, err := time.ParseDuration(submission.Timeout)
			if err != nil || timeout <= 0 || timeout > agent.limits.Task {
				writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_TIMEOUT", "Task timeout must be positive and no greater than "+agent.limits.Task.String(), core.CategoryInputError, false)
				return
			}
		}
		request.Body = io.NopCloser(bytes.NewReader(data))
		taskAPI.HandleSubmit(writer, request)
	}
}

func (agent *maintenanceAgent) handleObservationEvent(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeCoordinatorError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Observation admission requires POST", core.CategoryInputError, false)
		return
	}
	var event observationEvent
	if err := decodeCoordinatorRequest(request, agent.profile.Limits.MaximumRequestBytes, &event); err != nil {
		writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Observation event must be one bounded JSON object", core.CategoryInputError, false)
		return
	}
	if event.ProfileID != agent.profile.CoordinatorProfileID || !observationIDPattern.MatchString(event.ObservationID) {
		writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_OBSERVATION", "Observation profile or identity is invalid", core.CategoryInputError, false)
		return
	}
	input := investigationInput{
		ProfileID: agent.profile.CoordinatorProfileID,
		CaseID:    observationCaseID(agent.profile.ProfileSHA256, event.ObservationID),
		Objective: observationObjective(event.ObservationID), ObservationRefs: []string{event.ObservationID},
	}
	result, err := agent.admitInvestigation(request.Context(), input, "authenticated_event")
	if err != nil {
		var deferred *admissionDeferred
		if errors.As(err, &deferred) {
			writeCoordinatorError(writer, http.StatusTooManyRequests, "ADMISSION_DEFERRED", deferred.Error(), core.CategoryInputError, false)
			return
		}
		agent.Logger.WarnWithContext(request.Context(), "Authenticated observation admission failed", map[string]interface{}{
			"operation": "observation_event", "request_id": observation.RequestID(request), "error_type": "admission_failed", "case_id": input.CaseID, "error": core.RedactSensitiveError(err).Error(),
		})
		writeCoordinatorError(writer, http.StatusServiceUnavailable, "ADMISSION_FAILED", "Observation could not be durably admitted", core.CategoryServiceError, true)
		return
	}
	status := http.StatusAccepted
	if result.Reused {
		status = http.StatusOK
	}
	writeCoordinatorSuccess(writer, status, map[string]interface{}{
		"case_id": result.CaseID, "task_id": result.TaskID, "status": result.Status,
		"reused": result.Reused, "task_status_url": "/api/v1/tasks/" + result.TaskID,
		"case_status_url": "/api/v1/cases/" + result.CaseID,
	})
}

func (agent *maintenanceAgent) handleCaseGet(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeCoordinatorError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Case retrieval requires GET", core.CategoryInputError, false)
		return
	}
	caseID := strings.TrimPrefix(request.URL.Path, "/api/v1/cases/")
	if !coordinatorCaseIDPattern.MatchString(caseID) {
		writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_CASE_ID", "Case identity is invalid", core.CategoryInputError, false)
		return
	}
	record, err := agent.cases.get(request.Context(), caseID)
	if errors.Is(err, errCaseNotFound) {
		writeCoordinatorError(writer, http.StatusNotFound, "CASE_NOT_FOUND", "Maintenance case was not found", core.CategoryInputError, false)
		return
	}
	if err != nil {
		writeCoordinatorError(writer, http.StatusServiceUnavailable, "CASE_STORE_UNAVAILABLE", "Maintenance case could not be read", core.CategoryServiceError, true)
		return
	}
	writeCoordinatorSuccess(writer, http.StatusOK, record)
}

func (agent *maintenanceAgent) handleCoordinatorHealth(writer http.ResponseWriter, request *http.Request) {
	status := http.StatusOK
	state := "ready"
	if agent.getOrchestrator() == nil {
		status = http.StatusServiceUnavailable
		state = "initializing"
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{
		"status": state, "service": maintenanceAgentName,
		"investigation_ready":        agent.profile.InvestigationReady,
		"collection_enabled":         agent.profile.Collection.Enabled,
		"admission_controls_enabled": agent.admission.Enabled,
		"timestamp":                  agent.now(),
	})
}
