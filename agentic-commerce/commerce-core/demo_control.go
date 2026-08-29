package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

const (
	demoActionStatus                  = "status"
	demoActionReset                   = "reset"
	demoActionSetInventoryUnavailable = "set_inventory_unavailable"
	demoActionActivatePolicyRelease   = "activate_policy_release"
)

type demoStateRequest struct {
	Action          string `json:"action"`
	OrderID         string `json:"order_id,omitempty"`
	ExpectedVersion int    `json:"expected_version,omitempty"`
	ReleaseID       string `json:"release_id,omitempty"`
}

type demoState struct {
	Action            string        `json:"action"`
	ActivePolicy      PolicyRelease `json:"active_policy_release"`
	Order             *Order        `json:"order,omitempty"`
	AvailableReleases []string      `json:"available_policy_releases"`
	OrderCount        int           `json:"order_count"`
}

// ManageDemoState provides deterministic controls for the local capability
// demonstration. The capability is internal and additionally disabled unless
// DEV_MODE=true, so it never enters an LLM tool catalog or a production profile.
func (t *CommerceTool) handleManageDemoState(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "manage_demo_state")
	defer observer.finish()
	if !strings.EqualFold(os.Getenv("DEV_MODE"), "true") {
		err := fmt.Errorf("demo state controls require DEV_MODE=true")
		observer.reject(err, "authorization_error", nil)
		sendCommerceError(w, http.StatusForbidden, "DEMO_CONTROL_DISABLED", err.Error())
		return
	}
	var request demoStateRequest
	if !decodeCommerceJSON(w, r, &request, observer) {
		return
	}
	normalizeCommerceStrings(&request.Action, &request.OrderID, &request.ReleaseID)
	observer.validated(map[string]any{"action": request.Action, "order_id": request.OrderID, "release_id": request.ReleaseID})
	state, err := t.store.ManageDemoState(r.Context(), request)
	if err != nil {
		t.handleCommerceError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"action": request.Action, "release_id": state.ActivePolicy.ReleaseID, "order_id": request.OrderID})
	writeCommerceSuccess(w, state)
}

func (s *CommerceStore) ManageDemoState(ctx context.Context, request demoStateRequest) (demoState, error) {
	if err := ctx.Err(); err != nil {
		return demoState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected *Order
	switch request.Action {
	case demoActionStatus:
		if request.OrderID != "" {
			order, ok := s.orders[request.OrderID]
			if !ok {
				return demoState{}, errOrderNotFound
			}
			copy := cloneOrder(order)
			selected = &copy
		}
	case demoActionReset:
		s.orders = cloneOrders(s.seedOrders)
		s.decisions = map[string]PolicyDecision{}
		s.commands = map[string]CommandResult{}
		s.notifications = map[string]Notification{}
		s.policyRelease = s.policyReleases[s.baselineReleaseID]
		s.sectionIDs = policySectionSet(s.policyRelease)
	case demoActionSetInventoryUnavailable:
		if request.OrderID == "" || request.ExpectedVersion < 1 {
			return demoState{}, fmt.Errorf("order_id and positive expected_version are required")
		}
		order, ok := s.orders[request.OrderID]
		if !ok {
			return demoState{}, errOrderNotFound
		}
		if order.Version != request.ExpectedVersion {
			return demoState{}, fmt.Errorf("%w: expected order version %d, found %d", errDecisionStale, request.ExpectedVersion, order.Version)
		}
		order.InventoryAvailable = false
		order.InventoryReserved = false
		order.Version++
		s.orders[order.OrderID] = order
		copy := cloneOrder(order)
		selected = &copy
	case demoActionActivatePolicyRelease:
		release, ok := s.policyReleases[request.ReleaseID]
		if !ok {
			return demoState{}, fmt.Errorf("unknown demo policy release %q", request.ReleaseID)
		}
		s.policyRelease = release
		s.sectionIDs = policySectionSet(release)
	default:
		return demoState{}, fmt.Errorf("unsupported demo action %q", request.Action)
	}
	releases := make([]string, 0, len(s.policyReleases))
	for releaseID := range s.policyReleases {
		releases = append(releases, releaseID)
	}
	sort.Strings(releases)
	return demoState{Action: request.Action, ActivePolicy: s.policyRelease, Order: selected, AvailableReleases: releases, OrderCount: len(s.orders)}, nil
}

func cloneOrders(source map[string]Order) map[string]Order {
	result := make(map[string]Order, len(source))
	for key, order := range source {
		result[key] = cloneOrder(order)
	}
	return result
}

func cloneOrder(order Order) Order {
	if order.Recovery != nil {
		recovery := *order.Recovery
		order.Recovery = &recovery
	}
	return order
}
