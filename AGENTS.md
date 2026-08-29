# AGENTS.md

Guidance for coding assistants working in `truva-g3-domain-examples`.

## Repository boundary

This repository contains domain-specific examples built on TruvaG3. Framework
modules and shared local infrastructure remain in the sibling `truva-g3`
checkout. Keep both repositories under the same parent directory with their
standard names.

Do not copy or fork the framework's cluster, Redis, observability, ingress, or
Registry Viewer setup here. Bootstrap those once through
`truva-g3/examples/travel-chat-agent/setup.sh full-deploy`.

## Run examples through their setup scripts

Every runnable domain example owns one top-level `setup.sh`. Use its documented
verbs; do not hand-run image builds, Kind image loading, or Kubernetes manifests.
The domain script may build, load, configure, deploy, test, and remove only its
own workloads. It must not create or delete the shared cluster or infrastructure.

For Agentic Commerce:

```bash
cd agentic-commerce
./setup.sh help
./setup.sh deploy
```

## Framework conformance

Before changing an agent or tool, read the sibling framework's
`docs/building/AGENT_DEVELOPMENT_GUIDE.md` or
`docs/building/TOOL_DEVELOPMENT_GUIDE.md`. The `my-*` directories under
`truva-g3/examples` are read-only templates: copy them when starting a new
component and never modify them from this repository.

Domain code may use framework APIs but must not introduce domain-specific
behavior into framework modules. Preserve interface boundaries around storage,
retrieval, policy, and external systems.

## Configuration and secrets

Commit `.env.example`, never `.env`. Agent AI credentials stay in the agent's
own `.env`; tool settings stay in each tool's `.env`. A root example `.env` is
only for setup values shared by that example.

Never commit generated binaries, LadybugDB files, WAL files, native-library
downloads, browser captures, or live debug payloads containing secrets.

## Ports

Reserve every agent and tool port in the root [README](README.md#port-registry)
before use. Do not reuse a port already assigned by either repository.

## Solution blueprints

Blueprints are design records, not runnable systems. Preserve their research,
label implementation status honestly, and link implemented slices to their
example. Keep adaptive agent choices separate from deterministic business
invariants such as authorization, state transitions, and idempotency.
