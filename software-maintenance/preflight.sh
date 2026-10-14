#!/usr/bin/env bash
# Read-only prerequisite for operator-triggered diagnostics, not agent routing.
maintenance_automatic_preflight() {
    local base="http://maintenance-agent.localhost" health ledger
    health="$(curl --fail --silent --show-error --max-time 15 "$base/maintenance/health")" || return 1
    if ! printf '%s' "$health" | jq -e '.admission_controls_enabled == true' >/dev/null; then
        printf '%s' "$health" | jq -e '.collection_enabled == false' >/dev/null || {
            printf 'Automatic collection is active without bounded admission controls\n' >&2; return 1;
        }
        return 0
    fi
    [[ -n "${MAINTENANCE_AGENT_API_TOKEN:-}" ]] || { printf 'Admission preflight requires the operator credential\n' >&2; return 1; }
    ledger="$(curl --fail --silent --show-error --max-time 15 -H "Authorization: Bearer $MAINTENANCE_AGENT_API_TOKEN" "$base/api/v1/admission")" || return 1
    printf '%s' "$ledger" | jq -e --arg run "${MAINTENANCE_ADMISSION_RUN_ID:-default}" \
        '.success == true and .data.schema_version == 1 and .data.policy.run_id == $run and .data.policy.max_cases > 0 and .data.deadline > .data.started_at' >/dev/null || {
        printf 'Admission ledger is unavailable or differs from the operator run identity\n' >&2; return 1;
    }
    local expected component pod actual
    expected="$(shasum -a 256 "$SCRIPT_DIR/profiles/retail-order-maintenance.json" | awk '{print $1}')" || return 1
    for component in maintenance-agent maintenance-assessment-tool; do
        pod="$(kubectl get pods -n "$NAMESPACE" -l "app=$component" -o json | jq -er '.items[] | select(.status.phase == "Running" and .metadata.deletionTimestamp == null and (.status.containerStatuses | all(.ready))) | .metadata.name' | head -1)" || return 1
        [[ -n "$pod" ]] || { printf 'No ready %s pod for profile validation\n' "$component" >&2; return 1; }
        actual="$(kubectl exec -n "$NAMESPACE" "$pod" -- sha256sum /app/profiles/retail-order-maintenance.json | awk '{print $1}')" || return 1
        [[ "$actual" == "$expected" ]] || { printf '%s has a stale baked coordinator profile; rebuild it\n' "$component" >&2; return 1; }
    done
    printf '[INFO] Bounded admission ledger and both baked profiles match (not a recovery guarantee)\n'
}

maintenance_diagnostic_preflight() {
    # Opt-in: the ordinary viewer-only preflight retains its previous behavior.
    [[ -n "${MAINTENANCE_DIAGNOSTIC_PROFILE:-}" ]] || return 0
    load_diagnostic_profile || return 1
    local component app key value
    local expected=("MAINTENANCE_DIAGNOSTIC_PROFILE=$MAINTENANCE_DIAGNOSTIC_PROFILE")
    while IFS='=' read -r key value || [[ -n "$key" ]]; do
        [[ -z "$key" || "$key" == \#* ]] && continue
        expected+=("$key=${!key}")
    done < "$SCRIPT_DIR/profiles/diagnostics/expanded-budget.env"
    for component in coordinator repair review; do
        app="$(component_app "$component")"
        kubectl rollout status "deployment/$app" -n "$NAMESPACE" --timeout=30s || return 1
        # Read the live process environment, not merely desired ConfigMap data.
        # Only public numeric/profile settings are passed or printed here.
        kubectl exec -n "$NAMESPACE" "deployment/$app" -- sh -c '
            for setting do
                key=${setting%%=*}; expected=${setting#*=}
                actual=$(printenv "$key") || actual="<unset>"
                if [ "$actual" != "$expected" ]; then
                    printf "Diagnostic mismatch: %s expected=%s actual=%s\n" "$key" "$expected" "$actual" >&2
                    exit 1
                fi
            done
        ' sh "${expected[@]}" || return 1
        printf '[INFO] %s: live diagnostic profile and all budget settings match\n' "$app"
    done
}

maintenance_viewer_preflight() {
    local base="${MAINTENANCE_VIEWER_URL:-http://registry.localhost}" domains
    command -v curl >/dev/null && command -v jq >/dev/null || { printf 'Preflight needs curl and jq\n' >&2; return 1; }
    domains="$(curl --fail --silent --show-error --max-time 15 "$base/api/memory/domains")" || return 1
    printf '%s' "$domains" | jq -e '.domains | type == "array" and index("software-maintenance") != null' >/dev/null || {
        printf 'Viewer runtime configuration is missing software-maintenance; configure its local domain list and redeploy via its setup script\n' >&2; return 1;
    }
    curl --fail --silent --show-error --max-time 15 "$base/api/memory/events?domain=software-maintenance&limit=1" |
        jq -e 'type == "object" and (.events | type == "array")' >/dev/null || return 1
    printf '[INFO] Viewer domain and memory API preflight passed (not proof of backend health or complete history)\n'
}
