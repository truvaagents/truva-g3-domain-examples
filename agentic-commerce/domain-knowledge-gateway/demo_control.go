package main

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

type demoDomainPackRequest struct {
	Action   string `json:"action"`
	PackName string `json:"pack_name,omitempty"`
}

func (t *GatewayTool) handleManageDemoDomainPack(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "manage_demo_domain_pack")
	defer observer.finish()
	if !strings.EqualFold(os.Getenv("DEV_MODE"), "true") {
		err := fmt.Errorf("demo domain-pack controls require DEV_MODE=true")
		observer.reject(err, "authorization_error", nil)
		sendError(w, http.StatusForbidden, "DEMO_CONTROL_DISABLED", err.Error())
		return
	}
	var request demoDomainPackRequest
	if !decodeJSON(w, r, &request, observer) {
		return
	}
	normalizeGatewayStrings(&request.Action, &request.PackName)
	observer.validated(map[string]any{"action": request.Action, "pack_name": request.PackName})
	switch request.Action {
	case "status":
	case "activate":
		if request.PackName == "" {
			err := fmt.Errorf("pack_name is required for activation")
			observer.reject(err, "validation_error", nil)
			sendError(w, http.StatusBadRequest, "MISSING_FIELD", err.Error())
			return
		}
		if _, allowed := demoPackDirectories[request.PackName]; !allowed {
			err := fmt.Errorf("unknown allow-listed domain pack %q", request.PackName)
			observer.reject(err, "validation_error", nil)
			sendError(w, http.StatusBadRequest, "UNKNOWN_DOMAIN_PACK", err.Error())
			return
		}
		if _, err := t.store.Activate(r.Context(), request.PackName); err != nil {
			t.handleStoreError(w, observer, err)
			return
		}
	default:
		err := fmt.Errorf("unsupported demo action %q", request.Action)
		observer.reject(err, "validation_error", nil)
		sendError(w, http.StatusBadRequest, "INVALID_ACTION", err.Error())
		return
	}
	activePack, release, err := t.store.ActivePack()
	if err != nil {
		t.handleStoreError(w, observer, err)
		return
	}
	available := make([]string, 0, len(demoPackDirectories))
	for name := range demoPackDirectories {
		available = append(available, name)
	}
	sort.Strings(available)
	observer.succeed(map[string]any{"action": request.Action, "active_pack": activePack, "release_id": release.ReleaseID})
	writeSuccess(w, map[string]any{"action": request.Action, "active_pack": activePack, "release": release, "available_packs": available})
}
