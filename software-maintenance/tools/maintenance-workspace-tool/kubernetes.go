package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/truvaagents/truva-g3/telemetry"
)

const (
	// #nosec G101 -- fixed Kubernetes projected filename, not embedded credential material.
	kubernetesTokenPath       = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	kubernetesCAPath          = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	kubernetesNamespacePath   = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	functionalAcceptanceLabel = "maintenance-functional-acceptance"
)

type kubernetesRunner struct {
	baseURL              string
	namespace            string
	token                string
	httpClient           *http.Client
	functionalAcceptance bool
}

type runnerState struct {
	Status       string
	ExitCode     *int32
	TimedOut     bool
	LogTruncated bool
	StartedAt    time.Time
	CompletedAt  time.Time
	Logs         []byte
}

func newKubernetesRunnerFromEnvironment(functionalAcceptance bool) (*kubernetesRunner, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")
	if port == "" {
		port = "443"
	}
	parsedPort, portErr := strconv.Atoi(port)
	if net.ParseIP(host) == nil || portErr != nil || parsedPort < 1 || parsedPort > 65535 {
		return nil, errors.New("workspace runner requires in-cluster Kubernetes credentials")
	}
	// #nosec G304 -- these are fixed Kubernetes projected service-account paths.
	tokenBytes, err := os.ReadFile(kubernetesTokenPath)
	if err != nil || len(bytes.TrimSpace(tokenBytes)) < 20 {
		return nil, errors.New("read Kubernetes service-account token")
	}
	// #nosec G304 -- fixed Kubernetes projected CA path.
	caBytes, err := os.ReadFile(kubernetesCAPath)
	if err != nil {
		return nil, errors.New("read Kubernetes service CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("parse Kubernetes service CA")
	}
	// #nosec G304 -- fixed Kubernetes projected namespace path.
	namespaceBytes, err := os.ReadFile(kubernetesNamespacePath)
	if err != nil || !workspaceNamePattern.Match(bytes.TrimSpace(namespaceBytes)) {
		return nil, errors.New("read Kubernetes namespace")
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		MaxIdleConns:    8, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, ForceAttemptHTTP2: true,
	}
	client := telemetry.NewTracedHTTPClientWithTransport(transport)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &kubernetesRunner{
		baseURL: "https://" + net.JoinHostPort(host, port), namespace: string(bytes.TrimSpace(namespaceBytes)),
		token: string(bytes.TrimSpace(tokenBytes)), httpClient: client, functionalAcceptance: functionalAcceptance,
	}, nil
}

func (runner *kubernetesRunner) submit(ctx context.Context, profile workspaceProfile, record jobRecord, subPath string, command commandProfile) error {
	if !workspaceIDPattern.MatchString(record.WorkspaceID) || !jobIDPattern.MatchString(record.JobID) || !workspaceNamePattern.MatchString(command.ID) {
		return errors.New("job submission identity is invalid")
	}
	if _, err := normalizeWorkspacePath(subPath); err != nil {
		return errors.New("job workspace path is invalid")
	}
	activeDeadline := profile.Limits.MaximumJobSeconds
	ttlAfterFinished := int64(3600)
	backoffLimit := int32(0)
	readOnlyRoot := true
	readOnlySource := true
	allowPrivilegeEscalation := false
	runAsNonRoot := true
	runAsUser := int64(10002)
	runAsGroup := int64(20001)
	fsGroup := int64(20001)
	jobLabels := runner.jobLabels(record)
	podLabels := runner.jobLabels(record)
	payload := map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{
			"name": record.KubernetesJob, "namespace": runner.namespace,
			"labels": jobLabels,
			"annotations": map[string]string{
				"maintenance.truva.dev/job-id":         record.JobID,
				"maintenance.truva.dev/profile-sha256": record.ProfileSHA256,
				"maintenance.truva.dev/command-sha256": record.CommandSHA256,
			},
		},
		"spec": map[string]any{
			"backoffLimit": backoffLimit, "activeDeadlineSeconds": activeDeadline, "ttlSecondsAfterFinished": ttlAfterFinished,
			"template": map[string]any{
				"metadata": map[string]any{"labels": podLabels},
				"spec": map[string]any{
					"restartPolicy": "Never", "automountServiceAccountToken": false,
					"securityContext": map[string]any{"runAsNonRoot": runAsNonRoot, "runAsUser": runAsUser, "runAsGroup": runAsGroup, "fsGroup": fsGroup, "seccompProfile": map[string]string{"type": "RuntimeDefault"}},
					"containers": []any{map[string]any{
						"name": "runner", "image": profile.RunnerImage, "imagePullPolicy": "IfNotPresent",
						"command": command.Argv[:1], "args": command.Argv[1:], "workingDir": "/workspace",
						"env": []any{
							map[string]string{"name": "HOME", "value": "/tmp"}, map[string]string{"name": "GOCACHE", "value": "/tmp/go-cache"},
							map[string]string{"name": "GOMODCACHE", "value": "/tmp/go-mod-cache"}, map[string]string{"name": "GOWORK", "value": "off"},
							map[string]string{"name": "GOMAXPROCS", "value": "2"}, map[string]string{"name": "GOFLAGS", "value": "-p=2"},
							map[string]string{"name": "GOMEMLIMIT", "value": "2560MiB"},
						},
						"securityContext": map[string]any{"allowPrivilegeEscalation": allowPrivilegeEscalation, "readOnlyRootFilesystem": readOnlyRoot, "capabilities": map[string]any{"drop": []string{"ALL"}}},
						"resources": map[string]any{
							"requests": map[string]string{"cpu": "250m", "memory": "768Mi", "ephemeral-storage": "1Gi"},
							"limits":   map[string]string{"cpu": "2", "memory": "3Gi", "ephemeral-storage": "4Gi"},
						},
						"volumeMounts": []any{
							map[string]any{"name": "workspace", "mountPath": "/workspace", "subPath": subPath, "readOnly": readOnlySource},
							map[string]any{"name": "tmp", "mountPath": "/tmp"},
						},
					}},
					"volumes": []any{
						map[string]any{"name": "workspace", "persistentVolumeClaim": map[string]string{"claimName": profile.WorkspacePVC}},
						map[string]any{"name": "tmp", "emptyDir": map[string]any{"sizeLimit": "4Gi"}},
					},
				},
			},
		},
	}
	status, _, err := runner.request(ctx, http.MethodPost, "/apis/batch/v1/namespaces/"+runner.namespace+"/jobs", payload, 512<<10)
	if err != nil {
		return err
	}
	if status == http.StatusConflict {
		return runner.verifyExistingJob(ctx, record)
	}
	if status != http.StatusCreated {
		return fmt.Errorf("kubernetes rejected sandbox Job with status %d", status)
	}
	return nil
}

func (runner *kubernetesRunner) jobLabels(record jobRecord) map[string]string {
	labels := map[string]string{
		"app":                 workspaceToolName,
		"maintenance-sandbox": "true",
		"maintenance-job-id":  record.JobID[4:20],
	}
	if runner.functionalAcceptance {
		labels[functionalAcceptanceLabel] = "true"
	}
	return labels
}

func (runner *kubernetesRunner) verifyExistingJob(ctx context.Context, record jobRecord) error {
	status, data, err := runner.request(ctx, http.MethodGet, "/apis/batch/v1/namespaces/"+runner.namespace+"/jobs/"+record.KubernetesJob, nil, 512<<10)
	if err != nil || status != http.StatusOK {
		return errors.New("cannot reconcile an existing Kubernetes Job")
	}
	var existing struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if json.Unmarshal(data, &existing) != nil || existing.Metadata.Name != record.KubernetesJob || existing.Metadata.Annotations["maintenance.truva.dev/job-id"] != record.JobID || existing.Metadata.Annotations["maintenance.truva.dev/profile-sha256"] != record.ProfileSHA256 || existing.Metadata.Annotations["maintenance.truva.dev/command-sha256"] != record.CommandSHA256 {
		return errors.New("existing Kubernetes Job does not match the requested job identity")
	}
	return nil
}

func (runner *kubernetesRunner) inspect(ctx context.Context, kubernetesJob string, maximumLogBytes int64) (runnerState, error) {
	if !workspaceNamePattern.MatchString(kubernetesJob) {
		return runnerState{}, errors.New("kubernetes Job name is invalid")
	}
	status, data, err := runner.request(ctx, http.MethodGet, "/apis/batch/v1/namespaces/"+runner.namespace+"/jobs/"+kubernetesJob, nil, 512<<10)
	if err != nil {
		return runnerState{}, err
	}
	if status != http.StatusOK {
		return runnerState{}, fmt.Errorf("kubernetes Job lookup returned status %d", status)
	}
	var job struct {
		Status struct {
			StartTime      time.Time `json:"startTime"`
			CompletionTime time.Time `json:"completionTime"`
			Active         int       `json:"active"`
			Succeeded      int       `json:"succeeded"`
			Failed         int       `json:"failed"`
			Conditions     []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
				Reason string `json:"reason"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &job); err != nil {
		return runnerState{}, errors.New("decode Kubernetes Job")
	}
	state := runnerState{Status: "pending", StartedAt: job.Status.StartTime, CompletedAt: job.Status.CompletionTime}
	if job.Status.Active > 0 {
		state.Status = "running"
	}
	if job.Status.Succeeded > 0 {
		state.Status = "succeeded"
	}
	if job.Status.Failed > 0 {
		state.Status = "failed"
	}
	for _, condition := range job.Status.Conditions {
		if condition.Status == "True" && condition.Reason == "DeadlineExceeded" {
			state.Status = "timed_out"
			state.TimedOut = true
		}
	}
	if state.Status == "pending" || state.Status == "running" {
		return state, nil
	}
	podName, exitCode, finishedAt, err := runner.terminatedPod(ctx, kubernetesJob)
	if err != nil {
		return runnerState{}, err
	}
	state.ExitCode = exitCode
	// Failed Jobs may lack completionTime. Use the runner's recorded termination
	// time, not the observation time, so a failed receipt remains attributable.
	if state.CompletedAt.IsZero() {
		state.CompletedAt = finishedAt
	}
	if state.CompletedAt.IsZero() {
		return runnerState{}, errors.New("sandbox completion timestamp is unavailable")
	}
	logsStatus, logs, truncated, err := runner.requestLogPrefix(ctx, "/api/v1/namespaces/"+runner.namespace+"/pods/"+podName+"/log?container=runner", maximumLogBytes)
	if err != nil {
		return runnerState{}, err
	}
	if logsStatus != http.StatusOK {
		return runnerState{}, fmt.Errorf("kubernetes log lookup returned status %d", logsStatus)
	}
	state.LogTruncated = truncated
	state.Logs = logs
	return state, nil
}

func (runner *kubernetesRunner) terminatedPod(ctx context.Context, kubernetesJob string) (string, *int32, time.Time, error) {
	selector := url.QueryEscape("job-name=" + kubernetesJob)
	status, data, err := runner.request(ctx, http.MethodGet, "/api/v1/namespaces/"+runner.namespace+"/pods?labelSelector="+selector, nil, 1<<20)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if status != http.StatusOK {
		return "", nil, time.Time{}, fmt.Errorf("kubernetes Pod lookup returned status %d", status)
	}
	var pods struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				ContainerStatuses []struct {
					Name  string `json:"name"`
					State struct {
						Terminated *struct {
							ExitCode   int32     `json:"exitCode"`
							FinishedAt time.Time `json:"finishedAt"`
						} `json:"terminated"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &pods); err != nil || len(pods.Items) != 1 || !workspaceNamePattern.MatchString(pods.Items[0].Metadata.Name) {
		return "", nil, time.Time{}, errors.New("kubernetes returned an invalid sandbox Pod set")
	}
	for _, container := range pods.Items[0].Status.ContainerStatuses {
		if container.Name == "runner" && container.State.Terminated != nil {
			exitCode := container.State.Terminated.ExitCode
			return pods.Items[0].Metadata.Name, &exitCode, container.State.Terminated.FinishedAt, nil
		}
	}
	return "", nil, time.Time{}, errors.New("sandbox Pod has no terminated runner state")
}

func (runner *kubernetesRunner) request(ctx context.Context, method, requestPath string, body any, maximumBytes int64) (int, []byte, error) {
	status, data, _, err := runner.requestBounded(ctx, method, requestPath, body, maximumBytes, false)
	return status, data, err
}

// Only successful log responses permit a bounded prefix. Kubernetes JSON and
// error bodies remain strict; an interrupted read is not completed evidence.
func (runner *kubernetesRunner) requestLogPrefix(ctx context.Context, requestPath string, maximumBytes int64) (int, []byte, bool, error) {
	return runner.requestBounded(ctx, http.MethodGet, requestPath, nil, maximumBytes, true)
}

func (runner *kubernetesRunner) requestBounded(ctx context.Context, method, requestPath string, body any, maximumBytes int64, logPrefix bool) (int, []byte, bool, error) {
	if maximumBytes < 0 || maximumBytes == 1<<63-1 {
		return 0, nil, false, errors.New("invalid Kubernetes response byte limit")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, false, errors.New("encode Kubernetes request")
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, runner.baseURL+requestPath, reader)
	if err != nil {
		return 0, nil, false, errors.New("create Kubernetes request")
	}
	request.Header.Set("Authorization", "Bearer "+runner.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := runner.httpClient.Do(request)
	if err != nil {
		return 0, nil, false, fmt.Errorf("kubernetes request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil {
		return 0, nil, false, fmt.Errorf("read Kubernetes response: %w", err)
	}
	if int64(len(data)) > maximumBytes {
		if logPrefix && response.StatusCode == http.StatusOK {
			return response.StatusCode, data[:maximumBytes], true, nil
		}
		return 0, nil, false, errors.New("kubernetes response exceeded its limit")
	}
	return response.StatusCode, data, false, nil
}

func kubernetesJobName(jobID string) string {
	return "mw-" + jobID[4:36]
}
