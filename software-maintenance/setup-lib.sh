#!/usr/bin/env bash
# Example-owned composition only; framework setup helpers own build/skills APIs.

maintenance_component_dir() {
    case "$1" in
        coordinator) printf '%s/agents/maintenance-agent\n' "$SCRIPT_DIR" ;;
        repair|review) printf '%s/agents/maintenance-%s-agent\n' "$SCRIPT_DIR" "$1" ;;
        evidence|source|workspace|publication|assessment) printf '%s/tools/maintenance-%s-tool\n' "$SCRIPT_DIR" "$1" ;;
        *) fail "Unknown component: $1" ;;
    esac
}

maintenance_init_config() {
    local directory component
    umask 077
    for component in root evidence source workspace repair review publication assessment coordinator; do
        directory="$SCRIPT_DIR"
        [[ "$component" == root ]] || directory="$(maintenance_component_dir "$component")"
        if [[ ! -e "$directory/.env" ]]; then
            cp "$directory/.env.example" "$directory/.env"
            info "Created $component configuration; fill its credentials before deploy"
        else
            info "Preserved existing $component configuration"
        fi
    done
}

maintenance_migrate_config() (
    # Explicit one-time conversion of the old shared-secret file. Never overwrite
    # an existing component .env, and keep the entire old file recoverable.
    local component directory backup line key
    [[ -f "$SCRIPT_DIR/.env" ]] || fail "No legacy root .env to migrate"
    for component in evidence source workspace repair review publication assessment coordinator; do
        directory="$(maintenance_component_dir "$component")"
        [[ ! -e "$directory/.env" ]] || fail "Refusing to overwrite $component .env; migrate manually from the preserved root file"
    done
    umask 077
    backup="$(mktemp -d "$SCRIPT_DIR/.env-migration.XXXXXX")"
    cp "$SCRIPT_DIR/.env" "$backup/original.env"
    truvag3_load_env "$SCRIPT_DIR/.env"
    for component in root evidence source workspace repair review publication assessment coordinator; do
        directory="$SCRIPT_DIR"
        [[ "$component" == root ]] || directory="$(maintenance_component_dir "$component")"
        while IFS= read -r line || [[ -n "$line" ]]; do
            if [[ "$line" =~ ^(#\ )?([A-Z][A-Z0-9_]*)= ]]; then
                key="${BASH_REMATCH[2]}"
                if [[ "${!key+x}" == x ]]; then
                    printf '%s=%q\n' "$key" "${!key}"
                    continue
                fi
            fi
            printf '%s\n' "$line"
        done < "$directory/.env.example" > "$backup/$component.env"
    done
    for component in evidence source workspace repair review publication assessment coordinator; do
        directory="$(maintenance_component_dir "$component")"
        cp "$backup/$component.env" "$directory/.env"
    done
    cp "$backup/root.env" "$SCRIPT_DIR/.env"
    success "Migrated component configuration; original preserved at $backup/original.env"
)

maintenance_env_key() {
    # Match assignments that the shared shell loader accepts, including export
    # and indentation. Callers inspect names only and never print their values.
    if [[ "$1" =~ ^[[:blank:]]*(export[[:blank:]]+)?([A-Z][A-Z0-9_]*)= ]]; then
        printf '%s\n' "${BASH_REMATCH[2]}"
    fi
}

maintenance_check_root_env() {
    local key line
    [[ -f "$SCRIPT_DIR/.env" ]] || fail "Missing root .env; run ./setup.sh init-config"
    while IFS= read -r line || [[ -n "$line" ]]; do
        key="$(maintenance_env_key "$line")"
        case "$key" in
            \#*|'') continue ;;
            *_TOKEN|*_API_KEY|*_SECRET|*_PASSWORD|*_ACCESS_KEY_ID|*_SECRET_ACCESS_KEY)
                fail "Root .env contains credential setting $key; move credentials to component .env files. The original file is preserved."
                ;;
        esac
    done < "$SCRIPT_DIR/.env"
}

maintenance_load_component_env() {
    local component="${1:-}" directory
    maintenance_check_root_env
    truvag3_load_env "$SCRIPT_DIR/.env"
    [[ "${NAMESPACE:-software-maintenance}" == software-maintenance ]] ||
        fail "Manifests are pinned to namespace software-maintenance; changing NAMESPACE alone is unsupported"
    if [[ -n "$component" ]]; then
        directory="$(maintenance_component_dir "$component")"
        [[ -f "$directory/.env" ]] || fail "Missing $component .env; run ./setup.sh init-config"
        truvag3_load_env "$directory/.env"
    fi
    [[ "${NAMESPACE:-software-maintenance}" == software-maintenance ]] ||
        fail "Component NAMESPACE must match the pinned software-maintenance manifests"
}

maintenance_validate_configuration() (
    local component directory key line seen
    maintenance_check_root_env
    maintenance_validate_profile_pins
    maintenance_validate_shared_summary
    # Isolated children run before this validator loads other components' keys.
    for component in evidence source workspace repair review publication assessment coordinator; do
        "$SCRIPT_DIR/setup.sh" validate-component "$component"
    done
    truvag3_load_env "$SCRIPT_DIR/.env"
    # Validation gets all identities in this short-lived subshell; deployment
    # processes load only their own component credentials.
    for component in evidence source workspace repair review publication assessment coordinator; do
        directory="$(maintenance_component_dir "$component")"
        [[ -f "$directory/.env" ]] || fail "Missing $component .env; run ./setup.sh init-config"
        truvag3_load_env "$directory/.env"
        while IFS= read -r line || [[ -n "$line" ]]; do
            key="$(maintenance_env_key "$line")"
            [[ "$key" =~ ^MAINTENANCE_[A-Z0-9_]+_TOKEN$ ]] || continue
            seen="maintenance_seen_$key"
            if [[ "${!seen+x}" == x ]]; then
                [[ "${!seen}" == "${!key-}" ]] || fail "Shared credential $key differs between component files"
            else
                printf -v "$seen" '%s' "${!key-}"
            fi
        done < "$directory/.env"
    done
    local key
    for key in $TRUVAG3_ALL_SECRET_KEYS; do export "$key=${!key-}"; done
    for component in evidence source workspace repair review publication assessment coordinator; do
        require_component_credentials "$component"
    done
    local pair left right
    for pair in \
        MAINTENANCE_AGENT_SERVICE_TOKEN:MAINTENANCE_EVIDENCE_COORDINATOR_TOKEN \
        MAINTENANCE_AGENT_SERVICE_TOKEN:MAINTENANCE_SOURCE_COORDINATOR_TOKEN \
        MAINTENANCE_AGENT_SERVICE_TOKEN:MAINTENANCE_WORKSPACE_COORDINATOR_TOKEN \
        MAINTENANCE_AGENT_SERVICE_TOKEN:MAINTENANCE_PUBLICATION_COORDINATOR_TOKEN \
        MAINTENANCE_AGENT_SERVICE_TOKEN:MAINTENANCE_REPAIR_COORDINATOR_TOKEN \
        MAINTENANCE_AGENT_SERVICE_TOKEN:MAINTENANCE_REVIEW_COORDINATOR_TOKEN \
        MAINTENANCE_REPAIR_SERVICE_TOKEN:MAINTENANCE_EVIDENCE_REPAIR_TOKEN \
        MAINTENANCE_REPAIR_SERVICE_TOKEN:MAINTENANCE_SOURCE_REPAIR_TOKEN \
        MAINTENANCE_REPAIR_SERVICE_TOKEN:MAINTENANCE_WORKSPACE_REPAIR_TOKEN \
        MAINTENANCE_REVIEW_SERVICE_TOKEN:MAINTENANCE_EVIDENCE_REVIEW_TOKEN \
        MAINTENANCE_REVIEW_SERVICE_TOKEN:MAINTENANCE_SOURCE_REVIEW_TOKEN \
        MAINTENANCE_REVIEW_SERVICE_TOKEN:MAINTENANCE_WORKSPACE_REVIEW_TOKEN; do
        left="${pair%%:*}"; right="${pair#*:}"
        [[ "${!left:-}" == "${!right:-}" ]] || fail "$left must match $right"
    done
    success "Configuration, profile pins and cross-component identities verified; no cluster changes made"
)

maintenance_summary_limit() {
    # Match the agents' decimal integer contract, including + and leading zeros.
    # Bound the normalized text before shell arithmetic to avoid overflow.
    local key="$1" value
    value="${!key-10000}"
    [[ "$value" =~ ^\+?[0-9]+$ ]] || fail "$key must be an integer between 1 and 65536 UTF-8 bytes"
    value="${value#+}"
    while [[ "${#value}" -gt 1 && "$value" == 0* ]]; do value="${value#0}"; done
    [[ "${#value}" -le 5 ]] || fail "$key must be an integer between 1 and 65536 UTF-8 bytes"
    (( value >= 1 && value <= 65536 )) || fail "$key must be an integer between 1 and 65536 UTF-8 bytes"
    printf '%s\n' "$value"
}

maintenance_validate_shared_summary() (
    # Do not let one component's environment mask the other's resolved value.
    local coordinator assessment
    coordinator="$(load_env coordinator >/dev/null || exit 1; maintenance_summary_limit MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES)" || exit 1
    assessment="$(load_env assessment >/dev/null || exit 1; maintenance_summary_limit MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES)" || exit 1
    [[ "$coordinator" == "$assessment" ]] || fail "Coordinator and assessment MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES must match; it participates in profile identity"
)

maintenance_validate_profile_pins() {
    # These are the fixed files baked into the supplied Kubernetes manifests.
    # Service profile_version has a different, typed canonical contract; only
    # agreement of its consumers is checked here, not its authoritative digest.
    local edge consumer dependency key actual expected version=""
    command -v jq >/dev/null || fail "jq is required for profile validation"
    command -v shasum >/dev/null || fail "shasum is required for profile validation"
    for edge in repair:source repair:workspace review:source review:workspace \
        publication:source publication:workspace publication:review \
        maintenance:repair maintenance:review maintenance:publication; do
        consumer="${edge%%:*}"; dependency="${edge#*:}"
        key="expected_${dependency}_profile_sha256"
        expected="$(jq -er --arg key "$key" '.[$key] | select(type == "string")' "$SCRIPT_DIR/profiles/retail-order-$consumer.json")" || fail "Missing $key in $consumer profile"
        actual="$(shasum -a 256 "$SCRIPT_DIR/profiles/retail-order-$dependency.json")" || fail "Cannot hash $dependency profile"
        actual="${actual%% *}"
        [[ "$expected" == "$actual" ]] || fail "Stale $key in $consumer profile; review and repin $dependency before deployment"
    done
    for consumer in repair review maintenance; do
        expected="$(jq -er '.expected_service_profile_version | select(type == "string")' "$SCRIPT_DIR/profiles/retail-order-$consumer.json")" || fail "Missing service profile version in $consumer profile"
        [[ "$expected" =~ ^sha256:[a-f0-9]{64}$ ]] || fail "Invalid service profile version in $consumer profile"
        [[ -z "$version" || "$version" == "$expected" ]] || fail "Service profile version differs between repair, review and coordinator profiles"
        version="$expected"
    done
}

maintenance_validate_deployment() {
    maintenance_validate_profile_pins
    case "$1" in coordinator|assessment) maintenance_validate_shared_summary ;; esac
}

maintenance_validate_profile_readiness() {
    case "$1" in
        coordinator|assessment) maintenance_summary_limit MAINTENANCE_AGENT_MAXIMUM_SUMMARY_BYTES >/dev/null ;;
        repair) maintenance_summary_limit MAINTENANCE_REPAIR_MAXIMUM_SUMMARY_BYTES >/dev/null ;;
        review) maintenance_summary_limit MAINTENANCE_REVIEW_MAXIMUM_SUMMARY_BYTES >/dev/null ;;
    esac
    case "$1" in
        workspace)
            jq -e '.verification_ready | type == "boolean"' "$SCRIPT_DIR/profiles/retail-order-workspace.json" >/dev/null || fail "Invalid workspace readiness profile"
            if jq -e '.verification_ready == true' "$SCRIPT_DIR/profiles/retail-order-workspace.json" >/dev/null; then
                [[ "${MAINTENANCE_NETWORK_ISOLATION_ATTESTED:-false}" == true || "${MAINTENANCE_FUNCTIONAL_ACCEPTANCE_MODE:-false}" == true ]] ||
                    fail "Ready workspace profile requires verified network isolation or explicit development acceptance mode"
            fi
            ;;
        publication)
            jq -e '.publication_ready | type == "boolean"' "$SCRIPT_DIR/profiles/retail-order-publication.json" >/dev/null || fail "Invalid publication readiness profile"
            if jq -e '.publication_ready == true' "$SCRIPT_DIR/profiles/retail-order-publication.json" >/dev/null; then
                [[ "${MAINTENANCE_PUBLICATION_ENABLE_WRITES:-false}" == true ]] ||
                    fail "Ready publication profile requires explicit MAINTENANCE_PUBLICATION_ENABLE_WRITES=true"
            fi
            ;;
    esac
}

maintenance_require_cluster() {
    command -v kubectl >/dev/null || fail "kubectl is required"
    command -v kind >/dev/null || fail "Kind is required"
    kind get clusters | grep -Fxq "$CLUSTER_NAME" ||
        fail "Kind cluster $CLUSTER_NAME is missing; bootstrap shared infrastructure in the framework checkout"
    [[ "$(kubectl config current-context)" == "kind-$CLUSTER_NAME" ]] ||
        fail "Select kubectl context kind-$CLUSTER_NAME before operating this example"
    kubectl get namespace truvag3-examples >/dev/null
    kubectl get service redis -n truvag3-examples >/dev/null
}

maintenance_remove_temp() {
    local directory="$1"
    case "$directory" in
        "${TMPDIR:-/tmp}"/maintenance-build.*|"${TMPDIR:-/tmp}"/maintenance-config.*)
            [[ -d "$directory" && ! -L "$directory" ]] || return 1
            rm -rf -- "$directory"
            ;;
        *) printf 'Refusing unexpected temporary directory\n' >&2; return 1 ;;
    esac
}

maintenance_prepare_build_context() {
    local mode="$1" component="$2" destination="$3" source_dir
    source_dir="$(component_source_path "$component")"
    if [[ "$mode" == workspace ]]; then
        mkdir -p "$destination/framework" "$destination/software-maintenance"
        # No Git metadata, environment files, investigations or binaries enter
        # the context. The original checkout is never edited by workspace builds.
        tar -C "$TRUVAG3_ROOT" --exclude='.git' --exclude='.env*' \
            --exclude='notes' --exclude='bugs' --exclude='papers' \
            --exclude='coverage*' --exclude='*.test' \
            -cf - ai core memory orchestration resilience telemetry |
            tar -C "$destination/framework" -xf -
        destination="$destination/software-maintenance"
    fi
    tar -C "$SCRIPT_DIR" --exclude='.env*' --exclude='*.md' \
        --exclude='*.test' --exclude='maintenance-workspace-tool/maintenance-workspace-tool' \
        -cf - go.mod go.sum internal profiles "$source_dir" |
        tar -C "$destination" -xf -
    cp "$SCRIPT_DIR/.dockerignore" "${3}/.dockerignore"
}

maintenance_create_configmap() (
    local name="$1" namespace="$2" ignored_file="$3"
    shift 3
    local component="$MAINTENANCE_COMPONENT" directory tmp key line include extra source_file
    directory="$(maintenance_component_dir "$component")"
    umask 077
    tmp="$(mktemp -d "${TMPDIR:-/tmp}/maintenance-config.XXXXXX")"
    trap 'maintenance_remove_temp "$tmp"' EXIT
    # Generate only public resolved settings. Component overrides win over root
    # defaults; diagnostic overrides are applied by the existing helper afterward.
    for source_file in "$SCRIPT_DIR/.env" "$directory/.env"; do
        while IFS= read -r line || [[ -n "$line" ]]; do
            key="$(maintenance_env_key "$line")"
            [[ -n "$key" ]] || continue
            case "$key" in TRUVAG3_ROOT|TRUVAG3_CONTAINER_RUNTIME|TRUVAG3_SETUP_AI_PROVIDER) continue ;; esac
            include=false
            [[ "$key" == TRUVAG3_* ]] && include=true
            for extra in $TRUVAG3_CONFIG_INCLUDE_VARS "$@"; do
                [[ "$key" != "$extra" ]] || include=true
            done
            [[ "$include" == true ]] || continue
            case "$key" in *TOKEN*|*API_KEY*|*SECRET*|*PASSWORD*)
                # Output token budgets are public configuration, credentials are not.
                case "$key" in *_MAX_TOKENS|*_TOKEN_BUDGET|*_TOKEN_MULTIPLIER|*_TOKENS) ;; *) continue ;; esac ;;
            esac
            printf '%s=%s\n' "$key" "${!key-}"
        done < "$source_file"
    done | awk '!seen[$0]++' > "$tmp/public.env"
    truvag3_create_configmap "$name" "$namespace" "$tmp/public.env" "$@"
)
