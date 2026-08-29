# Agentic Commerce Control Tower capability demo

This example turns the control-tower proposal into a deliberately small,
locally runnable proof: one asynchronous agent and two discovered tools in a
Kind cluster. It demonstrates evidence-grounded reasoning, deterministic policy
enforcement, idempotent synthetic action, verification, and HITL without
pretending to be a production commerce platform.

The source proposal and preserved research are in
[`AGENTIC_COMMERCE_CONTROL_TOWER.md`](../solution-blueprints/AGENTIC_COMMERCE_CONTROL_TOWER.md).
This README, the checked-in capability contracts, and the scenario assertions
describe the current executable scope.

For a progressive, plain-English explanation of the concept, components,
scenario behavior, live runs, and evidence, start with the
[illustrated beginner tutorial](docs/tutorial/index.html). The
[Markdown learning path](docs/tutorial/README.md) provides more detail.

## What runs

| Pod | Framework role | Responsibility |
|---|---|---|
| `order-operations-agent` | agent | Webhook intake, Redis task worker, tool orchestration, skill binding, HITL |
| `commerce-core` | tool | Eight synthetic orders, six discoverable commerce capabilities, and two hidden demo controls |
| `domain-knowledge-gateway` | tool | Four domain-neutral evidence capabilities backed by embedded LadybugDB |

Redis, the registry/Skills API, ingress, and telemetry services are reused from
the standard TruvaG3 local infrastructure. LadybugDB is a native library and one
database file inside the gateway pod, not another service. The gateway uses an
`emptyDir`, so redeployment rebuilds its small immutable projection from the
selected domain pack.

```text
order-risk webhook -> Redis task -> order-operations-agent
                                      | discovery
                     +----------------+----------------+
                     v                                 v
                commerce-core              domain-knowledge-gateway
            synthetic state + policy       hybrid search + exact evidence
                                                     |
                                             KnowledgeStore interface
                                                     |
                                          LadybugDB embedded database
```

The exact ten-capability boundary is documented in
[`contracts/tool-capabilities.md`](contracts/tool-capabilities.md).

## Why retrieval is more than a dumb search

The gateway first applies tenant, release, jurisdiction, channel, and effective-
date constraints. It then combines BM25-style lexical relevance with a
deterministic semantic feature vector using aliases supplied by the selected
domain pack, followed by reciprocal-rank fusion. Results carry
exact section IDs, source URLs, validity, and the immutable release hash. The
agent can traverse explicit exception/prerequisite/authority relations and must
read decisive sections exactly before asking `commerce-core` for a decision.
Each exact read returns a short-lived signed receipt bound to the release,
section, tenant, jurisdiction, channel, and decision time.

The LLM chooses and sequences tools; it does not decide eligibility. The commerce
tool accepts only exact evidence IDs paired with valid receipts and returns an
authoritative decision token. Execution revalidates the receipts, policy,
current order version, and idempotency binding before applying an action-specific
state transition. A small application policy reads only the deterministic token
class: bounded `automatic.*` decisions proceed, while `approval.*` and
unrecognized tokens pause at a step-level framework checkpoint. After approval,
the agent runtime signs the checkpoint, decision, and idempotency identity; Commerce Core
rejects approval-class execution without that proof even if orchestration were
to fail open.

The commerce pack contains normalized, dated facts from FTC/eCFR, Target,
Walmart, USPS, and FedEx sources plus an explicitly synthetic enterprise policy.
It stores short excerpts and summaries with source URLs rather than copying full
documents. Seven golden cases check expected and forbidden evidence. This is much
closer to a real policy workflow than placing prose in an agent skill, but it is
still a curated snapshot—not legal advice or a live policy ingestion system.

There is no meaningful 2–10 MB design limit. The bundled pack is intentionally
small so the behavior is inspectable. At larger corpus sizes the stable gateway
contract remains, while indexing, embeddings, sharding, and persistence can move
behind `KnowledgeStore`.

## Run it

This repository deliberately does not duplicate TruvaG3's cluster and shared-
infrastructure installer. Keep both repositories as sibling directories:

```text
truvaagents/
├── truva-g3/
└── truva-g3-domain-examples/
```

Bootstrap the workspace once through the framework's maintained travel example:

```bash
cd ../../truva-g3/examples/travel-chat-agent
cp .env.example .env
# Add an AI provider key, then install the framework workspace.
./setup.sh full-deploy
```

Then return to this directory and install only the commerce agent and tools:

```bash
cp .env.example .env
cp order-operations-agent/.env.example order-operations-agent/.env
cp commerce-core/.env.example commerce-core/.env
cp domain-knowledge-gateway/.env.example domain-knowledge-gateway/.env
# Add at least one supported provider key to order-operations-agent/.env.
./setup.sh deploy
```

The sibling framework checkout must be named `truva-g3`; the component modules
resolve the local framework source through that stable workspace layout. Use
`./setup.sh help` for the authoritative verb list; do not build or apply the
child components manually.

The lifecycle follows the same interface as the repository's agent and tool
examples:

| Goal | Command |
|---|---|
| Build all local binaries | `./setup.sh build` |
| Run all three processes against local Redis | `./setup.sh run` |
| Run local component tests | `./setup.sh test-local` |
| Deploy code to an existing cluster | `./setup.sh deploy` |
| Apply configuration-only changes | `./setup.sh rollout` |
| Rebuild changed code without cache | `./setup.sh rebuild` |
| Test deployed services | `./setup.sh smoke` |
| Port-forward the agent or all services | `./setup.sh forward`, `./setup.sh forward-all` |
| Run the default outcome demonstration | `./setup.sh demo` |
| Prove the normal no-agent/no-LLM path only | `./setup.sh demo normal-order` |
| Remove this demo while preserving shared infrastructure | `./setup.sh clean` |

`deploy` and `rebuild` always restart all three deployments after loading their
new `:latest` images. `rollout` never rebuilds images; it recreates configuration
and Secrets, republishes the agent skill best-effort, and restarts the pods.
The optional local `run` path requires a reachable Redis instance and a real AI
provider key. Kind remains the recommended workflow because the framework
bootstrap also supplies the Skills API and observability stack. Cluster and
infrastructure lifecycle remains owned by `truva-g3`; this repository never
deletes or replaces it.

The top-level script orchestrates the lifecycle but does not centralize runtime
configuration. Agent settings and AI credentials are read from
`order-operations-agent/.env`; each tool's settings are read from its own `.env`.
The root `.env` is reserved for setup values shared across components.

Submit a scenario through the agent ingress:

```bash
curl -i -X POST http://agentic-commerce.localhost/webhook/order-risk \
  -H 'Content-Type: application/json' \
  --data-binary @scenarios/usps-express-late.json
```

The `202 Accepted` response includes a `status_url`. Query it on the same host.
If the result is `pending_approval`, inspect and approve the checkpoint, then
resume it:

```bash
curl -s http://agentic-commerce.localhost/hitl/checkpoints
curl -s -X POST http://agentic-commerce.localhost/hitl/command \
  -H 'Content-Type: application/json' \
  -d '{"checkpoint_id":"<checkpoint-id>","type":"approve"}'
curl -s -X POST http://agentic-commerce.localhost/hitl/resume/<checkpoint-id>
```

Event IDs are deduplicated for 24 hours. A duplicate active or completed event
is rejected; a duplicate whose task failed is safely reset and requeued without
creating a second task identity. Change `event_id` when intentionally starting a
new scenario. `./setup.sh smoke` checks all three deployed services without
executing a recovery.

The default demo resets synthetic Commerce state, then reports the deterministic
normal-order path plus a recovered success, a pending approval checkpoint, a
deterministic denial, and an insufficient-evidence abstention. It prints task,
execution, and checkpoint IDs where applicable. The approval checkpoint remains
pending so an operator—not the setup script—decides whether to approve or reject
it.

The fixtures, exact expected outcomes, development-only state controls, and
operator-paired verification steps for conflict, stale HITL bases, and live
domain switching are documented in [`scenarios/README.md`](scenarios/README.md).
Insufficiency was manually verified before becoming the default abstention path;
the remaining five paired scenarios are never triggered by setup or smoke.

## Domain portability

The framework pattern and knowledge gateway are domain-agnostic; the complete
commerce solution is not. A new domain supplies:

1. A pack conforming to [`domain-pack.schema.json`](contracts/domain-pack.schema.json),
   including sources, effective sections, relations, domain vocabulary aliases,
   and golden cases.
2. Domain transaction capabilities analogous to state/options/evaluate/execute/
   verify, with deterministic enforcement around any mutation.
3. A domain procedure skill and an agent prompt that bind those capabilities.

`domain-packs/it-access-demo` is a non-commerce conformance fixture. Gateway tests
load it without code changes and verify retrieval plus relation traversal. To run
the gateway with that bundled pack, change only `DOMAIN_PACK_PATH` in the gateway
ConfigMap to `/app/domain-packs/it-access-demo/pack.json` and use
`./setup.sh rollout`.

## Honest demo boundaries

- Orders, commands, and notifications are process-local synthetic data.
- The source corpus is manually normalized and does not refresh itself.
- Semantic matching uses a deterministic local feature vector, not an external
  embedding model or reranker.
- There is no authentication, tenant authorization, durable volume, backup,
  policy publishing workflow, or external side effect.
- Results should be evaluated with the golden cases; a successful LLM narrative
  is not itself an accuracy measure.
- The shared HMAC key proves demo-internal exact reads and approved resumes; it
  is generated as a Kubernetes Secret and is an integrity control, not user or
  service authentication.

These constraints keep the infrastructure minimal while preserving the contracts
that matter for a realistic future implementation.
