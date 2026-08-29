#!/bin/bash
# Agentic Commerce capability-demo lifecycle. This is the only deployment
# entrypoint; component directories intentionally contain no setup scripts.

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOMAIN_REPO_ROOT="$(dirname "$SCRIPT_DIR")"
WORKSPACE_ROOT="$(dirname "$DOMAIN_REPO_ROOT")"
TRUVAG3_ROOT="$WORKSPACE_ROOT/truva-g3"
CLUSTER_NAME="truvag3-demo-$(whoami)"
NAMESPACE="truvag3-examples"
MANIFEST="$SCRIPT_DIR/k8-deployment.yaml"
SKILLS_DIR="$SCRIPT_DIR/order-operations-agent/skills/packages"
SETUP_ENV="$SCRIPT_DIR/.env"
AGENT_ENV="$SCRIPT_DIR/order-operations-agent/.env"
COMMERCE_ENV="$SCRIPT_DIR/commerce-core/.env"
GATEWAY_ENV="$SCRIPT_DIR/domain-knowledge-gateway/.env"
DEPLOYMENTS=(order-operations-agent commerce-core domain-knowledge-gateway)
IMAGES=(order-operations-agent commerce-core domain-knowledge-gateway)
AGENT_PORT=8394
COMMERCE_PORT=8395
GATEWAY_PORT=8396
GO_CACHE_DIR="${GOCACHE:-${TMPDIR:-/tmp}/truvag3-agentic-commerce-go-build}"
BUILD_CONTEXT_DIR=""

SHARED_SETUP="$TRUVAG3_ROOT/examples/k8-deployment/setup-env-lib.sh"
if [ ! -f "$SHARED_SETUP" ]; then
    printf 'TruvaG3 framework checkout not found at %s\n' "$TRUVAG3_ROOT" >&2
    printf 'Clone truva-g3 beside truva-g3-domain-examples before using this script.\n' >&2
    exit 1
fi
source "$SHARED_SETUP"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_header() { echo -e "${BLUE}== Agentic Commerce: $1 ==${NC}"; }
print_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
print_error() { echo -e "${RED}[ERROR]${NC} $1" >&2; }
print_info() { echo -e "${YELLOW}[INFO]${NC} $1"; }

load_env() {
    # The parent owns only demo-wide/setup values. Agent configuration and AI
    # credentials remain colocated with the agent, matching other examples.
    truvag3_load_env "$SETUP_ENV"
    truvag3_load_env "$AGENT_ENV"
}

ensure_component_envs() {
    # Loading in subshells bootstraps missing .env files without allowing one
    # component's PORT, service name, or local paths to leak into another.
    (truvag3_load_env "$COMMERCE_ENV")
    (truvag3_load_env "$GATEWAY_ENV")
}

check_prerequisites() { truvag3_check_prerequisites; }

require_ai_provider() {
    local key
    for key in OPENAI_API_KEY ANTHROPIC_API_KEY GROQ_API_KEY DEEPSEEK_API_KEY XAI_API_KEY MISTRAL_API_KEY QWEN_API_KEY TOGETHER_API_KEY; do
        if [ -n "${!key:-}" ]; then
            return 0
        fi
    done
    print_error "The agent requires at least one supported AI provider key in $AGENT_ENV"
    print_info "Set OPENAI_API_KEY, ANTHROPIC_API_KEY, or another key listed in order-operations-agent/.env.example"
    exit 1
}

require_kubernetes_tools() {
    local missing=()
    for tool in kubectl kind; do
        command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
    done
    if [ ${#missing[@]} -gt 0 ]; then
        print_error "Missing Kubernetes tools: ${missing[*]}"
        exit 1
    fi
}

require_existing_cluster() {
    if ! kind get clusters 2>/dev/null | grep -Fxq "$CLUSTER_NAME"; then
        print_error "Kind cluster $CLUSTER_NAME is not available"
        print_info "Bootstrap the framework workspace first:"
        print_info "  cd $TRUVAG3_ROOT/examples/travel-chat-agent"
        print_info "  ./setup.sh full-deploy"
        exit 1
    fi
    if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
        print_error "Shared namespace $NAMESPACE is not available"
        print_info "Run the framework bootstrap above before deploying this example"
        exit 1
    fi
}

build_all() {
    print_header "Building local binaries"
    local component
    for component in "${DEPLOYMENTS[@]}"; do
        print_info "Building $component"
        (
            cd "$SCRIPT_DIR/$component"
            GOWORK=off GOCACHE="$GO_CACHE_DIR" go mod download
            GOWORK=off GOCACHE="$GO_CACHE_DIR" go build -o "$component" .
        )
    done
    print_success "All component binaries built"
}

test_local() {
    print_header "Local tests"
    local component
    for component in "${DEPLOYMENTS[@]}"; do
        print_info "Testing $component"
        (cd "$SCRIPT_DIR/$component" && GOWORK=off GOCACHE="$GO_CACHE_DIR" go test ./...)
    done
    print_success "All component tests passed"
}

docker_build_all() {
    local no_cache=""
    if [ "${DOCKER_NO_CACHE:-false}" = "true" ] || [ "${1:-}" = "--no-cache" ]; then
        no_cache="--no-cache"
    fi
    print_header "Building three images"
    prepare_docker_context
    trap cleanup_docker_context EXIT
    local image
    for image in "${IMAGES[@]}"; do
        truvag3_build_docker "$image:latest" \
            "$SCRIPT_DIR/$image/Dockerfile.workspace" "$BUILD_CONTEXT_DIR" "$no_cache"
    done
    cleanup_docker_context
    trap - EXIT
}

prepare_docker_context() {
    BUILD_CONTEXT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/truvag3-domain-build.XXXXXX")"
    mkdir -p \
        "$BUILD_CONTEXT_DIR/truva-g3" \
        "$BUILD_CONTEXT_DIR/truva-g3-domain-examples"

    tar -C "$TRUVAG3_ROOT" \
        --exclude='.git' --exclude='coverage.out' --exclude='notes' \
        -cf - ai core orchestration resilience telemetry | \
        tar -C "$BUILD_CONTEXT_DIR/truva-g3" -xf -
    tar -C "$DOMAIN_REPO_ROOT" \
        --exclude='agentic-commerce/.env' \
        --exclude='agentic-commerce/*/.env' \
        --exclude='agentic-commerce/commerce-core/commerce-core' \
        --exclude='agentic-commerce/domain-knowledge-gateway/domain-knowledge-gateway' \
        --exclude='agentic-commerce/order-operations-agent/order-operations-agent' \
        --exclude='agentic-commerce/**/lib-ladybug' \
        --exclude='*.lbug' --exclude='*.wal' \
        -cf - agentic-commerce | \
        tar -C "$BUILD_CONTEXT_DIR/truva-g3-domain-examples" -xf -
}

cleanup_docker_context() {
    if [ -z "$BUILD_CONTEXT_DIR" ]; then
        return
    fi
    case "$BUILD_CONTEXT_DIR" in
        "${TMPDIR:-/tmp}"/truvag3-domain-build.*)
            rm -rf "$BUILD_CONTEXT_DIR"
            ;;
        *)
            print_error "Refusing to remove unexpected build context: $BUILD_CONTEXT_DIR"
            return 1
            ;;
    esac
    BUILD_CONTEXT_DIR=""
}

run_local() {
    print_header "Running all components locally"
    load_env
    require_ai_provider
    ensure_component_envs

    local redis_url="${REDIS_URL:-redis://localhost:6379}"
    if ! command -v redis-cli >/dev/null 2>&1; then
        print_error "redis-cli is required to verify the local Redis dependency"
        print_info "Start Redis on localhost:6379, then rerun this command"
        exit 1
    fi
    if ! redis-cli -u "$redis_url" ping 2>/dev/null | grep -q PONG; then
        print_error "Redis is not reachable at $redis_url"
        print_info "Start Redis or bootstrap TruvaG3, then use ./setup.sh deploy"
        exit 1
    fi

    build_all
    prepare_skills

    local control_key="${CONTROL_PROOF_HMAC_KEY:-agentic-commerce-local-demo-control-key-2026}"
    local otlp_endpoint="${OTEL_EXPORTER_OTLP_ENDPOINT:-http://localhost:4318}"
    local pids=()
    cleanup_local() {
        local pid
        for pid in "${pids[@]}"; do
            kill "$pid" 2>/dev/null || true
        done
    }
    trap cleanup_local EXIT INT TERM

    print_info "Starting commerce-core on localhost:$COMMERCE_PORT"
    (
        cd "$SCRIPT_DIR/commerce-core"
        truvag3_load_env "$COMMERCE_ENV"
        env PORT="${PORT:-$COMMERCE_PORT}" REDIS_URL="$redis_url" NAMESPACE="$NAMESPACE" \
            OTEL_SERVICE_NAME="${OTEL_SERVICE_NAME:-commerce-core}" \
            OTEL_EXPORTER_OTLP_ENDPOINT="${OTEL_EXPORTER_OTLP_ENDPOINT:-$otlp_endpoint}" \
            COMMERCE_ORDERS_PATH="${COMMERCE_ORDERS_PATH:-$SCRIPT_DIR/commerce-core/data/orders.json}" \
            CONTROL_PROOF_HMAC_KEY="$control_key" TRUVAG3_DISCOVERY_RETRY=true \
            ./commerce-core
    ) &
    pids+=("$!")

    print_info "Starting domain-knowledge-gateway on localhost:$GATEWAY_PORT"
    (
        cd "$SCRIPT_DIR/domain-knowledge-gateway"
        truvag3_load_env "$GATEWAY_ENV"
        env PORT="${PORT:-$GATEWAY_PORT}" REDIS_URL="$redis_url" NAMESPACE="$NAMESPACE" \
            OTEL_SERVICE_NAME="${OTEL_SERVICE_NAME:-domain-knowledge-gateway}" \
            OTEL_EXPORTER_OTLP_ENDPOINT="${OTEL_EXPORTER_OTLP_ENDPOINT:-$otlp_endpoint}" \
            DOMAIN_PACK_PATH="${DOMAIN_PACK_PATH:-$SCRIPT_DIR/domain-packs/commerce-demo/pack.json}" \
            KNOWLEDGE_STORE_PATH="${KNOWLEDGE_STORE_PATH:-:memory:}" \
            CONTROL_PROOF_HMAC_KEY="$control_key" TRUVAG3_DISCOVERY_RETRY=true \
            ./domain-knowledge-gateway
    ) &
    pids+=("$!")

    print_info "Starting order-operations-agent on localhost:$AGENT_PORT"
    print_info "Press Ctrl+C to stop all three components"
    (
        cd "$SCRIPT_DIR/order-operations-agent"
        env PORT="${PORT:-$AGENT_PORT}" REDIS_URL="$redis_url" NAMESPACE="$NAMESPACE" \
            OTEL_SERVICE_NAME="${OTEL_SERVICE_NAME:-order-operations-agent}" \
            OTEL_EXPORTER_OTLP_ENDPOINT="${OTEL_EXPORTER_OTLP_ENDPOINT:-$otlp_endpoint}" \
            CONTROL_PROOF_HMAC_KEY="$control_key" \
            TRUVAG3_HITL_WEBHOOK_URL="${TRUVAG3_HITL_WEBHOOK_URL:-http://localhost:$AGENT_PORT/internal/hitl-webhook}" \
            ./order-operations-agent
    )
}

load_images() {
    local image
    for image in "${IMAGES[@]}"; do
        truvag3_load_to_kind "$image:latest" "$CLUSTER_NAME"
    done
}

configure() {
    kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
    ensure_component_envs
    truvag3_create_secret "ai-provider-keys-order-operations-agent" "$NAMESPACE"
    configure_control_secret
    truvag3_create_configmap "order-operations-agent-env-config" "$NAMESPACE" "$AGENT_ENV"
    truvag3_create_configmap "commerce-core-env-config" "$NAMESPACE" "$COMMERCE_ENV"
    truvag3_create_configmap "domain-knowledge-gateway-env-config" "$NAMESPACE" "$GATEWAY_ENV"
}

configure_control_secret() {
    local secret_name="agentic-commerce-control-secrets"
    local control_key="${CONTROL_PROOF_HMAC_KEY:-}"
    if [ -z "$control_key" ] && kubectl get secret "$secret_name" -n "$NAMESPACE" >/dev/null 2>&1; then
        print_info "Preserving existing commerce control key"
        return
    fi
    if [ -z "$control_key" ]; then
        command -v openssl >/dev/null 2>&1 || { print_error "openssl is required to generate the demo control key"; exit 1; }
        control_key="$(openssl rand -hex 32)"
    fi
    if [ ${#control_key} -lt 32 ]; then
        print_error "CONTROL_PROOF_HMAC_KEY must contain at least 32 characters"
        exit 1
    fi
    kubectl create secret generic "$secret_name" -n "$NAMESPACE" \
        --from-literal=CONTROL_PROOF_HMAC_KEY="$control_key" --dry-run=client -o yaml | kubectl apply -f -
    unset control_key
}

prepare_skills() { truvag3_prepare_agent_skills "$SKILLS_DIR"; }

wait_for_rollouts() {
    local deployment
    for deployment in commerce-core domain-knowledge-gateway order-operations-agent; do
        print_info "Waiting for $deployment"
        if ! kubectl rollout status "deployment/$deployment" -n "$NAMESPACE" --timeout=180s; then
            kubectl logs -n "$NAMESPACE" -l "app=$deployment" --tail=80 || true
            return 1
        fi
    done
}

restart_deployments() {
    local deployment
    for deployment in "${DEPLOYMENTS[@]}"; do
        print_info "Restarting $deployment"
        kubectl rollout restart "deployment/$deployment" -n "$NAMESPACE"
    done
}

apply_and_wait() {
    print_info "Applying Kubernetes manifests"
    kubectl apply -f "$MANIFEST"
    # Images use a local :latest tag. Restarting is required on repeat deploys
    # so pods consume the image that was just loaded into Kind.
    restart_deployments
    wait_for_rollouts
    print_success "Agentic Commerce is ready at http://agentic-commerce.localhost"
}

deploy() {
    print_header "Deploy"
    check_prerequisites
    require_kubernetes_tools
    require_existing_cluster
    load_env
    require_ai_provider
    docker_build_all
    load_images
    configure
    prepare_skills
    apply_and_wait
    truvag3_verify_ingress "agentic-commerce.localhost" || true
}

rebuild() {
    print_header "Fresh rebuild"
    check_prerequisites
    require_kubernetes_tools
    require_existing_cluster
    load_env
    require_ai_provider
    DOCKER_NO_CACHE=true docker_build_all
    load_images
    configure
    prepare_skills
    kubectl apply -f "$MANIFEST"
    restart_deployments
    wait_for_rollouts
    print_success "Fresh rebuild complete"
}

rollout() {
    print_header "Configuration rollout"
    check_prerequisites
    require_kubernetes_tools
    require_existing_cluster
    load_env
    require_ai_provider
    configure
    prepare_skills
    kubectl apply -f "$MANIFEST"
    restart_deployments
    wait_for_rollouts
    print_success "Configuration rollout complete"
}

verify() {
    print_header "Ingress verification"
    truvag3_verify_ingress "agentic-commerce.localhost"
}

forward() {
    truvag3_forward "order-operations-agent-service" "$AGENT_PORT" 80 "$NAMESPACE"
}

forward_all() {
    truvag3_forward_all \
        "order-operations-agent-service:$AGENT_PORT:80" \
        "commerce-core-service:$COMMERCE_PORT:80" \
        "domain-knowledge-gateway-service:$GATEWAY_PORT:80" \
        "grafana:3000:80" \
        "prometheus:9090:9090" \
        "jaeger-query:16686:80"
}

status() {
    print_header "Status"
    kubectl get deployment,po,svc,ingress -n "$NAMESPACE" -l app.kubernetes.io/part-of=agentic-commerce
}

logs() {
    local component="${2:-order-operations-agent}"
    case "$component" in
        order-operations-agent|commerce-core|domain-knowledge-gateway) ;;
        *) print_error "Unknown component: $component"; exit 1 ;;
    esac
    kubectl logs -n "$NAMESPACE" -l "app=$component" -f --tail=100
}

smoke() {
    print_header "Smoke checks"
    local forwarding_dir
    forwarding_dir="$(mktemp -d)"
    local pids=()
    cleanup_forwards() {
        local pid
        for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
        rm -f "$forwarding_dir/agent.log" "$forwarding_dir/commerce.log" "$forwarding_dir/gateway.log"
        rmdir "$forwarding_dir" 2>/dev/null || true
    }
    trap cleanup_forwards EXIT
    kubectl port-forward -n "$NAMESPACE" service/order-operations-agent-service "$AGENT_PORT":80 >"$forwarding_dir/agent.log" 2>&1 & pids+=("$!")
    kubectl port-forward -n "$NAMESPACE" service/commerce-core-service "$COMMERCE_PORT":80 >"$forwarding_dir/commerce.log" 2>&1 & pids+=("$!")
    kubectl port-forward -n "$NAMESPACE" service/domain-knowledge-gateway-service "$GATEWAY_PORT":80 >"$forwarding_dir/gateway.log" 2>&1 & pids+=("$!")
    local attempt
    for attempt in $(seq 1 15); do
        if curl -fsS "http://127.0.0.1:$AGENT_PORT/ready" >/dev/null 2>&1 && \
           curl -fsS "http://127.0.0.1:$COMMERCE_PORT/api/capabilities" >/dev/null 2>&1 && \
           curl -fsS "http://127.0.0.1:$GATEWAY_PORT/api/capabilities" >/dev/null 2>&1; then
            break
        fi
        if [ "$attempt" -eq 15 ]; then
            print_error "Timed out waiting for port-forwarded services"
            local log_file
            for log_file in "$forwarding_dir"/*.log; do
                print_info "$(basename "$log_file")"
                sed 's/^/  /' "$log_file" >&2
            done
            return 1
        fi
        sleep 1
    done
    curl -fsS -X POST "http://127.0.0.1:$COMMERCE_PORT/api/capabilities/get_order_context" \
        -H 'Content-Type: application/json' -d '{"order_id":"ORD-1003"}' >/dev/null
    curl -fsS -X POST "http://127.0.0.1:$GATEWAY_PORT/api/capabilities/search_evidence" \
        -H 'Content-Type: application/json' \
        -d '{"query":"Priority Mail Express late delivery claim","tenant_id":"commerce-demo","jurisdiction":"US","channel":"shipping","as_of":"2026-08-20T18:00:00Z","limit":5}' >/dev/null
    print_success "Agent, commerce core, and knowledge gateway responded"
}

test_all() {
    test_local
    smoke
}

eval_retrieval() {
    print_header "Deterministic evaluation"
    (cd "$SCRIPT_DIR/domain-knowledge-gateway" && GOWORK=off GOCACHE="$GO_CACHE_DIR" go test -v ./internal/knowledge -run 'TestGoldenRetrievalCases|TestFiltersApplyBeforeRanking|TestCommercePolicyPinMatchesCanonicalPack')
    (cd "$SCRIPT_DIR/commerce-core" && GOWORK=off GOCACHE="$GO_CACHE_DIR" go test -v ./...)
    print_success "Retrieval gold cases and deterministic commerce decisions passed"
}

domain_conformance() {
    print_header "Alternate-domain conformance"
    (cd "$SCRIPT_DIR/domain-knowledge-gateway" && GOWORK=off GOCACHE="$GO_CACHE_DIR" go test -v ./internal/knowledge -run TestAlternateDomainPackUsesTheSameGatewayContract)
}

demo() {
	local scenario="${2:-all}"
	if [ "$scenario" = "all" ]; then
		demo_suite
		return
	fi
	if [ "$scenario" = "normal-order" ]; then
		demo_normal_order
		return
	fi
	local payload="$SCRIPT_DIR/scenarios/$scenario.json"
	if [ ! -f "$payload" ]; then
		print_error "Unknown scenario '$scenario'"
		print_info "Available: all, normal-order, automatic-recovery, denied-option, insufficient-evidence, late-unshipped, usps-express-late, high-value-missing"
		exit 1
    fi
    command -v jq >/dev/null 2>&1 || { print_error "demo requires jq"; exit 1; }
    local event_id="demo-${scenario}-$(date +%s)"
    print_header "Submitting $scenario"
    jq --arg event_id "$event_id" '.event_id = $event_id' "$payload" | \
        curl -fsS -X POST http://agentic-commerce.localhost/webhook/order-risk \
            -H 'Content-Type: application/json' --data-binary @-
    echo
	print_info "Poll the returned status_url on http://agentic-commerce.localhost"
}

start_demo_commerce_forward() {
	DEMO_FORWARD_DIR="$(mktemp -d)"
	DEMO_COMMERCE_PORT=18395
	kubectl port-forward -n "$NAMESPACE" service/commerce-core-service "$DEMO_COMMERCE_PORT":80 >"$DEMO_FORWARD_DIR/commerce.log" 2>&1 &
	DEMO_FORWARD_PID=$!
	local attempt
	for attempt in $(seq 1 20); do
		if curl -fsS "http://127.0.0.1:$DEMO_COMMERCE_PORT/api/capabilities" >/dev/null 2>&1; then
			return
		fi
		sleep 1
	done
	print_error "Timed out waiting for the Commerce Core demo control"
	sed 's/^/  /' "$DEMO_FORWARD_DIR/commerce.log" >&2
	return 1
}

stop_demo_commerce_forward() {
	if [ -n "${DEMO_FORWARD_PID:-}" ]; then
		kill "$DEMO_FORWARD_PID" 2>/dev/null || true
		wait "$DEMO_FORWARD_PID" 2>/dev/null || true
	fi
	if [ -n "${DEMO_FORWARD_DIR:-}" ]; then
		rm -f "$DEMO_FORWARD_DIR/commerce.log"
		rmdir "$DEMO_FORWARD_DIR" 2>/dev/null || true
	fi
}

demo_normal_order_from_forward() {
	local result
	result="$(jq '{order_id}' "$SCRIPT_DIR/scenarios/normal-order.json" | \
		curl -fsS -X POST "http://127.0.0.1:$DEMO_COMMERCE_PORT/api/capabilities/process_normal_order" \
			-H 'Content-Type: application/json' --data-binary @-)"
	if [ "$(jq -r '.success' <<<"$result")" != "true" ] || \
	   [ "$(jq -r '.data.terminal_outcome' <<<"$result")" != "normal_order_confirmed" ] || \
	   [ "$(jq -r '.data.agent_invoked' <<<"$result")" != "false" ] || \
	   [ "$(jq -r '.data.llm_invoked' <<<"$result")" != "false" ]; then
		print_error "NORMAL_ORDER did not satisfy its deterministic contract"
		echo "$result" | jq .
		return 1
	fi
	printf '%s\n' "$result" | jq '{scenario:"NORMAL_ORDER", terminal_outcome:.data.terminal_outcome, order_id:.data.order_id, order_version:.data.order_version, agent_invoked:.data.agent_invoked, llm_invoked:.data.llm_invoked}'
}

demo_normal_order() {
	command -v jq >/dev/null 2>&1 || { print_error "demo requires jq"; exit 1; }
	require_kubernetes_tools
	start_demo_commerce_forward
	trap stop_demo_commerce_forward EXIT
	print_header "NORMAL_ORDER deterministic path"
	demo_normal_order_from_forward
}

demo_submit_and_assert() {
	local label="$1"
	local scenario="$2"
	local expected="$3"
	local event_id="demo-${scenario}-$(date +%s)-$$"
	local submission task_id status_url task state outcome request_id checkpoint_id
	submission="$(jq --arg event_id "$event_id" '.event_id = $event_id' "$SCRIPT_DIR/scenarios/$scenario.json" | \
		curl -fsS -X POST http://agentic-commerce.localhost/webhook/order-risk \
			-H 'Content-Type: application/json' --data-binary @-)"
	task_id="$(jq -r '.task_id // empty' <<<"$submission")"
	status_url="$(jq -r '.status_url // empty' <<<"$submission")"
	if [ -z "$task_id" ] || [ -z "$status_url" ]; then
		print_error "$label submission did not return a task"
		echo "$submission" | jq .
		return 1
	fi
	local attempt
	for attempt in $(seq 1 180); do
		task="$(curl -fsS "http://agentic-commerce.localhost$status_url")"
		state="$(jq -r '.status // empty' <<<"$task")"
		outcome="$(jq -r '.result.terminal_outcome // empty' <<<"$task")"
		if [ "$outcome" = "$expected" ] || [ "$state" = "failed" ] || [ "$state" = "cancelled" ]; then
			break
		fi
		sleep 2
	done
	if [ "$outcome" != "$expected" ]; then
		print_error "$label outcome '$outcome' did not match '$expected'"
		echo "$task" | jq '{id,status,result,error}'
		return 1
	fi
	request_id="$(jq -r '.result.request_id // empty' <<<"$task")"
	checkpoint_id="$(jq -r '.result.checkpoint_id // empty' <<<"$task")"
	if [ -z "$request_id" ] && [ -n "$checkpoint_id" ]; then
		request_id="$(curl -fsS "http://agentic-commerce.localhost/hitl/checkpoints/$checkpoint_id" | jq -r '.request_id // empty')"
	fi
	printf '%s\n' "$task" | jq --arg scenario "$label" --arg request_id "$request_id" '{scenario:$scenario, task_id:.task_id, request_id:$request_id, terminal_outcome:.result.terminal_outcome, policy_outcome:.result.policy_outcome, checkpoint_id:(.result.checkpoint_id // null)}'
}

demo_suite() {
	command -v jq >/dev/null 2>&1 || { print_error "demo requires jq"; exit 1; }
	require_kubernetes_tools
	print_header "Default outcome demonstration"
	print_info "Showing deterministic normal, recovered success, pending approval, denial, and abstention"
	start_demo_commerce_forward
	trap stop_demo_commerce_forward EXIT
	curl -fsS -X POST "http://127.0.0.1:$DEMO_COMMERCE_PORT/api/capabilities/manage_demo_state" \
		-H 'Content-Type: application/json' -d '{"action":"reset"}' >/dev/null
	demo_normal_order_from_forward
	demo_submit_and_assert "AUTO_RECOVER" "automatic-recovery" "recovered"
	demo_submit_and_assert "APPROVAL_REQUIRED" "high-value-missing" "approval_required"
	demo_submit_and_assert "DENIED_OPTION" "denied-option" "denied"
	demo_submit_and_assert "INSUFFICIENT_EVIDENCE" "insufficient-evidence" "insufficient_evidence"
	print_success "Default demonstration produced every required outcome class"
	print_info "The approval checkpoint remains pending for an explicit operator decision"
}

skills_sync() { load_env; truvag3_sync_agent_skills "$SKILLS_DIR"; }
skills_check() { load_env; truvag3_check_agent_skills "$SKILLS_DIR"; }

clean() {
    print_header "Cleanup"
    kubectl delete -f "$MANIFEST" --ignore-not-found
    kubectl delete \
        configmap/order-operations-agent-env-config \
        configmap/commerce-core-env-config \
        configmap/domain-knowledge-gateway-env-config \
        configmap/agentic-commerce-env-config \
        secret/ai-provider-keys-order-operations-agent \
        secret/ai-provider-keys-agentic-commerce \
        secret/agentic-commerce-control-secrets \
        -n "$NAMESPACE" --ignore-not-found
    print_success "Agentic Commerce workloads removed; shared infrastructure remains"
}

show_help() {
    echo "Agentic Commerce capability demo"
    echo
    echo "Usage: ./setup.sh <command>"
    echo
    echo "Local Development Commands:"
    echo "  build         Build all three Go binaries locally"
    echo "  run           Build and run the agent and both tools against local Redis"
    echo "  test-local    Run local Go tests for all three components"
    echo "  eval          Run retrieval gold cases and deterministic decision tests"
    echo "  domain-conformance  Prove the same gateway contract with the IT-access pack"
    echo
    echo "Kubernetes Deployment Commands:"
    echo "  Prerequisite: bootstrap the sibling truva-g3 workspace first"
    echo "  docker-build  Build all three images without changing the cluster"
    echo "  deploy        Build, load, configure, and deploy to an existing Kind cluster"
    echo "  rebuild       No-cache image rebuild and deployment rollout"
    echo "  rollout       Recreate env/secret config and restart without rebuilding"
    echo "  test          Run local tests, then deployed smoke checks"
    echo "  smoke         Check the deployed agent and both tool services"
    echo "  verify        Verify the agent ingress route"
    echo "  forward       Port-forward the agent service on localhost:$AGENT_PORT"
    echo "  forward-all   Port-forward all components and observability services"
	echo "  demo [name]   Run the default outcome suite, or one named scenario (including normal-order)"
    echo "  status        Show this demo's Kubernetes resources"
    echo "  logs [name]   Follow one component (default: order-operations-agent)"
    echo "  skills-sync   Strictly publish the agent-owned skill package"
    echo "  skills-check  Check skill-package drift without changing state"
    echo
    echo "Cleanup Commands:"
    echo "  clean         Remove this demo; preserve the shared cluster and infrastructure"
    echo
    echo "  help          Show this help"
}

case "${1:-help}" in
    build) build_all ;;
    run) run_local ;;
    test-local) test_local ;;
    deploy) deploy ;;
    docker-build) docker_build_all "${2:-}" ;;
    rebuild) rebuild ;;
    rollout) rollout ;;
    test) test_all ;;
    smoke) smoke ;;
    verify) verify ;;
    forward) forward ;;
    forward-all) forward_all ;;
    eval) eval_retrieval ;;
    domain-conformance) domain_conformance ;;
    demo) demo "$@" ;;
    status) status ;;
    logs) logs "$@" ;;
    skills-sync) skills_sync ;;
    skills-check) skills_check ;;
    clean) clean ;;
    help|--help|-h) show_help ;;
    *) print_error "Unknown command: $1"; show_help; exit 1 ;;
esac
