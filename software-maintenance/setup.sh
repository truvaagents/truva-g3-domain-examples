#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOMAIN_REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TRUVAG3_ROOT="${TRUVAG3_ROOT:-$(dirname "$DOMAIN_REPO_ROOT")/truva-g3}"
SHARED_EXAMPLES_DIR="$TRUVAG3_ROOT/examples"
# Help is usable before the framework checkout or container runtime is installed.
case "${1:-help}" in
    help|-h|--help) ;;
    *)
        [[ -f "$SHARED_EXAMPLES_DIR/k8-deployment/setup-env-lib.sh" ]] || {
            printf 'Framework checkout missing. Clone truva-g3 beside this repo, or export TRUVAG3_ROOT.\n' >&2
            exit 1
        }
        source "$SHARED_EXAMPLES_DIR/k8-deployment/setup-env-lib.sh"
        ;;
esac
source "$SCRIPT_DIR/setup-lib.sh"
source "$SCRIPT_DIR/diagnostic-profile.sh"
source "$SCRIPT_DIR/preflight.sh"

CLUSTER_NAME="${CLUSTER_NAME:-truvag3-demo-$(whoami)}"
NAMESPACE="software-maintenance"

EVIDENCE_APP="maintenance-evidence-tool"
EVIDENCE_IMAGE="$EVIDENCE_APP:latest"
EVIDENCE_PORT=8397
EVIDENCE_MANIFEST="$SCRIPT_DIR/deploy/maintenance-evidence-tool.yaml"
EVIDENCE_DOCKERFILE="$SCRIPT_DIR/tools/maintenance-evidence-tool/Dockerfile"

ASSESSMENT_APP="maintenance-assessment-tool"
ASSESSMENT_IMAGE="$ASSESSMENT_APP:latest"
ASSESSMENT_PORT=8404
ASSESSMENT_MANIFEST="$SCRIPT_DIR/deploy/maintenance-assessment-tool.yaml"
ASSESSMENT_DOCKERFILE="$SCRIPT_DIR/tools/maintenance-assessment-tool/Dockerfile"

SOURCE_APP="maintenance-source-tool"
SOURCE_IMAGE="$SOURCE_APP:latest"
SOURCE_PORT=8399
SOURCE_MANIFEST="$SCRIPT_DIR/deploy/maintenance-source-tool.yaml"
SOURCE_DOCKERFILE="$SCRIPT_DIR/tools/maintenance-source-tool/Dockerfile"

WORKSPACE_APP="maintenance-workspace-tool"
WORKSPACE_IMAGE="$WORKSPACE_APP:latest"
WORKSPACE_PORT=8401
WORKSPACE_MANIFEST="$SCRIPT_DIR/deploy/maintenance-workspace-tool.yaml"
WORKSPACE_DOCKERFILE="$SCRIPT_DIR/tools/maintenance-workspace-tool/Dockerfile"

PUBLICATION_APP="maintenance-publication-tool"
PUBLICATION_IMAGE="$PUBLICATION_APP:latest"
PUBLICATION_PORT=8403
PUBLICATION_MANIFEST="$SCRIPT_DIR/deploy/maintenance-publication-tool.yaml"
PUBLICATION_DOCKERFILE="$SCRIPT_DIR/tools/maintenance-publication-tool/Dockerfile"

REPAIR_APP="maintenance-repair-agent"
REPAIR_IMAGE="$REPAIR_APP:latest"
REPAIR_PORT=8400
REPAIR_MANIFEST="$SCRIPT_DIR/deploy/maintenance-repair-agent.yaml"
REPAIR_DOCKERFILE="$SCRIPT_DIR/agents/maintenance-repair-agent/Dockerfile"

REVIEW_APP="maintenance-review-agent"
REVIEW_IMAGE="$REVIEW_APP:latest"
REVIEW_PORT=8402
REVIEW_MANIFEST="$SCRIPT_DIR/deploy/maintenance-review-agent.yaml"
REVIEW_DOCKERFILE="$SCRIPT_DIR/agents/maintenance-review-agent/Dockerfile"

COORDINATOR_APP="maintenance-agent"
COORDINATOR_IMAGE="$COORDINATOR_APP:latest"
COORDINATOR_PORT=8398
COORDINATOR_MANIFEST="$SCRIPT_DIR/deploy/maintenance-agent.yaml"
COORDINATOR_DOCKERFILE="$SCRIPT_DIR/agents/maintenance-agent/Dockerfile"

info() { printf '[INFO] %s\n' "$1"; }
success() { printf '[SUCCESS] %s\n' "$1"; }
fail() { printf '[ERROR] %s\n' "$1" >&2; exit 1; }

load_env() {
    MAINTENANCE_COMPONENT="${1:-}"
    local diagnostic_profile="${MAINTENANCE_DIAGNOSTIC_PROFILE-}"
    local framework_source="${MAINTENANCE_FRAMEWORK_SOURCE-}" framework_source_set="${MAINTENANCE_FRAMEWORK_SOURCE+x}"
    local key value saved_count=0
    local saved_overrides=()
    while IFS='=' read -r key value || [[ -n "$key" ]]; do
        [[ -z "$key" || "$key" == \#* ]] && continue
        if [[ "${!key+x}" == x ]]; then
            saved_overrides[$saved_count]="$key=${!key}"
            saved_count=$((saved_count + 1))
        fi
    done < "$SCRIPT_DIR/profiles/diagnostics/expanded-budget.env"
    maintenance_load_component_env "${1:-}"
    if [[ "$framework_source_set" == x ]]; then export MAINTENANCE_FRAMEWORK_SOURCE="$framework_source"; fi

    local index
    for ((index=0; index<saved_count; index++)); do export "${saved_overrides[$index]}"; done
    # The explicit invocation selects the overlay; base .env remains untouched.
    export MAINTENANCE_DIAGNOSTIC_PROFILE="$diagnostic_profile"
    case "${1:-}" in
        coordinator|repair|review) load_diagnostic_profile ;;
        *) validate_memory_configuration ;;
    esac

    # The shared secret helper iterates the complete provider registry while
    # this example documents only the common provider keys. Define every
    # optional provider variable so strict shell mode treats an unconfigured
    # provider as empty instead of aborting deployment.
    local key
    for key in $TRUVAG3_ALL_SECRET_KEYS; do
        export "$key=${!key-}"
    done
}

normalize_component() {
    case "${1:-evidence}" in
        evidence|maintenance-evidence-tool) printf 'evidence\n' ;;
        assessment|maintenance-assessment-tool) printf 'assessment\n' ;;
        source|maintenance-source-tool) printf 'source\n' ;;
        workspace|maintenance-workspace-tool) printf 'workspace\n' ;;
        publication|maintenance-publication-tool) printf 'publication\n' ;;
        repair|maintenance-repair-agent) printf 'repair\n' ;;
        review|maintenance-review-agent) printf 'review\n' ;;
        coordinator|maintenance-agent) printf 'coordinator\n' ;;
        *) fail "Unknown component '$1'. Supported components: evidence, assessment, source, workspace, publication, repair, review, coordinator" ;;
    esac
}

component_app() {
    case "$1" in
        assessment) printf '%s\n' "$ASSESSMENT_APP" ;;
        evidence) printf '%s\n' "$EVIDENCE_APP" ;;
        source) printf '%s\n' "$SOURCE_APP" ;;
        workspace) printf '%s\n' "$WORKSPACE_APP" ;;
        publication) printf '%s\n' "$PUBLICATION_APP" ;;
        repair) printf '%s\n' "$REPAIR_APP" ;;
        review) printf '%s\n' "$REVIEW_APP" ;;
        coordinator) printf '%s\n' "$COORDINATOR_APP" ;;
    esac
}

component_image() {
    case "$1" in
        assessment) printf '%s\n' "$ASSESSMENT_IMAGE" ;;
        evidence) printf '%s\n' "$EVIDENCE_IMAGE" ;;
        source) printf '%s\n' "$SOURCE_IMAGE" ;;
        workspace) printf '%s\n' "$WORKSPACE_IMAGE" ;;
        publication) printf '%s\n' "$PUBLICATION_IMAGE" ;;
        repair) printf '%s\n' "$REPAIR_IMAGE" ;;
        review) printf '%s\n' "$REVIEW_IMAGE" ;;
        coordinator) printf '%s\n' "$COORDINATOR_IMAGE" ;;
    esac
}

component_port() {
    case "$1" in
        assessment) printf '%s\n' "$ASSESSMENT_PORT" ;;
        evidence) printf '%s\n' "$EVIDENCE_PORT" ;;
        source) printf '%s\n' "$SOURCE_PORT" ;;
        workspace) printf '%s\n' "$WORKSPACE_PORT" ;;
        publication) printf '%s\n' "$PUBLICATION_PORT" ;;
        repair) printf '%s\n' "$REPAIR_PORT" ;;
        review) printf '%s\n' "$REVIEW_PORT" ;;
        coordinator) printf '%s\n' "$COORDINATOR_PORT" ;;
    esac
}

component_manifest() {
    case "$1" in
        assessment) printf '%s\n' "$ASSESSMENT_MANIFEST" ;;
        evidence) printf '%s\n' "$EVIDENCE_MANIFEST" ;;
        source) printf '%s\n' "$SOURCE_MANIFEST" ;;
        workspace) printf '%s\n' "$WORKSPACE_MANIFEST" ;;
        publication) printf '%s\n' "$PUBLICATION_MANIFEST" ;;
        repair) printf '%s\n' "$REPAIR_MANIFEST" ;;
        review) printf '%s\n' "$REVIEW_MANIFEST" ;;
        coordinator) printf '%s\n' "$COORDINATOR_MANIFEST" ;;
    esac
}

component_dockerfile() {
    case "$1" in
        assessment) printf '%s\n' "$ASSESSMENT_DOCKERFILE" ;;
        evidence) printf '%s\n' "$EVIDENCE_DOCKERFILE" ;;
        source) printf '%s\n' "$SOURCE_DOCKERFILE" ;;
        workspace) printf '%s\n' "$WORKSPACE_DOCKERFILE" ;;
        publication) printf '%s\n' "$PUBLICATION_DOCKERFILE" ;;
        repair) printf '%s\n' "$REPAIR_DOCKERFILE" ;;
        review) printf '%s\n' "$REVIEW_DOCKERFILE" ;;
        coordinator) printf '%s\n' "$COORDINATOR_DOCKERFILE" ;;
    esac
}

component_source_path() {
    case "$1" in
        evidence|assessment|source|workspace|publication) printf './tools/%s\n' "$(component_app "$1")" ;;
        repair|review|coordinator) printf './agents/%s\n' "$(component_app "$1")" ;;
    esac
}

require_distinct_tokens() {
    local label="$1"
    shift
    local tokens=("$@")
    local i j
    for ((i = 0; i < ${#tokens[@]}; i++)); do
        [ ${#tokens[$i]} -ge 32 ] || fail "$label credential $((i + 1)) must contain at least 32 characters"
        for ((j = i + 1; j < ${#tokens[@]}; j++)); do
            [ "${tokens[$i]}" != "${tokens[$j]}" ] || fail "$label caller credentials must be distinct"
        done
    done
}

require_ai_provider_credentials() {
    local key
    for key in $TRUVAG3_AI_PROVIDER_KEYS; do
        [ -z "${!key:-}" ] || return 0
    done
    fail "Agent requires at least one configured OpenAI-compatible, Anthropic, or Gemini provider credential"
}

require_component_credentials() {
    maintenance_validate_profile_readiness "$1"
    case "$1" in
        assessment)
            require_distinct_tokens "Assessment tool" "${MAINTENANCE_AGENT_API_TOKEN:-}" "${MAINTENANCE_AGENT_SERVICE_TOKEN:-}"
            ;;
        evidence)
            require_distinct_tokens "Evidence tool" \
                "${MAINTENANCE_EVIDENCE_COORDINATOR_TOKEN:-}" \
                "${MAINTENANCE_EVIDENCE_REPAIR_TOKEN:-}" \
                "${MAINTENANCE_EVIDENCE_REVIEW_TOKEN:-}"
            ;;
        source)
            local github_token="${GITHUB_TOKEN:-}"
            [ ${#github_token} -ge 20 ] || fail "GITHUB_TOKEN must contain at least 20 characters"
            require_distinct_tokens "Source tool" \
                "${MAINTENANCE_SOURCE_COORDINATOR_TOKEN:-}" \
                "${MAINTENANCE_SOURCE_REPAIR_TOKEN:-}" \
                "${MAINTENANCE_SOURCE_REVIEW_TOKEN:-}" \
                "${MAINTENANCE_SOURCE_WORKSPACE_TOKEN:-}"
            ;;
        workspace)
            require_distinct_tokens "Workspace tool" \
                "${MAINTENANCE_SOURCE_WORKSPACE_TOKEN:-}" \
                "${MAINTENANCE_WORKSPACE_COORDINATOR_TOKEN:-}" \
                "${MAINTENANCE_WORKSPACE_REPAIR_TOKEN:-}" \
                "${MAINTENANCE_WORKSPACE_REVIEW_TOKEN:-}" \
                "${MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN:-}"
            ;;
        publication)
            require_distinct_tokens "Publication tool" \
                "${MAINTENANCE_PUBLICATION_COORDINATOR_TOKEN:-}"
            if [ "${MAINTENANCE_PUBLICATION_ENABLE_WRITES:-false}" = "true" ]; then
                local github_write_token="${GITHUB_WRITE_TOKEN:-}"
                [ ${#github_write_token} -ge 20 ] || fail "GITHUB_WRITE_TOKEN must contain at least 20 characters when publication writes are enabled"
                require_distinct_tokens "Publication tool" \
                    "${MAINTENANCE_PUBLICATION_COORDINATOR_TOKEN:-}" \
                    "${MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN:-}" \
                    "${MAINTENANCE_REVIEW_PUBLICATION_TOKEN:-}"
            fi
            ;;
        repair)
            require_ai_provider_credentials
            require_distinct_tokens "Repair agent" \
                "${MAINTENANCE_REPAIR_COORDINATOR_TOKEN:-}" \
                "${MAINTENANCE_REPAIR_SERVICE_TOKEN:-}"
            ;;
        review)
            require_ai_provider_credentials
            require_distinct_tokens "Review agent" \
                "${MAINTENANCE_REVIEW_COORDINATOR_TOKEN:-}" \
                "${MAINTENANCE_REVIEW_PUBLICATION_TOKEN:-}" \
                "${MAINTENANCE_REVIEW_SERVICE_TOKEN:-}"
            ;;
        coordinator)
            require_ai_provider_credentials
            require_distinct_tokens "Maintenance coordinator" \
                "${MAINTENANCE_AGENT_API_TOKEN:-}" \
                "${MAINTENANCE_AGENT_SERVICE_TOKEN:-}"
            ;;
    esac
}

configure_runtime() {
    local component="$1"
    kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
    case "$component" in
        assessment)
            maintenance_create_configmap "maintenance-assessment-tool-env-config" "$NAMESPACE" "$SCRIPT_DIR/.env" "APP_ENV" \
                "MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES" \
                "MAINTENANCE_ASSESSMENT_PAGE_LIMIT" "MAINTENANCE_ASSESSMENT_FACT_MAX_BYTES" \
                "MAINTENANCE_ASSESSMENT_LOOKUP_MAX_BYTES" "MAINTENANCE_ASSESSMENT_ORIGIN_MAX_BYTES" "MAINTENANCE_ASSESSMENT_REVALIDATE_AFTER" \
                "MAINTENANCE_REMEDIATION_STATE_MAX_AGE" "MAINTENANCE_LIFECYCLE_DUE_LIMIT" \
                "MAINTENANCE_LIFECYCLE_TERMINAL_RETENTION"
            truvag3_create_tool_secret "maintenance-assessment-tool-secrets" "$NAMESPACE" \
                "MAINTENANCE_AGENT_API_TOKEN" "MAINTENANCE_AGENT_SERVICE_TOKEN"
            ;;
        evidence)
            maintenance_create_configmap "maintenance-evidence-tool-env-config" "$NAMESPACE" "$SCRIPT_DIR/.env" "APP_ENV"
            truvag3_create_tool_secret "maintenance-evidence-tool-secrets" "$NAMESPACE" \
                "MAINTENANCE_EVIDENCE_COORDINATOR_TOKEN" \
                "MAINTENANCE_EVIDENCE_REPAIR_TOKEN" \
                "MAINTENANCE_EVIDENCE_REVIEW_TOKEN"
            ;;
        source)
            maintenance_create_configmap "maintenance-source-tool-env-config" "$NAMESPACE" "$SCRIPT_DIR/.env" "GITHUB_API_BASE_URL"
            truvag3_create_tool_secret "maintenance-source-tool-secrets" "$NAMESPACE" \
                "GITHUB_TOKEN" \
                "MAINTENANCE_SOURCE_COORDINATOR_TOKEN" \
                "MAINTENANCE_SOURCE_REPAIR_TOKEN" \
                "MAINTENANCE_SOURCE_REVIEW_TOKEN" \
                "MAINTENANCE_SOURCE_WORKSPACE_TOKEN"
            ;;
        workspace)
            maintenance_create_configmap "maintenance-workspace-tool-env-config" "$NAMESPACE" "$SCRIPT_DIR/.env" \
                "MAINTENANCE_NETWORK_ISOLATION_ATTESTED" "MAINTENANCE_FUNCTIONAL_ACCEPTANCE_MODE" \
                "MAINTENANCE_WORKSPACE_MAX_JOB_WAIT" "MAINTENANCE_WORKSPACE_JOB_POLL_INTERVAL"
            truvag3_create_tool_secret "maintenance-workspace-tool-secrets" "$NAMESPACE" \
                "MAINTENANCE_SOURCE_WORKSPACE_TOKEN" \
                "MAINTENANCE_WORKSPACE_COORDINATOR_TOKEN" \
                "MAINTENANCE_WORKSPACE_REPAIR_TOKEN" \
                "MAINTENANCE_WORKSPACE_REVIEW_TOKEN" \
                "MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN"
            ;;
        publication)
            maintenance_create_configmap "maintenance-publication-tool-env-config" "$NAMESPACE" "$SCRIPT_DIR/.env" \
                "GITHUB_API_BASE_URL" "MAINTENANCE_PUBLICATION_ENABLE_WRITES" "MAINTENANCE_GITHUB_ERROR_MESSAGE_MAX_BYTES"
            truvag3_create_tool_secret "maintenance-publication-tool-secrets" "$NAMESPACE" \
                "GITHUB_WRITE_TOKEN" \
                "MAINTENANCE_PUBLICATION_COORDINATOR_TOKEN" \
                "MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN" \
                "MAINTENANCE_REVIEW_PUBLICATION_TOKEN"
            ;;
        repair)
            maintenance_create_configmap "maintenance-repair-agent-env-config" "$NAMESPACE" "$SCRIPT_DIR/.env" "APP_ENV" \
                "MAINTENANCE_REPAIR_MAXIMUM_SUMMARY_BYTES"
            truvag3_create_secret "maintenance-repair-agent-secrets" "$NAMESPACE" \
                "MAINTENANCE_REPAIR_COORDINATOR_TOKEN" \
                "MAINTENANCE_REPAIR_SERVICE_TOKEN"
            ;;
        review)
            maintenance_create_configmap "maintenance-review-agent-env-config" "$NAMESPACE" "$SCRIPT_DIR/.env" "APP_ENV" \
                "MAINTENANCE_REVIEW_MAXIMUM_SUMMARY_BYTES"
            truvag3_create_secret "maintenance-review-agent-secrets" "$NAMESPACE" \
                "MAINTENANCE_REVIEW_COORDINATOR_TOKEN" \
                "MAINTENANCE_REVIEW_PUBLICATION_TOKEN" \
                "MAINTENANCE_REVIEW_SERVICE_TOKEN"
            ;;
        coordinator)
            maintenance_create_configmap "maintenance-agent-env-config" "$NAMESPACE" "$SCRIPT_DIR/.env" "APP_ENV" \
                "MAINTENANCE_INVESTIGATION_WORKERS" \
                "MAINTENANCE_ADMISSION_ENABLED" "MAINTENANCE_ADMISSION_RUN_ID" \
                "MAINTENANCE_ADMISSION_MAX_CASES" "MAINTENANCE_ADMISSION_MAX_PENDING" \
                "MAINTENANCE_ADMISSION_MAX_PER_WINDOW" "MAINTENANCE_ADMISSION_FAILURE_QUOTA" \
                "MAINTENANCE_ADMISSION_SUCCESS_QUOTA" "MAINTENANCE_ADMISSION_RUN_DURATION" \
                "MAINTENANCE_ADMISSION_WINDOW" "MAINTENANCE_ADMISSION_COOLDOWN" \
                "MAINTENANCE_ASSESSMENT_RECORD_TIMEOUT" \
                "MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES" \
                "MAINTENANCE_ASSESSMENT_PAGE_LIMIT" "MAINTENANCE_ASSESSMENT_FACT_MAX_BYTES" \
                "MAINTENANCE_ASSESSMENT_LOOKUP_MAX_BYTES" "MAINTENANCE_ASSESSMENT_ORIGIN_MAX_BYTES" "MAINTENANCE_ASSESSMENT_REVALIDATE_AFTER" \
                "MAINTENANCE_REMEDIATION_STATE_MAX_AGE" "MAINTENANCE_LIFECYCLE_DUE_LIMIT" \
                "MAINTENANCE_LIFECYCLE_TERMINAL_RETENTION"
            truvag3_create_secret "maintenance-agent-secrets" "$NAMESPACE" \
                "MAINTENANCE_AGENT_API_TOKEN" \
                "MAINTENANCE_AGENT_SERVICE_TOKEN"
            ;;
    esac
}

prepare_skills() {
    truvag3_prepare_agent_skills "$SCRIPT_DIR/skills/packages"
}

prepare_component_skills() {
    case "$1" in
        repair|review|coordinator) prepare_skills ;;
    esac
}

sync_skills() {
    truvag3_sync_agent_skills "$SCRIPT_DIR/skills/packages"
}

check_skills() {
    truvag3_check_agent_skills "$SCRIPT_DIR/skills/packages"
}

build_binary() {
    local component="$1"
    local app source_path
    app="$(component_app "$component")"
    source_path="$(component_source_path "$component")"
    info "Building $app against published module dependencies (no module-file edits)"
    mkdir -p "$SCRIPT_DIR/bin"
    (
        cd "$SCRIPT_DIR"
        GOWORK=off go build -mod=readonly -o "$SCRIPT_DIR/bin/$app" "$source_path"
    )
    success "Built bin/$app"
}

build_image() (
    # A private, allowlisted context works with Docker and Podman without
    # depending on uncommitted changes to the shared helper.
    local component="$1" no_cache="${2:-}" image dockerfile build_context mode
    image="$(component_image "$component")"
    dockerfile="$(component_dockerfile "$component")"
    mode="${MAINTENANCE_FRAMEWORK_SOURCE:-release}"
    case "$mode" in
        release) ;;
        workspace) dockerfile="$dockerfile.workspace" ;;
        *) fail "MAINTENANCE_FRAMEWORK_SOURCE must be release or workspace" ;;
    esac
    build_context="$(mktemp -d "${TMPDIR:-/tmp}/maintenance-build.XXXXXX")"
    trap 'maintenance_remove_temp "$build_context"' EXIT
    maintenance_prepare_build_context "$mode" "$component" "$build_context"
    cp "$dockerfile" "$build_context/Dockerfile"
    info "Building $image from $mode dependencies"
    truvag3_build_docker "$image" "$build_context/Dockerfile" "$build_context" "$no_cache"
)

load_image() {
    local image
    image="$(component_image "$1")"
    info "Loading $image into Kind cluster $CLUSTER_NAME"
    kind load docker-image "$image" --name "$CLUSTER_NAME"
}

deploy_component() {
    local component="$1"
    maintenance_validate_deployment "$component"
    local app manifest
    app="$(component_app "$component")"
    manifest="$(component_manifest "$component")"
    load_env "$component"
    require_component_credentials "$component"
    maintenance_require_cluster
    prepare_component_skills "$component"
    build_image "$component"
    load_image "$component"
    configure_runtime "$component"
    apply_diagnostic_configmap "$component"
    apply_component_manifest "$component" "$manifest"
    # Images use :latest with imagePullPolicy Never. Applying an unchanged
    # manifest alone leaves existing pods running the old image/configuration.
    kubectl rollout restart "deployment/$app" -n "$NAMESPACE"
    kubectl rollout status "deployment/$app" -n "$NAMESPACE" --timeout=120s
    success "Deployed $app"
}

rebuild_component() {
    local component="$1"
    maintenance_validate_deployment "$component"
    local app manifest
    app="$(component_app "$component")"
    manifest="$(component_manifest "$component")"
    load_env "$component"
    require_component_credentials "$component"
    maintenance_require_cluster
    prepare_component_skills "$component"
    build_image "$component" --no-cache
    load_image "$component"
    configure_runtime "$component"
    apply_diagnostic_configmap "$component"
    apply_component_manifest "$component" "$manifest"
    kubectl rollout restart "deployment/$app" -n "$NAMESPACE"
    kubectl rollout status "deployment/$app" -n "$NAMESPACE" --timeout=120s
    success "Rebuilt and deployed $app"
}

rollout_component() {
    local component="$1"
    maintenance_validate_deployment "$component"
    local app manifest
    app="$(component_app "$component")"
    manifest="$(component_manifest "$component")"
    load_env "$component"
    require_component_credentials "$component"
    maintenance_require_cluster
    prepare_component_skills "$component"
    configure_runtime "$component"
    apply_diagnostic_configmap "$component"
    apply_component_manifest "$component" "$manifest"
    kubectl rollout restart "deployment/$app" -n "$NAMESPACE"
    kubectl rollout status "deployment/$app" -n "$NAMESPACE" --timeout=120s
    success "Rolled out $app configuration"
}

check_evidence_access() {
    # Health endpoints do not authenticate. Exercise a read with the same
    # outbound credential the executor uses to catch cross-workload drift.
    local caller_token="$1"
    curl -fsS -X POST \
        -H "Authorization: Bearer $caller_token" \
        -H "Content-Type: application/json" \
        -d '{"profile_id":"retail-order-service"}' \
        "http://maintenance-evidence-tool.localhost/api/capabilities/get_service_profile" |
        jq -e '.success == true and .data.profile_id == "retail-order-service"' >/dev/null
}

test_component() {
    local component="$1"
    load_env "$component"
    require_component_credentials "$component"
    case "$component" in
        assessment)
            local assessment_base="http://maintenance-assessment-tool.localhost"
            info "Checking assessment-tool health, discovery, and authorized history read"
            curl -fsS "$assessment_base/health" >/dev/null
            curl -fsS "$assessment_base/api/capabilities" >/dev/null
            curl -fsS -X POST -H "Authorization: Bearer $MAINTENANCE_AGENT_SERVICE_TOKEN" \
                -H "Content-Type: application/json" -d '{"profile_id":"retail-order-maintenance"}' \
                "$assessment_base/maintenance/assessments/find" |
                jq -e '.success == true and .data.coordinator_profile_id == "retail-order-maintenance"'
            ;;
        evidence)
            local evidence_base="http://maintenance-evidence-tool.localhost"
            info "Checking evidence-tool health and discovery contract"
            curl -fsS "$evidence_base/health" >/dev/null
            curl -fsS "$evidence_base/api/capabilities" >/dev/null
            curl -fsS -X POST \
                -H "Authorization: Bearer $MAINTENANCE_EVIDENCE_COORDINATOR_TOKEN" \
                -H "Content-Type: application/json" \
                -d '{"profile_id":"retail-order-service"}' \
                "$evidence_base/api/capabilities/get_service_profile" |
                jq -e '.success == true and .data.profile_id == "retail-order-service"'
            ;;
        source)
            local source_base="http://maintenance-source-tool.localhost"
            info "Checking source-tool health, discovery, and exact revision resolution"
            curl -fsS "$source_base/health" >/dev/null
            curl -fsS "$source_base/api/capabilities" >/dev/null
            curl -fsS -X POST \
                -H "Authorization: Bearer $MAINTENANCE_SOURCE_COORDINATOR_TOKEN" \
                -H "Content-Type: application/json" \
                -d '{"repository_id":"retail-order-service","revision":"main"}' \
                "$source_base/api/capabilities/resolve_source_revision" |
                jq -e '.success == true and (.data | type == "object")'
            ;;
        workspace)
            local workspace_base="http://maintenance-workspace-tool.localhost"
            info "Checking workspace-tool health and discovery contract"
            curl -fsS "$workspace_base/health" >/dev/null
            curl -fsS "$workspace_base/api/capabilities" >/dev/null
            ;;
        publication)
            local publication_base="http://maintenance-publication-tool.localhost"
            info "Checking publication-tool health and discovery contract"
            curl -fsS "$publication_base/health" >/dev/null
            curl -fsS "$publication_base/api/capabilities" >/dev/null
            ;;
        repair)
            local repair_base="http://maintenance-repair-agent.localhost"
            info "Checking repair-agent readiness and discovery contract"
            curl -fsS "$repair_base/repair/health" >/dev/null
            curl -fsS "$repair_base/api/capabilities" >/dev/null
            check_evidence_access "$MAINTENANCE_REPAIR_SERVICE_TOKEN"
            ;;
        review)
            local review_base="http://maintenance-review-agent.localhost"
            info "Checking review-agent readiness and discovery contract"
            curl -fsS "$review_base/review/health" >/dev/null
            curl -fsS "$review_base/api/capabilities" >/dev/null
            check_evidence_access "$MAINTENANCE_REVIEW_SERVICE_TOKEN"
            ;;
        coordinator)
            local coordinator_base="http://maintenance-agent.localhost"
            info "Checking coordinator readiness and discovery contract"
            curl -fsS "$coordinator_base/maintenance/health" >/dev/null
            curl -fsS "$coordinator_base/api/capabilities" >/dev/null
            check_evidence_access "$MAINTENANCE_AGENT_SERVICE_TOKEN"
            ;;
    esac
    printf '\n'
    success "$(component_app "$component") smoke checks passed"
}

run_component() {
    local component="$1"
    local app port
    app="$(component_app "$component")"
    port="$(component_port "$component")"
    load_env "$component"
    require_component_credentials "$component"
    prepare_component_skills "$component"
    case "$component" in
        assessment)
            export MAINTENANCE_ASSESSMENT_PROFILE_FILE="${MAINTENANCE_ASSESSMENT_PROFILE_FILE:-$SCRIPT_DIR/profiles/retail-order-maintenance.json}"
            export MAINTENANCE_EVIDENCE_PROFILES_FILE="${MAINTENANCE_EVIDENCE_PROFILES_FILE:-$SCRIPT_DIR/profiles/retail-order-service.json}"
            ;;
        evidence)
            export MAINTENANCE_EVIDENCE_PROFILES_FILE="${MAINTENANCE_EVIDENCE_PROFILES_FILE:-$SCRIPT_DIR/profiles/retail-order-service.json}"
            ;;
        source)
            export MAINTENANCE_SOURCE_PROFILES_FILE="${MAINTENANCE_SOURCE_PROFILES_FILE:-$SCRIPT_DIR/profiles/retail-order-source.json}"
            ;;
        workspace)
            fail "maintenance-workspace-tool requires its in-cluster Kubernetes service account; use './setup.sh deploy workspace'"
            ;;
        publication)
            export MAINTENANCE_PUBLICATION_PROFILES_FILE="${MAINTENANCE_PUBLICATION_PROFILES_FILE:-$SCRIPT_DIR/profiles/retail-order-publication.json}"
            ;;
        repair)
            export MAINTENANCE_REPAIR_PROFILES_FILE="${MAINTENANCE_REPAIR_PROFILES_FILE:-$SCRIPT_DIR/profiles/retail-order-repair.json}"
            ;;
        review)
            export MAINTENANCE_REVIEW_PROFILES_FILE="${MAINTENANCE_REVIEW_PROFILES_FILE:-$SCRIPT_DIR/profiles/retail-order-review.json}"
            ;;
        coordinator)
            export MAINTENANCE_AGENT_PROFILE_FILE="${MAINTENANCE_AGENT_PROFILE_FILE:-$SCRIPT_DIR/profiles/retail-order-maintenance.json}"
            ;;
    esac
    export PORT="$port"
    build_binary "$component"
    cd "$SCRIPT_DIR"
    exec "$SCRIPT_DIR/bin/$app"
}

cleanup_component() {
    load_env
    local component="$1"
    local app manifest
    app="$(component_app "$component")"
    manifest="$(component_manifest "$component")"
    maintenance_require_cluster
    kubectl delete -f "$manifest" --ignore-not-found
    kubectl delete configmap "$app-env-config" -n "$NAMESPACE" --ignore-not-found
    kubectl delete secret "$app-secrets" -n "$NAMESPACE" --ignore-not-found
    success "Removed only $app resources"
}

show_help() {
    cat <<'EOF'
Software Maintenance example setup

Usage: ./setup.sh <command> [component]

Components:
  evidence                 maintenance-evidence-tool (default)
  assessment               maintenance-assessment-tool (passive assessment history/evidence)
  source                   maintenance-source-tool
  workspace                maintenance-workspace-tool (Kubernetes only)
  publication              maintenance-publication-tool (writes require explicit enablement)
  repair                   maintenance-repair-agent (profile-gated candidate development)
  review                   maintenance-review-agent (profile-gated independent review)
  coordinator              maintenance-agent (collection disabled in the shipped profile)

Commands:
  help                     Show this help; no arguments also shows help
  init-config              Create missing root/component .env files; never overwrite
  migrate-config           Split legacy root .env; preserve it in an ignored backup
  validate                 Check configuration and identity wiring without deployment
  deploy-all               Validate and deploy all eight components; coordinator last
  test-all                 Smoke-check all components, skills and preflight
  build [component]        Build the Go binary with GOWORK=off
  image [component]        Build the selected component's container image
  run [component]          Build and run locally using .env
  test [component]         Smoke-test the deployed tool through ingress
  deploy [component]       Build, load, and deploy only the selected component
  rebuild [component]      No-cache image rebuild and deployment rollout
  rollout [component]      Refresh Secret/ConfigMap and restart without rebuilding
  status [component]       Show the selected component's Kubernetes status
  logs [component]         Follow the selected component's logs
  forward [component]      Forward the selected service to its local component port
  full-deploy              Not supported here; bootstrap shared infra in the framework repo
  skills-check             Compare published maintenance skills with Git (read-only)
  skills-sync              Strictly reconcile and verify maintenance skills
  preflight                Read-only viewer checks; with a diagnostic profile, also verify live agent budgets
  cleanup <component>      Remove only the explicitly selected component

Before deploy, run init-config and fill each component's .env. Root .env
contains only shared non-secret settings. Run validate before deploy-all. The retail-order-service is deployed separately from its own repository.
Go must be available on PATH for the standard-library configuration preflight,
including configuration-only rollout. It runs before any cluster mutation.

All images default to published framework dependencies. For local framework
testing, use MAINTENANCE_FRAMEWORK_SOURCE=workspace ./setup.sh rebuild <component>
for any supported agent or tool. This does not change the checked-in go.mod.
EOF
}

command="${1:-help}"

case "$command" in
    help|-h|--help)
        show_help
        ;;
    init-config)
        maintenance_init_config
        ;;
    migrate-config)
        maintenance_migrate_config
        ;;
    validate)
        maintenance_validate_configuration
        ;;
    validate-component)
        component="$(normalize_component "${2:-evidence}")"
        load_env "$component"
        require_component_credentials "$component"
        ;;
    deploy-all)
        maintenance_validate_configuration
        load_env
        maintenance_require_cluster
        for component in evidence source workspace repair review publication assessment coordinator; do
            "$SCRIPT_DIR/setup.sh" deploy "$component"
        done
        "$SCRIPT_DIR/setup.sh" skills-check
        ;;
    test-all)
        load_env
        maintenance_require_cluster
        for component in evidence source workspace repair review publication assessment coordinator; do
            "$SCRIPT_DIR/setup.sh" test "$component"
        done
        "$SCRIPT_DIR/setup.sh" skills-check
        "$SCRIPT_DIR/setup.sh" preflight
        ;;
    preflight)
        maintenance_validate_deployment coordinator
        load_env coordinator
        maintenance_require_cluster
        maintenance_diagnostic_preflight
        maintenance_viewer_preflight
        maintenance_automatic_preflight
        ;;
    skills-check)
        load_env
        check_skills
        ;;
    skills-sync)
        load_env
        sync_skills
        ;;
    build|image|run|test|deploy|rebuild|rollout|status|logs|forward|full-deploy)
        component="$(normalize_component "${2:-evidence}")"
        case "$command" in
            build) build_binary "$component" ;;
            image) load_env "$component"; build_image "$component" ;;
            run) run_component "$component" ;;
            test) test_component "$component" ;;
            deploy) deploy_component "$component" ;;
            rebuild) rebuild_component "$component" ;;
            rollout) rollout_component "$component" ;;
            status)
                load_env
                maintenance_require_cluster
                app="$(component_app "$component")"
                kubectl get deployment,pod,service,ingress -n "$NAMESPACE" -l "app=$app"
                ;;
            logs)
                load_env
                maintenance_require_cluster
                app="$(component_app "$component")"
                kubectl logs -n "$NAMESPACE" -l "app=$app" -f --tail=100
                ;;
            forward)
                load_env
                maintenance_require_cluster
                app="$(component_app "$component")"
                port="$(component_port "$component")"
                truvag3_forward "$app" "$port" 80 "$NAMESPACE"
                ;;
            full-deploy)
                fail "Shared infrastructure is owned by $TRUVAG3_ROOT. Bootstrap it there, then use ./setup.sh deploy-all."
                ;;
        esac
        ;;
    cleanup)
        [ -n "${2:-}" ] || fail "Cleanup is intentionally scoped; choose evidence, source, workspace, publication, repair, review, or coordinator"
        component="$(normalize_component "$2")"
        cleanup_component "$component"
        ;;
    *)
        fail "Unknown command '$command'. Run './setup.sh help'."
        ;;
esac
