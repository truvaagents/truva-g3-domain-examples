#!/usr/bin/env bash
# Sourced by setup.sh; no framework settings are changed unless explicitly selected.

diagnostic_value_number() {
    local value="$1" number unit
    if [[ "$value" =~ ^[0-9]+$ ]]; then printf '%s\n' "$value"; return; fi
    if [[ "$value" =~ ^([0-9]+)(s|m|h)$ ]]; then
        number="${BASH_REMATCH[1]}"; unit="${BASH_REMATCH[2]}"
        case "$unit" in s) printf '%s\n' "$number";; m) printf '%s\n' "$((number * 60))";; h) printf '%s\n' "$((number * 3600))";; esac
        return
    fi
    printf 'Diagnostic comparison requires a positive integer or simple s/m/h duration\n' >&2
    return 1
}

# Setup accepts simple whole s/m/h durations only. Framework code also supports
# richer Go durations, but the diagnostic comparison deliberately rejects them.
diagnostic_duration_seconds() {
    local value="$1" number unit seconds
    if [[ "$value" == 0 ]]; then printf '0\n'; return; fi
    [[ "$value" =~ ^([0-9]{1,9})(s|m|h)$ ]] || { printf 'Diagnostic duration requires whole s/m/h units (or 0)\n' >&2; return 1; }
    number=$((10#${BASH_REMATCH[1]})); unit="${BASH_REMATCH[2]}"
    case "$unit" in s) seconds="$number";; m) seconds=$((number * 60));; h) seconds=$((number * 3600));; esac
    [[ "$seconds" -le 9223372036 ]] || { printf 'Diagnostic duration exceeds Go duration range\n' >&2; return 1; }
    printf '%s\n' "$seconds"
}

diagnostic_key_number() {
    case "$1" in
        *TIMEOUT|TRUVAG3_RESULT_DISTILL_DEADLINE) diagnostic_duration_seconds "$2" ;;
        *)
            [[ "$2" =~ ^[0-9]{1,9}$ ]] || { printf 'Diagnostic budget requires a bounded whole integer\n' >&2; return 1; }
            printf '%s\n' "$((10#$2))"
            ;;
    esac
}

validate_memory_configuration() {
    # Validate even on the no-profile path. Canonical Go duration parsing keeps
    # compound/fractional units and overflow behavior aligned with constructors.
    command -v go >/dev/null || { printf 'Configuration preflight requires Go on PATH\n' >&2; return 1; }
    GOWORK=off GO111MODULE=off go run "$SCRIPT_DIR/diagnostics/config-preflight/main.go" || return 1
}

load_diagnostic_profile() {
    validate_memory_configuration || return 1
    case "${MAINTENANCE_DIAGNOSTIC_PROFILE:-}" in
        '') return ;;
        expanded-budget) ;;
        *) fail "MAINTENANCE_DIAGNOSTIC_PROFILE must be empty or expanded-budget" ;;
    esac
    local key value prior old_number new_number source
    while IFS='=' read -r key value || [[ -n "$key" ]]; do
        [[ -z "$key" || "$key" == \#* ]] && continue
        if [[ "$key" == TRUVAG3_SKILL_RESOURCE_SELECTION_ATTEMPT_TIMEOUT ]]; then
            # This is a bounded-attempt experiment, not an increase-only budget.
            # Explicit presence wins, including invalid/empty values which fail
            # here before setup can mutate cluster resources. Zero inherits parent.
            diagnostic_duration_seconds "$value" >/dev/null || return 1
            prior="${!key-}"
            source=profile
            if [[ "${!key+x}" == x ]]; then value="$prior"; source=operator; fi
            diagnostic_duration_seconds "$value" >/dev/null || return 1
            export "$key=$value"
            printf '[INFO] %s=%s source=%s\n' "$key" "$value" "$source"
            continue
        fi
        case "$key" in
            TRUVAG3_AI_RETRY_ATTEMPTS|TRUVAG3_PLAN_MAX_TOKENS|TRUVAG3_SYNTHESIS_MAX_TOKENS|TRUVAG3_SKILL_SYNTHESIS_TOKEN_BUDGET|TRUVAG3_SKILL_RESOLUTION_MAX_TOKENS|TRUVAG3_SHARED_MEMORY_EVENT_SUMMARIZER_MAX_TOKENS|TRUVAG3_MICRO_RESOLUTION_MAX_TOKENS|TRUVAG3_ITERATIVE_PHASE_TIMEOUT|TRUVAG3_EXECUTION_STEP_TIMEOUT|TRUVAG3_ORCHESTRATION_TIMEOUT|TRUVAG3_HTTP_WRITE_TIMEOUT|TRUVAG3_RESULT_DISTILL_DEADLINE|TRUVAG3_ITERATIVE_MAX_PHASES) ;;
            *) fail "Unsupported diagnostic profile key: $key" ;;
        esac
        prior="${!key-}"
        new_number="$(diagnostic_key_number "$key" "$value")" || return 1
        [[ "$new_number" -gt 0 ]] || fail "Diagnostic value for $key must be positive"
        if [[ "${!key+x}" == x ]]; then
            old_number="$(diagnostic_key_number "$key" "$prior")" || return 1
            [[ "$old_number" -gt 0 ]] || fail "Diagnostic operator value for $key must be positive"
            [[ "$old_number" -le "$new_number" ]] || fail "Diagnostic profile would lower $key; resolve the higher operator override first"
        fi
        export "$key=$value"
    done < "$SCRIPT_DIR/profiles/diagnostics/expanded-budget.env"
    validate_memory_configuration || return 1
    export MAINTENANCE_DIAGNOSTIC_PROFILE
}

apply_diagnostic_configmap() {
    local component="$1" app patch
    case "$component" in coordinator|repair|review) ;; *) return ;; esac
    app="$(component_app "$component")"
    patch="$(jq -Rn --arg profile "${MAINTENANCE_DIAGNOSTIC_PROFILE:-}" '
      {data: (([inputs | select(length > 0 and (startswith("#") | not)) |
        split("=") | .[0] as $key | {key: $key, value: (env[$key] // null)}] | from_entries) +
        {MAINTENANCE_DIAGNOSTIC_PROFILE: (if $profile == "" then null else $profile end)})}' "$SCRIPT_DIR/profiles/diagnostics/expanded-budget.env")"
    kubectl patch configmap "$app-env-config" -n "$NAMESPACE" --type merge -p "$patch"
}

apply_component_manifest() {
    local component="$1" manifest="$2" app read_timeout
    kubectl apply -f "$manifest"
    case "$component" in repair|review) ;; *) return ;; esac
    app="$(component_app "$component")"
    read_timeout="$(awk '/nginx.ingress.kubernetes.io\/proxy-read-timeout:/ {gsub(/"/, "", $2); print $2}' "$manifest")"
    [[ "$read_timeout" =~ ^[0-9]+$ ]] || fail "Missing base ingress read timeout"
    if [[ "${MAINTENANCE_DIAGNOSTIC_PROFILE:-}" == expanded-budget ]]; then read_timeout=2520; fi
    kubectl annotate ingress "$app" -n "$NAMESPACE" --overwrite "nginx.ingress.kubernetes.io/proxy-read-timeout=$read_timeout"
}
