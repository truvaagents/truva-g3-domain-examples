# Agentic Commerce Control Tower Implementation Plan

Status: The agreed capability-demo functional scope is implemented and live
verified at [`agentic-commerce/`](../agentic-commerce/). The broader,
production-shaped release gates retained below are intentionally not all
complete and must not be inferred from the demo result.

The [plain-English tutorial](../agentic-commerce/docs/tutorial/README.md)
explains the implemented design and live verification progressively; this plan
retains the more detailed delivery contracts and deferred production gates.

Implementation snapshot (2026-08-22):

- Delivered: one async agent, two passive tools, ten public capability
  contracts, narrowly scoped hidden demo controls, one root lifecycle script,
  a pinned embedded LadybugDB adapter, a private `KnowledgeStore` seam, exact
  evidence reads, bounded graph traversal, filtered BM25-style plus
  deterministic semantic retrieval with RRF, immutable release hashing, seven
  commerce retrieval gold cases, a non-commerce conformance pack, pack-owned
  semantic aliases, signed scoped exact-read receipts, deterministic policy
  tokens, fail-closed evidence rules, idempotent action-specific commands and
  verification, notification, Redis tasks, safe failed-task retry, skills,
  framework-managed orchestrator-before-worker readiness, checkpoint-bound
  approval proof, resumed-task reconciliation, and decision-aware step-level
  HITL.
- Functional demonstration closure: deterministic normal-order processing
  without an agent or LLM; autonomous recovery; pending, approved, rejected,
  aborted, and expired approval lifecycles; deterministic denial; insufficient
  and conflicting evidence terminals; duplicate-event handling; policy- and
  state-changed-during-HITL guards; fresh reapproval after a stale policy basis;
  and live alternate-domain switching. The default demo presents normal,
  autonomous, pending-approval, denial, and abstention outcomes. Sensitive
  approval proof is redacted from persisted execution data, LLM debug records,
  logs, and traces while the raw value reaches only the guarded command call.
- Explicit capability-demo simplifications: synthetic commerce state is loaded
  from JSON and is process-local; the Ladybug projection is built by gateway
  startup rather than an init container; semantic features are local and
  deterministic rather than external model embeddings; the commerce pack has
  seven reviewed retrieval gold cases rather than the planned fifty; no authentication,
  durable volume, automated acquisition, or production publishing flow exists.
- Still required before this document's full Definition of Done can be claimed:
  durable SQLite state, the 50-case evaluation set and thresholds,
  provider-embedding evaluation, restart and quantitative measurement evidence,
  release-projector hardening, and all production operational and security
  gates. Registry Viewer presentation and lifecycle-join discrepancies are
  tracked separately in its dated observability audit; they do not invalidate
  the correlated Registry API, Jaeger, workload-log, and state proofs captured
  for the functional demo.

This document turns the
[Agentic Commerce Control Tower](AGENTIC_COMMERCE_CONTROL_TOWER.md) blueprint
into an executable delivery plan. The
[Enterprise Knowledge Foundry](ENTERPRISE_KNOWLEDGE_FOUNDRY.md) remains the
source of truth for offline ingestion, parsing, provenance, graph construction,
evaluation, and publication research.

Names and schemas in this document are application-level contracts, not
TruvaG3 framework APIs. The runnable slice's frozen external capability names
match Section 6.7; its narrower payload schemas are recorded beside the code in
`agentic-commerce/contracts/`. The larger examples in this plan remain
target contracts until their corresponding milestones are implemented.

## Table of Contents

- [1. Objective and Definition of Success](#1-objective-and-definition-of-success)
- [2. Target Full-Slice Decisions](#2-target-full-slice-decisions)
- [3. Demonstration Scenarios](#3-demonstration-scenarios)
- [4. Minimal Runtime Topology](#4-minimal-runtime-topology)
- [5. Proposed Example Layout](#5-proposed-example-layout)
- [6. Application Contracts](#6-application-contracts)
- [7. Domain-Pack Contract](#7-domain-pack-contract)
- [8. Retrieval and Decision Pipeline](#8-retrieval-and-decision-pipeline)
- [9. Delivery Milestones](#9-delivery-milestones)
- [10. Verification and Evaluation](#10-verification-and-evaluation)
- [11. Setup and Demonstration Interface](#11-setup-and-demonstration-interface)
- [12. Definition of Done](#12-definition-of-done)
- [13. Explicitly Deferred Work](#13-explicitly-deferred-work)
- [14. Research and Requirement Traceability](#14-research-and-requirement-traceability)

## 1. Objective and Definition of Success

The implementation provides one self-contained example under
`agentic-commerce/`. Its `setup.sh` deploys only the domain workloads into the
local Kind cluster bootstrapped by the sibling `truva-g3` repository.

The primary outcome is:

> When a warehouse disruption affects a next-day order containing a
> lithium-ion battery, the Order Operations Agent retrieves applicable,
> versioned evidence from real regulatory documents and reviewed internal
> policy fixtures; evaluates only currently feasible fulfillment alternatives;
> obtains a deterministic allow, deny, or approval-required decision; executes
> an idempotent recovery command when authorized; and verifies the final
> commerce state before reporting success.

The demonstration is successful only when it proves all of the following:

1. Real public regulatory material remains outside skills and is retrieved as
   exact, cited evidence from an immutable release.
2. Hybrid lexical and semantic retrieval improves paraphrase handling while
   bounded graph traversal resolves relationship-dependent applicability.
3. An LLM never authorizes a transaction or turns regulatory prose directly
   into an executable rule.
4. Low-risk recovery can complete autonomously.
5. Exceptional recovery can suspend and resume through existing TruvaG3 HITL
   infrastructure.
6. Missing or conflicting evidence causes abstention or escalation rather than
   a guessed action.
7. Current state and policy are revalidated immediately before mutation and
   after HITL resume.
8. The Registry Viewer and traces explain the evidence, decision, command, and
   verification path.
9. The same Domain Knowledge Gateway binary can load a small non-commerce pack
   without code changes.
10. Replacing LadybugDB with Neo4j requires no change to agent logic, skills,
    gateway capabilities, domain-pack records, policy logic, or gold cases.

## 2. Target Full-Slice Decisions

This table specifies the accuracy-hardened target needed to claim the complete
Definition of Done in Section 12. It is not a claim about the smaller runnable
demo already delivered. The implementation snapshot above and the runnable
example README are authoritative for current behavior; the principal deltas are
process-local JSON instead of SQLite, startup projection instead of an init
container, and deterministic local semantic features instead of provider
embeddings. Keeping the target here preserves the research and makes the
remaining work explicit.

| Concern | First-slice decision |
|---|---|
| Deployment | One self-contained Kind example deployed through `setup.sh` |
| Agent topology | One `core.BaseAgent` Order Operations Agent in combined push-event API-and-worker mode, bootstrapped from `my-async-agent` |
| Event and task durability | Reuse the existing Redis task queue and task-store pattern |
| HITL | Reuse the existing Redis checkpoint, command, and resume pattern |
| Commerce service | One passive `core.BaseTool` Commerce Core deployable, bootstrapped from `my-tool` |
| Policy | Deterministic capability hosted by Commerce Core, with a separate interface and decision record |
| Commerce state | SQLite in Commerce Core on a demo-only local persistent volume; one replica |
| Knowledge service | One passive `core.BaseTool` Domain Knowledge Gateway deployable, bootstrapped from `my-tool` |
| Knowledge storage | LadybugDB embedded inside the gateway process; no separate database pod |
| Provider seam | One application-internal `KnowledgeStore` interface; Ladybug first, Neo4j migration target |
| Knowledge publication | Immutable, pre-built domain pack is canonical; an init container builds a disposable serving projection |
| Retrieval | Applicability filters, lexical and vector candidates, deterministic fusion, bounded graph expansion, exact reads |
| Fusion | Reciprocal-rank fusion with a fixed constant of 60; no LLM reranker |
| Graph traversal | At most two semantic hops and five returned paths per request |
| Evidence bound | At most five evidence sections and 2,500 estimated content tokens per decision boundary |
| Agent procedure | One Git-owned, explicitly bound order-recovery skill package |
| Customer communication | Synthetic notification capability hosted by Commerce Core |
| Event simulator | Setup-host command or test driver, not another long-running pod |
| Foundry runtime | Not deployed in Kind |
| Additional agents | Deferred until an independent objective justifies another workload |

Before the full-slice gate can pass, two environment-dependent profiles must be
pinned and recorded in the release manifest and `.env.example`:

- the exact LadybugDB and Go-binding revisions, native-library artifact hashes,
  required extension artifacts, and supported Kind target architectures;
- the embedding provider, model identifier, vector dimensions, and query API
  configuration.

Provider-embedding hybrid evaluation is a full-slice release requirement. The
current deterministic local semantic vector is an inspectable capability-demo
baseline, not a substitute for that gate. A deployment with no compatible
query-embedding configuration may support lexical diagnostics, but it does not
satisfy the full Definition of Done. The gateway must reject a query
when its configured embedding model or dimensions do not match the active
release; it must not silently mix incompatible vectors.

## 3. Demonstration Scenarios

Scenario fixtures are version-controlled inputs with expected evidence,
relationship paths, policy decisions, state transitions, and final outcomes.

| ID | Trigger and important facts | Required behavior | Expected terminal result |
|---|---|---|---|
| `NORMAL_ORDER` | Inventory is available at the preferred warehouse | Use the deterministic commerce path without invoking the agent or LLM | Order remains confirmed and reserved |
| `AUTO_RECOVER` | Preferred warehouse fails; a compliant alternative preserves the promise and stays within cost authority | Retrieve applicable evidence, receive `allowed`, reassign, verify, notify | `recovered` |
| `DENIED_OPTION` | The cheapest alternative conflicts with an operationalized lithium-battery routing restriction | Policy rejects that option; the agent evaluates the next feasible option or escalates | Prohibited option is never executed |
| `APPROVAL_REQUIRED` | A compliant alternative exceeds the synthetic autonomous-cost threshold | Persist checkpoint and evidence, wait for approval, reread state and policy, execute only if still valid | `recovered` or `approval_rejected` |
| `INSUFFICIENT_EVIDENCE` | Battery classification or a required applicable section is missing | Return structured insufficiency, do not mutate, escalate | `insufficient_evidence` |
| `CONFLICTING_EVIDENCE` | Two applicable sources conflict and precedence cannot resolve them | Return structured conflict, do not mutate, escalate | `conflicting_evidence` |
| `POLICY_CHANGED_DURING_HITL` | The authority rule changes while approval is pending | Invalidate the old decision basis and request a new decision or approval | No stale approval is used |
| `STATE_CHANGED_DURING_HITL` | Alternative inventory disappears while approval is pending | Reject the stale command and replan from current state | No oversell or stale reassignment |
| `DUPLICATE_EVENT` | The same disruption event and command are delivered more than once | Deduplicate work and apply the business mutation at most once | One state transition |

At least one successful path, one approval path, one deterministic denial, and
one abstention path must be presented in the default live demonstration.

## 4. Minimal Runtime Topology

The diagram below is the target topology at the full Definition of Done. The
delivered demo keeps the same three long-running workloads and public contracts,
but Commerce Core currently uses process-local JSON state and the gateway builds
its disposable LadybugDB projection during startup.

```text
 setup-host scenario driver
             |
             v
 +-----------------------------+
 | Order Operations Agent      |
 | embedded API + worker       |
 | skill + HITL integration    |
 +-------------+---------------+
               |
     discovered capabilities
               |
       +-------+--------------------+
       |                            |
       v                            v
 +----------------------+   +---------------------------+
 | Commerce Core        |   | Domain Knowledge Gateway  |
 | orders + inventory   |   | search + fusion + bounds  |
 | options + commands   |   | exact reads + releases    |
 | policy + notification|   |---------------------------|
 | SQLite               |   | KnowledgeStore            |
 +----------------------+   | LadybugDB adapter          |
                            | embedded serving projection|
                            +---------------------------+

 Existing shared services: Redis, Skills API host, Registry Viewer,
 ingress-nginx, OpenTelemetry Collector, Loki, Prometheus, Jaeger, and Grafana.
```

Long-running solution workloads:

1. Order Operations Agent
2. Commerce Core
3. Domain Knowledge Gateway

At the full gate, the gateway pod gains a short-lived init container that
verifies the canonical domain pack and builds an idempotent LadybugDB serving
projection on a shared `emptyDir`; the main container then opens only an
activated projection. The current demo performs verification and projection in
the main process to avoid another image and lifecycle. The scenario driver is
invoked through `setup.sh` and does not consume another pod. A later Neo4j
adapter may add a database Deployment, Service, Secret, and optional PVC, but it
does not change this application topology above the gateway boundary.

## 5. Proposed Example Layout

This is the target layout for completing all milestones. The delivered minimal
tree is listed by `agentic-commerce/README.md`; absent files below are planned
work, not hidden implementation. The example is self-contained and may depend
only on the sibling framework checkout and its shared
`truva-g3/examples/k8-deployment/` helper.

```text
agentic-commerce/
  README.md
  ARCHITECTURE.md
  setup.sh
  .env.example
  k8-deployment.yaml
  go.mod

  contracts/
    events.go
    commerce.go
    knowledge.go
    policy.go

  commerce-core/
    Dockerfile
    Dockerfile.workspace
    cmd/server/
    internal/orders/
    internal/inventory/
    internal/fulfillment/
    internal/policy/
    internal/notification/
    migrations/

  domain-knowledge-gateway/
    Dockerfile
    Dockerfile.workspace
    cmd/server/
    cmd/load-release/
    internal/retrieval/
    internal/releases/
    internal/store/
      store.go
      ladybug/
    domain-packs/
      commerce-us/
        manifest.yaml
        sources/
        sections.jsonl
        entities.jsonl
        relationships.jsonl
        embeddings/
        retrieval-eval.jsonl
        policy-fixtures/
      it-ops-mini/
        manifest.yaml
        sources/
        sections.jsonl
        entities.jsonl
        relationships.jsonl
        embeddings/
        retrieval-eval.jsonl

  order-operations-agent/
    Dockerfile
    Dockerfile.workspace
    cmd/server/
    internal/worker/
    internal/recovery/
    skills/packages/commerce/order-recovery.json

  test/
    fixtures/scenarios/
    integration/
    store-conformance/
    e2e/
    eval/
```

This layout is proposed rather than implemented. It should be adjusted if Go
module boundaries or the real setup helper require a different self-contained
shape, but the component ownership boundaries must remain intact.

### 5.1 Required bootstrap sources

The implementation starts from repository scaffolds rather than recreating
framework wiring from memory:

| Component | Bootstrap source | Selected shape |
|---|---|---|
| Order Operations Agent | `truva-g3/examples/my-async-agent/` | Push event to queue to worker; one combined Deployment |
| Commerce Core | `truva-g3/examples/my-tool/` | Passive interface-backed tool with multiple capabilities |
| Domain Knowledge Gateway | `truva-g3/examples/my-tool/` | Passive interface-backed tool with multiple capabilities and an embedded store |

`truva-g3/examples/my-streaming-agent/` is a reviewed but deliberately unselected
scaffold. The first slice is an event-driven capability demonstration, not an
SSE chat application. Adding a browser chat surface would be a separate
requirement and would not replace the async agent.

The three `my-*` directories are read-only templates for this work. Before
application code is written, copy `truva-g3/examples/my-async-agent/` to the new
`order-operations-agent/` component and copy `truva-g3/examples/my-tool/` independently
to both `commerce-core/` and `domain-knowledge-gateway/`. Rename and adapt only
the copies under `agentic-commerce/`. Do not add solution code to, edit,
rename, or delete files in any original `truva-g3/examples/my-*` directory.

The copied scaffold files are then consolidated into this self-contained
example; the solution must not import or execute source from sibling example
folders at runtime. One root `setup.sh` owns all three images and Kubernetes
resources. Redundant component-local setup or manifest files from the copies
may be consolidated or removed only inside `agentic-commerce/`; the
original templates remain untouched.

Required scaffold behavior to retain:

- The agent embeds `core.BaseAgent`, initializes orchestration only after
  Discovery is available, and registers its queue consumer through
  `framework.RegisterRunnable` using `core.Runnable`.
- The inbound webhook is a schema-backed agent capability marked
  `Internal: true`, so it remains visible for framework inspection but is
  excluded from LLM tool catalogs. Health is also internal; task-status,
  approval, and administrative control routes remain infrastructure handlers.
  No public agent-as-tool capability is needed while no other agent delegates
  to Order Operations.
- Both tools embed `core.BaseTool`, register capabilities, and remain passive:
  they neither discover nor invoke other TruvaG3 components.
- Every agent-facing tool capability has a front-loaded description plus
  matching `InputSummary` and `OutputSummary`; complex payloads additionally
  expose a validated schema.
- Tool success and failure responses use the framework response and HTTP-status
  conventions so an error cannot be reported as an HTTP 200 success.
- Each workload uses `/health` for liveness. The agent uses `/ready` so it does
  not receive events until discovery-backed orchestration is initialized; the
  passive tools use `/api/capabilities` for readiness. All workloads retain
  consistent service identity across Go, Kubernetes, and telemetry, non-root
  containers, context-aware logging, and tracing middleware.
- The gateway Dockerfile is the one justified scaffold deviation: LadybugDB's
  official Go binding requires CGO and pinned native libraries, whereas the
  ordinary `my-tool` image disables CGO. The compatibility spike must prove
  this deviation on every supported Kind architecture.

## 6. Application Contracts

All application contracts are versioned. Unknown required versions are
rejected rather than guessed. Concrete Go types must replace the illustrative
JSON below during Milestone 1.

### 6.1 Business event

```json
{
  "schema_version": "commerce.event.v1",
  "event_id": "evt-001",
  "event_type": "warehouse_disrupted",
  "occurred_at": "2026-08-20T15:00:00Z",
  "aggregate_type": "warehouse",
  "aggregate_id": "wh-preferred",
  "aggregate_version": 4,
  "correlation_id": "demo-auto-recover",
  "data": {
    "affected_order_ids": ["ord-001"],
    "reason_code": "facility_unavailable"
  }
}
```

Required behavior:

- `event_id` is the deduplication identity.
- Event handling persists work before acknowledging intake.
- Duplicate and out-of-order events cannot create duplicate business changes.
- Business state is always reread from Commerce Core; event payloads are hints,
  not current transaction authority.

### 6.2 Knowledge search

Planned request:

```json
{
  "contract_version": "knowledge.search.v1",
  "release_id": "commerce-us-2026.08",
  "query": "Can this lithium battery order use the alternate air service?",
  "domains": ["fulfillment", "hazardous-materials"],
  "jurisdictions": ["US"],
  "effective_at": "2026-08-20T15:00:00Z",
  "retrieval_modes": ["lexical", "semantic", "graph"],
  "max_results": 5,
  "max_content_tokens": 2500
}
```

Planned result envelope:

```json
{
  "release_id": "commerce-us-2026.08",
  "outcome": "sufficient",
  "evidence": [
    {
      "evidence_id": "evidence-001",
      "authority_class": "regulation",
      "document_id": "49-cfr-173.185",
      "document_version": "release-pinned-version",
      "section_id": "section-173.185",
      "jurisdiction": "US",
      "effective_from": "release-pinned-date",
      "effective_to": null,
      "content_hash": "sha256:...",
      "source_uri": "https://www.ecfr.gov/current/title-49/subtitle-B/chapter-I/subchapter-C/part-173/subpart-E/section-173.185",
      "verification_status": "verified",
      "retrieval_methods": ["lexical", "semantic", "graph"],
      "excerpt": "Bounded exact evidence text",
      "relationship_path_ids": ["path-001"]
    }
  ],
  "conflicts": [],
  "truncated": false
}
```

The gateway must distinguish at least these outcomes:

- `sufficient`
- `insufficient`
- `conflicting`
- `release_unavailable`
- `integrity_failed`
- `embedding_mismatch`
- `temporarily_unavailable`

### 6.3 Exact read and graph traversal

`ReadExactSection` requires release, document version, and section identity. It
must reproduce the same bytes and hash used by the original decision.

`TraverseRelations` requires:

- an active release ID;
- one or more verified seed identities;
- an allowlist of relationship predicates;
- a maximum depth no greater than two;
- path and token bounds.

Every returned edge includes release ID, predicate, verification state,
provenance, and supporting evidence IDs. Unverified edges may be returned only
as advisory hints and are excluded from authoritative paths.

### 6.4 Fulfillment alternative

```json
{
  "option_id": "option-002",
  "order_id": "ord-001",
  "order_version": 7,
  "warehouse_id": "wh-alternate",
  "carrier_service_id": "carrier-ground-priority",
  "estimated_delivery_at": "2026-08-21T20:00:00Z",
  "additional_cost_minor_units": 800,
  "currency": "USD",
  "product_classifications": ["lithium-ion-battery"],
  "computed_at": "2026-08-20T15:01:00Z"
}
```

Commerce Core calculates and returns alternatives. The agent cannot fabricate
an option, cost, promise, inventory balance, or product classification.

### 6.5 Deterministic policy decision

```json
{
  "decision_id": "decision-001",
  "decision": "approval_required",
  "policy_id": "order-recovery-authority",
  "policy_version": "2026.08",
  "reason_codes": ["additional_cost_over_autonomy_limit"],
  "evaluated_order_version": 7,
  "evaluated_option_id": "option-002",
  "source_evidence_refs": [
    "evidence://commerce-us-2026.08/internal/order-recovery-authority/4.2"
  ],
  "expires_at": "2026-08-20T15:06:00Z"
}
```

Allowed decision values are `allowed`, `denied`, and `approval_required`.
Commerce Core rejects an expired decision, a mismatched order version, a
mismatched option, or an approval that does not authorize the same decision
basis.

### 6.6 Idempotent business command

```json
{
  "command_id": "cmd-001",
  "command_type": "reassign_fulfillment",
  "order_id": "ord-001",
  "expected_order_version": 7,
  "option_id": "option-002",
  "decision_id": "decision-001",
  "approval_id": "approval-001-or-null",
  "actor_type": "agent",
  "actor_id": "order-operations-agent"
}
```

Command outcomes are `applied`, `already_applied`, `version_conflict`,
`option_unavailable`, `authority_rejected`, `invalid_transition`, and
`temporary_failure`. Only `applied` and `already_applied` may proceed to final
verification.

### 6.7 Framework registration boundary

The two tool services may each register multiple capabilities; a capability is
not another service. The first slice targets this bounded catalog:

| Component | Planned discoverable capabilities |
|---|---|
| Commerce Core tool | `get_order_context`, `list_fulfillment_options`, `evaluate_recovery_policy`, `execute_recovery_command`, `verify_order_outcome`, `send_synthetic_notification` |
| Domain Knowledge Gateway tool | `search_evidence`, `traverse_relations`, `read_exact_section`, `get_release_info` |

These external names are frozen for the first slice; Go method names may follow
Go casing conventions. Their behavior and payloads are the contracts in this
section.
Health, readiness, seed, event intake, task status, HITL command, and other
operator endpoints are internal HTTP surfaces and are not advertised to the
agent planner as business capabilities.

Both tool constructors use `core.NewTool`; neither receives a Discovery client
for outbound orchestration. The Order Operations Agent uses
`core.NewBaseAgent`, discovers the two tools at runtime, and never imports their
handlers or storage packages. Its inbound and orchestration endpoints remain
`Internal: true` to prevent recursive self-calls.

## 7. Domain-Pack Contract

### 7.1 Required source mixture

The `commerce-us` release contains:

- a redistribution-approved snapshot of
  [16 CFR Part 435](https://www.ecfr.gov/current/title-16/chapter-I/subchapter-D/part-435);
- a redistribution-approved snapshot of
  [49 CFR 173.185](https://www.ecfr.gov/current/title-49/subtitle-B/chapter-I/subchapter-C/part-173/subpart-E/section-173.185);
- approved FTC guidance or a reference-only source record when full bundling is
  not approved;
- synthetic internal fulfillment, carrier-routing, compensation, and autonomy
  policies that are visibly labelled as synthetic;
- at least 50 reviewed evaluation cases.

No runtime network fetch is required to serve the release. Every bundled source
must have documented redistribution status. Publicly readable but unapproved
copyrighted material remains reference-only.

### 7.2 Manifest requirements

The release manifest pins:

- schema and release versions;
- domain and jurisdictions;
- acquisition timestamp and source URI for every source;
- source authority and redistribution classifications;
- document, section, entity, and relationship counts;
- source and generated-artifact hashes;
- effective-date catalog;
- embedding provider, model, dimensions, and normalization behavior;
- full-text, vector, and graph projection generations;
- compiler version;
- review and evaluation status;
- the minimum compatible gateway contract version.

The loader verifies all declared hashes before writing anything. Loading is
idempotent by release ID and uses a staging marker so the gateway never serves a
partially loaded release. Activation occurs only after counts, hashes, indexes,
and conformance checks pass.

### 7.3 Physical graph model

Stable provenance nodes and edges are provider mappings, not framework types:

```text
(Release)-[:HAS_SOURCE]->(Source)
(Source)-[:HAS_DOCUMENT]->(Document)
(Document)-[:HAS_SECTION]->(Section)
(Assertion)-[:SUPPORTED_BY]->(Section)
(Assertion)-[:ABOUT]->(Entity)
```

Domain semantic relationships use a stable physical relationship with a typed
predicate property:

```text
(Entity)-[:RELATION {
  predicate,
  release_id,
  verification_status,
  provenance_id,
  supporting_evidence_ids
}]->(Entity)
```

This allows commerce predicates such as `CONTAINS_BATTERY` and
`RESTRICTS_ROUTE`, or IT-operations predicates such as `DEPENDS_ON`, without
changing the gateway binary or introducing domain types into TruvaG3.

### 7.4 Minimal storage seam and backend swap

The provider boundary is private to the Domain Knowledge Gateway. It is not a
TruvaG3 framework API and must not grow into a general database abstraction.
The planned Go interface has only the primitives needed by the retrieval
pipeline:

```go
type KnowledgeStore interface {
	GetReleaseInfo(ctx context.Context, releaseID string) (ReleaseInfo, error)
	LexicalSearch(ctx context.Context, spec CandidateSpec) ([]Candidate, error)
	VectorSearch(ctx context.Context, spec VectorSpec) ([]Candidate, error)
	Traverse(ctx context.Context, spec TraversalSpec) ([]EvidencePath, error)
	ReadExact(ctx context.Context, ref SectionRef) (ExactSection, error)
}
```

The interface returns stable release, document, section, entity, assertion, and
path identities from the canonical pack. It never returns a provider node ID,
Cypher record, driver type, or provider-specific score in a public result.
Provider candidates are ranked inputs; the gateway, not the adapter, owns
applicability checks, reciprocal-rank fusion, result and token bounds,
verification, source precedence, conflict detection, and sufficiency.

Release installation is a provider-specific command behind the same gateway
image boundary, not another agent-facing interface. It consumes the validated
canonical JSONL records and builds a disposable projection. `ladybug/` owns its
Cypher, schema, indexes, extension loading, native handles, and database path. A
future `neo4j/` package owns the corresponding driver, constraints, indexes,
Cypher, batching, authentication, and connection lifecycle.

A backend swap is always a rebuild, never a database-to-database conversion:

1. Build the target projection from the unchanged domain pack.
2. Run the shared store-conformance suite and the unchanged retrieval gold set.
3. Deploy the gateway with the target provider configuration and any
   provider-specific Kubernetes resources.
4. Switch traffic only after the release identity and evaluation report match;
   rollback by restoring the previous gateway configuration.

For Neo4j, only a new `internal/store/neo4j/` adapter, its release projector,
provider configuration, and the Neo4j Kubernetes resources are expected to be
new. The agent, skills, capability payloads, retrieval orchestration, canonical
packs, policy code, and scenario fixtures must remain byte-for-byte compatible
unless an independently justified application requirement changes them.

## 8. Retrieval and Decision Pipeline

For a routine recovery decision, the gateway performs:

1. Validate contract version, release state, bounds, and embedding
   compatibility.
2. Filter candidates by active release, authority class, jurisdiction,
   lifecycle, and effective date.
3. Retrieve the top 20 full-text candidates.
4. Retrieve the top 20 vector candidates.
5. Fuse ranked candidates using reciprocal-rank fusion with constant 60.
6. Select no more than five verified entity or assertion seeds.
7. Traverse no more than two allowed semantic hops and retain no more than five
   paths.
8. Resolve the resulting assertions and paths to exact source sections.
9. Apply source precedence, conflict, integrity, and sufficiency checks.
10. Return no more than five exact evidence sections within the 2,500-token
    content budget.

The agent then:

1. Combines retrieved reference evidence with current Commerce Core facts.
2. Compares only alternatives returned by Commerce Core.
3. Requests a deterministic policy decision for one exact order version and
   option.
4. Executes only an `allowed` decision or a still-valid approved decision.
5. Rereads and verifies order, inventory, fulfillment, payment, and delivery
   state.
6. Communicates only the verified outcome.

The agent must stop or escalate when required evidence is insufficient,
conflicting, unverifiable, unavailable, or outside the requested release and
effective-date boundary.

## 9. Delivery Milestones

Milestones are sequential except that Milestones 2 and 3 may proceed in
parallel after Milestone 1 freezes their shared contracts. A milestone is not
complete until its exit criteria pass.

### Milestone 0: Repository and provider validation

Deliverables:

- Confirm the actual Go workspace and setup-helper conventions.
- Record the pre-implementation status of `truva-g3/examples/my-async-agent/`,
  `truva-g3/examples/my-streaming-agent/`, and `truva-g3/examples/my-tool/`, then verify that this
  work introduces no changes under those template directories.
- Record the file-by-file adaptation map from `my-async-agent` and `my-tool`,
  including every intentional deviation from those scaffolds.
- Run a time-boxed LadybugDB compatibility spike and pin the database, official
  Go binding, native-library artifacts, and required extension artifacts.
- Build and start the gateway image on every Kind target architecture supported
  by the example; prove the CGO/native-library packaging without runtime
  downloads.
- Run an embedding-provider spike and pin model, dimensions, normalization, and
  query configuration.
- Confirm SQLite persistence through a Commerce Core pod restart.
- Confirm LadybugDB full-text search, vector search, bounded graph traversal,
  exact reads, deterministic pack rebuild, read-only serving, and pod-restart
  behavior using representative pack data.
- Record gateway startup time and steady-state memory for the representative
  pack so the lightweight choice is based on measurements.
- Confirm that the existing Redis task, HITL, skills, and Registry Viewer paths
  can be reused without framework changes.

Exit criteria:

- A short decision record captures every pinned environment-dependent value.
- LadybugDB is accepted only if the gateway image builds reproducibly, contains
  every runtime artifact, and passes the required retrieval operations on the
  supported Kind architecture. If it fails, use Neo4j Community behind the
  unchanged `KnowledgeStore` boundary and record the failed criterion.
- No framework change is required for the initial slice, or an explicit design
  review approves the smallest necessary change before application coding.

### Milestone 1: Contract and scenario freeze

Deliverables:

- Versioned Go types for events, commerce facts, knowledge requests/results,
  policy decisions, commands, and scenario expectations.
- Frozen capability names, descriptions, input summaries, output summaries,
  HTTP error semantics, and visibility (`Internal`) settings.
- JSON fixtures for all scenarios in Section 3.
- Domain-pack manifest schema and validator.
- State-transition table and source-precedence table.
- Exact evidence-sufficiency rules for each scenario.

Exit criteria:

- Contract serialization and compatibility tests pass.
- Each scenario has one unambiguous expected terminal result.
- The scenario runner can validate deterministic fixtures without an LLM.

### Milestone 2: Deterministic Commerce Core

Deliverables:

- SQLite schema and migrations.
- Products, classifications, warehouses, inventory, orders, fulfillment
  alternatives, sandbox payments, and notifications.
- Optimistic concurrency and idempotent command ledger.
- Deterministic policy rules linked to evidence identities.
- The six Commerce Core capabilities in Section 6.7 plus internal health,
  readiness, and seed endpoints.

Exit criteria:

- Unit and integration tests cover every valid and invalid transition.
- Commerce Core is a passive `BaseTool` and cannot discover or invoke the
  gateway or agent.
- Duplicate commands return `already_applied` without another mutation.
- Stale versions, invalid options, expired decisions, and invalid approvals are
  rejected.
- `NORMAL_ORDER` succeeds with the AI provider disabled.
- Commerce state survives a Commerce Core container restart.

### Milestone 3: Immutable knowledge release

Deliverables:

- Approved source snapshots and acquisition records.
- Synthetic internal policies.
- Normalized sections with stable exact-read identities.
- Embeddings pinned to the manifest profile.
- Reviewed entities, assertions, and relationships.
- At least 50 gold retrieval and decision cases.
- Pack validator and reproducible build instructions.

Exit criteria:

- All source and artifact hashes validate.
- Every demonstrated relationship and operationalized regulatory rule has an
  exact supporting evidence reference.
- No unapproved copyrighted full text is included.
- Rebuilding from the approved inputs produces the declared logical artifacts
  or documents every intentionally nondeterministic field.

### Milestone 4: LadybugDB projection and Domain Knowledge Gateway

Deliverables:

- Gateway image containing the pinned LadybugDB native library and required
  full-text and vector extension artifacts.
- Default `KnowledgeStore` Ladybug adapter with no database-specific type above
  `internal/store/ladybug/`.
- Init-container release projector, shared `emptyDir`, and atomic activation
  marker.
- Full-text and vector indexes.
- Bounded `search_evidence`, `traverse_relations`, `read_exact_section`, and
  `get_release_info` capabilities.
- Structured missing, conflicting, integrity-failed, and unavailable outcomes.
- Provider-independent store-conformance suite and a Neo4j mapping/swap runbook;
  the first slice does not need to implement the Neo4j adapter.

Exit criteria:

- A cold deployment loads and activates the expected release.
- A repeated load does not duplicate nodes or relationships.
- Exact reads reproduce the manifest hash.
- Traversal never exceeds configured path or depth bounds.
- No LadybugDB query, native identifier, handle, or error type crosses the store
  boundary.
- The gateway is a passive `BaseTool`; its registered descriptions and schema
  summaries exactly match the Section 6 contracts.
- Gateway integration tests pass with the LLM disabled.
- The evaluation harness reports lexical-only, hybrid, and
  hybrid-plus-graph results separately.

### Milestone 5: Order Operations Agent

Deliverables:

- Embedded API-and-worker agent based on the existing event-driven pattern.
- One `core.Runnable` runtime registered through `framework.RegisterRunnable`
  in combined deployment mode; it initializes the orchestrator from
  framework-owned Discovery before starting the queue consumer.
- Redis task queue, task store, deduplication, and HITL checkpoint/resume.
- One Git-owned and explicitly bound order-recovery skill.
- Dynamic discovery of Commerce Core and Domain Knowledge Gateway
  capabilities.
- Evidence sufficiency, policy request, command, verification, and notification
  orchestration.

Exit criteria:

- `AUTO_RECOVER`, `DENIED_OPTION`, and `INSUFFICIENT_EVIDENCE` produce their
  expected outcomes.
- Skill instructions contain procedure but no copied regulatory corpus.
- Agent code does not write Commerce Core or knowledge storage directly.
- The schema-backed inbound webhook and health capability are `Internal: true`
  and therefore excluded from LLM tool catalogs; task, approval, and
  orchestration control endpoints remain infrastructure handlers, preventing
  recursive self-calls.
- A model timeout cannot corrupt transaction state.

### Milestone 6: HITL and stale-basis protection

Deliverables:

- Approval payload containing order version, option, evidence, customer impact,
  cost, policy decision, and expiry.
- Resume flow that rereads live state, active release, and current policy.
- Replanning and renewed approval when the basis changes.

Exit criteria:

- `APPROVAL_REQUIRED`, `POLICY_CHANGED_DURING_HITL`, and
  `STATE_CHANGED_DURING_HITL` pass.
- Rejection and expiration do not execute a business command.
- No approval authorizes a different option, order version, policy version, or
  materially different customer impact.

### Milestone 7: Observability and operator experience

Deliverables:

- Correlated trace from event through retrieval, policy, command, and
  verification.
- Registry Viewer evidence for skill versions, release, source sections,
  relationship paths, decisions, approval, commands, and final checks.
- Bounded metrics for scenario outcomes and retrieval failures.
- Sanitized logs without complete documents or sensitive customer data.

Exit criteria:

- An operator can explain every default demonstration outcome from Registry
  Viewer and Jaeger without reading pod-local files.
- Trace and log tests prove that complete documents, API keys, and customer
  records are not emitted.

### Milestone 8: Domain portability and release proof

Deliverables:

- Small `it-ops-mini` conformance pack using the same release and physical graph
  schema.
- Gateway conformance command that selects the alternate release without a
  binary rebuild.
- One-command evaluation report and one-command default demonstration.
- Complete README and architecture documentation.

Exit criteria:

- The same gateway image loads and queries both packs.
- No commerce entity, predicate, or policy type exists in TruvaG3 framework
  modules or the generic gateway contract.
- A fresh framework bootstrap followed by this example's `deploy`, `test`, and
  `eval` passes the release gates.

## 10. Verification and Evaluation

### 10.1 Test layers

| Layer | Required coverage |
|---|---|
| Unit | State transitions, policy rules, hashes, filtering, fusion, path bounds, sufficiency, command idempotency |
| Contract | JSON compatibility, required fields, unknown versions, error outcomes |
| Capability schema | Registered descriptions, `InputSummary`, `OutputSummary`, visibility, and JSON schema match the frozen contracts |
| Integration | SQLite persistence, Ladybug projection/index/query, gateway restart, Redis task and HITL lifecycle |
| Store conformance | Every adapter passes the same release, lexical, vector, traversal, exact-read, filtering, and failure-semantic cases |
| Scenario | All fixtures in Section 3 with exact terminal outcomes |
| Retrieval evaluation | At least 50 gold cases across lexical, hybrid, and hybrid-plus-graph modes |
| End to end | Event to verified state, including approval, retry, conflict, and abstention |
| Portability | Same gateway binary with `commerce-us` and `it-ops-mini` |

Every Go change must pass the repository's complete gate set: `go vet`,
`go build ./...`, `go test ./...`, `goimports`, `golangci-lint run`, `gosec`,
and `govulncheck`. A partial gate pass does not complete a milestone.

### 10.2 Knowledge evaluation coverage

The gold set includes:

- exact policy names and regulatory identifiers;
- paraphrases and indirect business language;
- SKU, product-class, warehouse, and carrier aliases;
- effective-date and jurisdiction selection;
- superseded sections;
- near-duplicate hard negatives;
- relationship-dependent questions;
- conflicting-source cases;
- unanswerable questions;
- retrieval-to-decision cases with an expected deterministic outcome.

### 10.3 Release thresholds

| Metric | Required threshold |
|---|---:|
| Exact-section recall at 5 | At least 90% |
| Correct jurisdiction and effective version | 100% |
| Correct reviewed path for graph-dependent cases | At least 95% |
| No-answer and abstention accuracy | At least 90% |
| Citation faithfulness for executed decisions | 100% |
| Fixed-scenario final decision accuracy | 100% |
| Unsafe action in denied, conflicting, or insufficient cases | 0 |
| Unverified edge used as authoritative evidence | 0 |
| Duplicate business mutation under replay | 0 |

Thresholds are release gates, not claims of production fitness. Evaluation
results must include per-case failures and an ablation comparison so a passing
aggregate cannot hide a safety-critical miss.

### 10.4 Required adversarial checks

- Retrieved text attempts to instruct the agent to ignore policy.
- A source hash differs from the active manifest.
- Query embedding dimensions differ from the release.
- A graph path contains an unverified or provenance-free edge.
- The requested effective date has no applicable policy.
- A newer document exists but is not yet effective.
- An approval references an expired decision.
- A capability disappears between planning and execution.
- Commerce Core applies a command but the response is lost and retried.

## 11. Setup and Demonstration Interface

The example's `setup.sh help` must document its real verbs. Planned verbs are:

| Command | Required behavior |
|---|---|
| `./setup.sh deploy` | Build and deploy the example into an existing cluster |
| `./setup.sh rebuild` | Rebuild all changed solution images without cache and redeploy |
| `./setup.sh rollout` | Refresh configuration and restart workloads without rebuilding code |
| `./setup.sh skills-sync` | Strictly reconcile Git-owned skill packages through the Skills API |
| `./setup.sh skills-check` | Strictly verify Git and runtime skill-package equality |
| `./setup.sh seed` | Reset and seed deterministic demo business data after explicit confirmation |
| `./setup.sh demo` | Run the default live scenario sequence and print execution IDs |
| `./setup.sh test` | Run deterministic integration and end-to-end checks |
| `./setup.sh eval` | Run the retrieval and decision gold set and write the evaluation report |
| `./setup.sh domain-conformance` | Load and test `it-ops-mini` through the same gateway image |
| `./setup.sh status` | Show solution and dependency health |
| `./setup.sh logs` | Stream the principal solution workload logs |
| `./setup.sh help` | Print commands without changing cluster state |

The framework repository owns cluster creation and shared infrastructure. This
implementation uses its shared setup helper for image loading, namespace
management, manifest application, rollout waits, and skill synchronization. It
must not duplicate infrastructure installation or hand-roll parallel behavior
already provided by the framework repository.

Planned application configuration includes:

- one of the supported AI-provider credentials for agent orchestration;
- a generated, Kubernetes-Secret-backed `CONTROL_PROOF_HMAC_KEY` used only
  for demo-internal exact-read receipts and checkpoint-bound approval proof;
- `EMBEDDING_BASE_URL`, `EMBEDDING_API_KEY`, `EMBEDDING_MODEL`, and
  `EMBEDDING_DIMENSIONS` for the release-compatible query embedding;
- `KNOWLEDGE_RELEASE_ID` for the active immutable release;
- `KNOWLEDGE_STORE_PROVIDER=ladybug` and an internal LadybugDB projection path;
- an internal SQLite path mounted on the Commerce Core persistent volume.

LadybugDB requires no database credentials or service endpoint. When the
Neo4j adapter is later implemented, only its gateway configuration and
Kubernetes Secret add Neo4j connection values; these must not appear in agent,
skill, domain-pack, or public capability configuration.

The final variable names must be frozen in Milestone 0 and documented in
`.env.example`. Secrets are never committed or included in the domain pack.

The initial `deploy` may initialize an empty Commerce Core database once.
Repeated `demo` runs use unique scenario-run and business identifiers and do
not reset existing state.

`seed` is the only command allowed to reset business fixtures. It must name the
target namespace and dataset, reject production-like namespaces, and require an
explicit confirmation flag in non-interactive use.

## 12. Definition of Done

The implementation is complete only when all of the following are true:

- The example is self-contained and follows the examples portability contract.
- The agent and tools are implemented by adapting `my-async-agent` and
  `my-tool`; every scaffold deviation is documented and tested.
- The implementation introduces no changes under `truva-g3/examples/my-async-agent/`,
  `truva-g3/examples/my-streaming-agent/`, or `truva-g3/examples/my-tool/`; all customization is
  confined to the copied files under `agentic-commerce/`.
- After the sibling framework workspace is bootstrapped, a clean `deploy`
  requires no manual Kubernetes or Skills API operation.
- Only three new long-running solution workloads are deployed.
- Exactly one workload is a `BaseAgent` and exactly two are passive `BaseTool`
  services; a registered capability does not create another service.
- `NORMAL_ORDER` completes with the AI provider disabled.
- All exception scenarios reach their exact expected terminal states.
- All release thresholds in Section 10.3 pass.
- Every executed exception decision retains exact evidence and deterministic
  policy references.
- No complete domain corpus is stored in skills, prompts, traces, or execution
  records.
- No unverified relationship supports an authoritative decision.
- Duplicate events and commands do not duplicate business mutations.
- HITL resume revalidates live facts, evidence release, and current policy.
- Registry Viewer and Jaeger expose the bounded decision trail.
- The same gateway image passes the non-commerce conformance test.
- The Ladybug adapter passes the provider-independent store-conformance suite,
  and the documented Neo4j swap changes only the adapter, projection loader,
  provider configuration, and Kubernetes resources.
- Agent, gateway, and Commerce Core unit and integration tests pass.
- Tool errors use non-2xx HTTP status codes and structured framework errors;
  advertised input and output summaries match their response structs.
- All Go pre-commit gates required by the repository pass for every changed Go
  module.
- Documentation distinguishes real regulation, official guidance, approved
  sources, synthetic internal policies, and demo-only operational rules.

Passing the demo does not establish production readiness, legal compliance, or
fitness for real payment, customer, carrier, or hazardous-material operations.

## 13. Explicitly Deferred Work

The first slice does not include:

- online source acquisition or scheduled re-ingestion;
- document parsing or OCR in the cluster;
- LLM graph extraction or entity resolution at runtime;
- a review and publication UI;
- a separate vector database or search cluster;
- global GraphRAG or corpus-wide community summarization;
- model reranking, contextual retrieval, or corrective retrieval;
- multiple Commerce Core replicas or a production transaction database;
- split API and worker deployments;
- a separate policy pod;
- a separate Inventory or Customer Recovery Agent;
- real payment, email, SMS, customer, carrier, or warehouse integrations;
- automatic interpretation of new regulation as executable policy;
- a generic TruvaG3 RAG or knowledge-graph framework abstraction;
- a Neo4j adapter and Neo4j server deployment; their seam, projection mapping,
  conformance target, and swap procedure are designed now, while implementation
  is deferred until a server-based backend is actually needed;
- production security, identity, tenancy, disaster recovery, or compliance
  certification.

## 14. Research and Requirement Traceability

| Decision or requirement | Source of rationale | Implementation proof |
|---|---|---|
| Preserve source text beside graph structure | Foundry research on Microsoft and AWS GraphRAG dataflows | Exact-section reads and citation-faithfulness evaluation |
| Keep graph edges reviewed and provenance-linked | Foundry graph-construction and Neo4j builder research | Projection validation and zero unverified authoritative paths |
| Use hybrid rather than lexical-only retrieval | Control Tower retrieval decision | Lexical, hybrid, and hybrid-plus-graph ablation report |
| Keep graph storage provider-neutral without adding framework complexity | Foundry provider boundary and minimal-demo requirement | Five-method gateway-private store interface, stable pack IDs, shared conformance suite, and no provider types above the adapter |
| Keep the Kind footprint small | LadybugDB embedded architecture and official Go binding | Three solution workloads and measured build, startup, memory, and restart spike |
| Keep Neo4j migration bounded | Canonical-pack and disposable-projection decision | Rebuild from the same pack, pass unchanged conformance and gold evaluations, then switch gateway configuration |
| Use real external policy evidence | Control Tower domain-pack decision | CFR source records and hashes in the active release |
| Keep confidential operating rules synthetic or approved | Source-governance and redistribution research | Manifest authority and redistribution classifications |
| Keep live facts out of the knowledge graph | Foundational architecture boundary | Agent reads alternatives and state only from Commerce Core |
| Keep enforcement deterministic | Control Tower policy boundary | Policy unit tests and zero LLM-authorized mutations |
| Reuse event and HITL patterns | Existing `event-driven-agent` and `agent-with-human-approval` examples | Redis queue, task, checkpoint, command, and resume integration tests |
| Bootstrap framework wiring from maintained templates | Agent and Tool Development Guides plus `my-async-agent`, `my-streaming-agent`, and `my-tool` scaffold review | One async `BaseAgent`, two passive `BaseTool` services, retained setup/telemetry/probe patterns, and documented deviations |
| Keep skills procedural and Git-owned | Examples portability and skills rules | Git package, explicit binding, `skills-sync`, and `skills-check` |
| Prove domain portability | Domain-pack architecture | Same gateway image loads `it-ops-mini` |

The research sections in the two blueprints remain authoritative. This plan
selects a narrow implementation profile for the local capability demonstration
without deleting the evaluated alternatives or presenting the local choices as
production defaults.
