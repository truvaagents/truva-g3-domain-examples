# Agentic Commerce Control Tower

Status: Solution blueprint with a runnable minimal capability slice at
[`agentic-commerce/`](../agentic-commerce/). The example demonstrates
the core boundaries but does not yet satisfy every production-shaped acceptance
criterion in this blueprint.

Readers who want the implemented concept, components, scenario progression,
and captured live evidence explained before the full blueprint should start
with the [illustrated beginner tutorial](../agentic-commerce/docs/tutorial/index.html).
The [Markdown tutorial series](../agentic-commerce/docs/tutorial/README.md)
provides a more detailed reference.

## Table of Contents

- [1. Purpose](#1-purpose)
- [2. Executive Summary](#2-executive-summary)
- [3. Business Outcomes](#3-business-outcomes)
- [4. Foundational Design Boundary](#4-foundational-design-boundary)
- [5. Scope and Non-Goals](#5-scope-and-non-goals)
- [6. What Real Time Means](#6-what-real-time-means)
- [7. Proposed Architecture](#7-proposed-architecture)
- [8. Business Event Model](#8-business-event-model)
- [9. Agent Responsibilities](#9-agent-responsibilities)
- [10. Deterministic Services and Tools](#10-deterministic-services-and-tools)
- [11. Skills](#11-skills)
- [12. Domain Knowledge, Retrieval, and Policy Enforcement](#12-domain-knowledge-retrieval-and-policy-enforcement)
- [13. Autonomy and Approval](#13-autonomy-and-approval)
- [14. Primary Workflow](#14-primary-workflow)
- [15. Transaction and Concurrency Safety](#15-transaction-and-concurrency-safety)
- [16. Failure and Recovery](#16-failure-and-recovery)
- [17. Observability and Auditability](#17-observability-and-auditability)
- [18. Local Demonstration](#18-local-demonstration)
- [19. Implementation Phases](#19-implementation-phases)
- [20. Acceptance Criteria](#20-acceptance-criteria)
- [21. Later Extensions](#21-later-extensions)
- [22. Open Design Decisions](#22-open-design-decisions)
- [23. Related Industry Direction](#23-related-industry-direction)

## 1. Purpose

This blueprint describes an e-commerce application in which agents are part of
the software that operates the business. The agents do not only answer questions
or advise employees. They receive business events, pursue defined outcomes,
coordinate capabilities, request approval when needed, execute permitted
actions, and verify the result.

The first scenario focuses on autonomous order fulfillment and recovery. It is
small enough to reproduce in a local Kind cluster but broad enough to
demonstrate a realistic enterprise workflow.

The first implementation is intentionally a capability demonstration rather
than a production topology. It minimizes infrastructure while retaining
production-shaped evidence, provenance, evaluation, deterministic policy
enforcement, transaction consistency, and failure behavior. Simplifications
should reduce operational complexity without removing the controls needed for
credible decisions.

This document remains the intended solution and research record. It does not
define a new TruvaG3 framework contract. The linked runnable slice deliberately
labels which broader ingestion, persistence, evaluation, security, and
production-operability requirements remain outside the capability demo.

The [runnable example](../agentic-commerce/), its
[capability contracts](../agentic-commerce/contracts/tool-capabilities.md), and
[scenario assertions](../agentic-commerce/scenarios/expected-results.json) are
authoritative for the implemented scope.

## 2. Executive Summary

The proposed system is an **Agentic Commerce Control Tower**. Its agents operate
the adaptive parts of the order lifecycle:

- Recover an order when inventory becomes unavailable.
- Select a fulfillment alternative that preserves the delivery promise.
- Decide whether added cost is inside the agent's authority.
- Ask a person to approve an exceptional financial or customer-impacting action.
- Inform the customer using confirmed transaction state.
- Verify that inventory, payment, fulfillment, and delivery state agree.
- Learn from completed exceptions and use that knowledge in later executions.

The agents are the active business workflow layer. Deterministic commerce
services remain the systems of record for products, prices, orders, inventory,
payments, and shipments.

```text
              Business and customer events
                          |
                          v
       Domain agents <----> Domain knowledge
        goals + skills      bounded, versioned evidence
               |
               v
              Policy and approval boundary
               |
               v
             Idempotent business commands
               |
               v
           Deterministic transaction services
               |
               v
                 New business events
```

This makes the system agentic without asking an LLM to provide database,
ledger, or payment guarantees.

The reusable architecture is domain agnostic at the framework, gateway,
evidence, provenance, retrieval, policy-decision, and execution boundaries.
Commerce knowledge, ontology, tools, policies, and skills remain replaceable
domain assets rather than framework assumptions. Domain agnosticism therefore
means that another domain can reuse the same runtime contracts and load a
different domain pack; it does not mean that accurate domain semantics require
no domain-specific modeling.

## 3. Business Outcomes

The example should demonstrate outcomes that a business can measure:

- More orders recovered without manual intervention
- Fewer missed delivery commitments
- Lower time to resolve order exceptions
- Lower support workload
- Controlled fulfillment and compensation costs
- Faster customer notification
- Complete evidence for every autonomous or approved action

The demonstration should report both customer benefit and operational cost. An
order is not successfully recovered merely because a technical step completed.
The delivery promise, financial effect, customer communication, and final
transaction state must all be verified.

## 4. Foundational Design Boundary

### 4.1 Agents own outcomes and adaptive decisions

An agent can decide:

- Which evidence is needed for an exception
- Which valid fulfillment alternatives should be compared
- Whether a proposed action meets business policy
- Whether a person must approve an action
- Which specialist agent or discovered capability can help
- How to communicate a confirmed outcome
- Whether recovery verification is sufficient

### 4.2 Transaction services own facts and invariants

An agent cannot declare inventory reserved, a payment authorized, or an order
completed. Only the authoritative service can make those state changes.

Deterministic services must enforce:

- Valid order-state transitions
- Inventory availability and reservation
- Price, tax, and currency calculations
- Payment authorization and settlement state
- Idempotency and duplicate suppression
- Optimistic concurrency or another explicit consistency policy
- Actor authority and applicable business limits

### 4.3 Agents issue commands, not database writes

Agents use discovered capabilities to issue structured commands. They never
write directly to a transaction database. A command can be rejected because the
business state changed, even when the command was reasonable when planned.

The agent must treat that rejection as new evidence, read authoritative state
again, and decide whether to replan, request approval, or stop.

### 4.4 Knowledge services own reference evidence

The separate Enterprise Knowledge Foundry prepares and publishes large manuals,
contracts, policies, and other searchable reference material. The
application-level Domain Knowledge Gateway serves bounded, versioned evidence
from a published release; it does not turn retrieved prose into instructions or
transaction authority. Skills continue to supply procedural guidance, and
deterministic services continue to enforce business invariants.

## 5. Scope and Non-Goals

### 5.1 Initial scope

The first runnable solution should cover one end-to-end exception:

1. A customer places a next-day order containing a lithium-ion battery.
2. Inventory is initially assigned to a preferred warehouse.
3. An inventory shortfall or warehouse disruption occurs.
4. An agent evaluates recovery alternatives using current commerce facts and
   versioned domain evidence.
5. Deterministic policy rejects ineligible transport or fulfillment options.
6. The system executes an autonomous or human-approved recovery.
7. A notification capability communicates the confirmed outcome.
8. Independent checks verify the new business state.

### 5.2 Non-goals for the first release

- Replacing a complete commercial e-commerce platform
- Processing real payment credentials or moving real money
- Letting an LLM calculate authoritative prices, taxes, or balances
- Putting model calls in every catalog, cart, or payment request
- Storing complete domain manuals, contracts, or regulatory corpora in skills
- Turning the Skills registry into a general RAG or vector-search subsystem
- Treating retrieved prose or an LLM decision as transaction authority
- Building a general workflow language
- Implementing every commerce agent described in this blueprint
- Coupling TruvaG3 framework modules to a commerce provider or protocol
- Treating model interpretation of regulatory text as legal advice or as the
  mechanism that establishes compliance

The initial implementation should use sandbox or simulated payment behavior and
synthetic customer data.

## 6. What Real Time Means

This is a business real-time system: it continuously reacts to events in seconds
or minutes. It is not a hard real-time control system.

| Decision | Target | Owner |
|---|---:|---|
| Read a price or inventory balance | Under 100 ms | Deterministic service |
| Reserve inventory | Under 100 ms | Transaction service |
| Authorize a payment | Under 1 second | Payment and risk services |
| Select a previously approved offer | Under 500 ms | Rules or precomputed result |
| Classify an order exception | Seconds | Fast agent/model path |
| Reroute or split an order | Seconds | Agent execution |
| Resolve a delivery disruption | Seconds to minutes | Multi-agent execution |
| Rebalance inventory or replenish stock | Minutes | Event-driven or scheduled agent |

Normal commerce requests must continue when an AI provider is slow or
unavailable. The transaction layer should either complete its deterministic
path or place the exception into durable work for later agent processing.

## 7. Proposed Architecture

```text
 Customer UI             Operations UI            Event simulator
     |                         |                         |
     +-------------------------+-------------------------+
                               |
                               v
                     Commerce API / Core
             catalog, cart, order, inventory,
              payment sandbox, fulfillment
                               |
                  authoritative business events
                               |
                               v
                   Durable event/task intake
                               |
              +----------------+----------------+
              |                |                |
              v                v                v
      Order Operations    Inventory Agent   Customer Recovery
          Agent                                    Agent
              |                |                |
              +----------------+----------------+
                               |
                 dynamic capability discovery
                               |
   +---------------+---------------+---------------+---------------+
   |               |               |               |               |
   v               v               v               v               v
Commerce        Domain          Policy          Scheduler      Collaboration
adapters        knowledge       service         tool           tools
   |               |               |                               |
   +---------------+---------------+-------------------------------+
                               |
                               v
                 policy decision and HITL
                               |
                               v
                  idempotent commerce command
                               |
                               v
                      Commerce API / Core

 Cross-cutting: execution records, Registry Viewer, traces, metrics, and logs
```

The agents should be independently deployable Kubernetes workloads. Durable
execution state makes replicas interchangeable; an agent replica must not keep
authoritative order state only in process memory.

The first Kind slice deploys only the Order Operations Agent. Inventory-agent
and Customer Recovery Agent responsibilities remain later decomposition points
until their independent objectives justify additional workloads. The initial
agent can use deterministic inventory, notification, and customer-choice
capabilities without turning each capability into another agent.

### 7.1 Framework capabilities used

- Event-driven execution
- Dynamic agent and tool discovery
- Explicit skill binding and progressive skill loading
- Multi-phase orchestration
- Human-in-the-loop suspension and resume
- Durable execution records
- Scheduled follow-up
- Shared investigation and institutional memory
- AI provider chaining and fallback
- Structured telemetry and distributed tracing
- Registry Viewer execution and skill inspection

## 8. Business Event Model

Business events should use a stable envelope independent of the message broker:

```json
{
  "event_id": "evt-01J...",
  "event_type": "inventory_reservation_failed",
  "aggregate_type": "order",
  "aggregate_id": "ord-1042",
  "aggregate_version": 7,
  "occurred_at": "2026-08-18T18:30:00Z",
  "correlation_id": "checkout-9f82",
  "causation_id": "cmd-01J...",
  "payload": {
    "line_id": "line-2",
    "requested_quantity": 2,
    "unavailable_location": "warehouse-north"
  }
}
```

The envelope must not carry payment credentials, authentication tokens, or
unnecessary personal data.

Initial event types may include:

- `order_placed`
- `inventory_reserved`
- `inventory_reservation_failed`
- `warehouse_disruption_detected`
- `fulfillment_option_changed`
- `shipment_delayed`
- `customer_response_received`
- `approval_resolved`
- `order_recovery_completed`
- `order_recovery_failed`

Consumers must tolerate duplicate delivery. `event_id`, aggregate version, and
command idempotency provide the information needed to do so safely.

## 9. Agent Responsibilities

The first implementation should start with the smallest set of agents that
demonstrates meaningful collaboration.

### 9.1 Order Operations Agent

Business objective:

> Fulfill every valid order while preserving customer commitments and staying
> within approved cost, margin, and authority limits.

Responsibilities:

- Receive order exceptions
- Gather authoritative order, inventory, and fulfillment facts
- Request specialist analysis when useful
- Compare only currently valid recovery options
- Propose structured commands
- Request approval when policy requires it
- Revalidate state before a resumed mutation
- Verify the final order outcome

The agent should expose a business capability such as order-exception
coordination. It should not advertise implementation-specific model or tool
names.

Capabilities intended for agent-to-agent collaboration must be externally
discoverable. Event ingestion and administrative endpoints can remain internal
when they are not valid delegation targets.

### 9.2 Inventory Agent

Business objective:

> Allocate scarce inventory to meet confirmed commitments while controlling
> fulfillment cost and avoiding preventable stockouts.

Responsibilities:

- Evaluate alternative locations
- Detect inventory contention
- Recommend reassignment or replenishment
- Explain trade-offs to the Order Operations Agent
- Recheck availability before reservation

This agent may be deferred if the first Order Operations Agent can evaluate
alternatives through deterministic inventory capabilities. It should be split
out only when its independent objective and event lifecycle add real value.

### 9.3 Customer Recovery Agent

Business objective:

> Preserve customer trust when the original order promise cannot be met.

Responsibilities:

- Use confirmed order state rather than an unexecuted proposal
- Select communication and compensation allowed by policy
- Present choices when customer input is required
- Record the communication outcome
- Escalate exceptional financial or customer-impact decisions

### 9.4 Later agents

- Fulfillment Agent for carrier and route selection
- Merchandising Agent for bounded promotion decisions
- Replenishment Agent for supplier and purchase-order coordination
- Customer Retention Agent for proactive, policy-controlled recovery

Creating many agents is not a goal by itself. A separate agent is justified
when it owns a distinct business objective, authority envelope, lifecycle, or
scaling boundary.

## 10. Deterministic Services and Tools

### 10.1 Commerce Core

The Commerce Core is the system of record. The local implementation may expose
separate capabilities while initially sharing one deployable service.

Required capability groups:

- Product and catalog queries
- Cart creation and inspection
- Order creation and inspection
- Inventory availability, reservation, and release
- Fulfillment-option calculation
- Sandbox payment authorization state
- Delivery-commitment updates
- Bounded refunds or account credits

The storage technology is an application choice, not a TruvaG3 framework
dependency. The runnable example should choose a reproducible default and keep
the business interfaces independent of that choice.

### 10.2 Agent-facing tools

Tools adapt deterministic business APIs into discoverable, typed capabilities.
They must preserve authoritative error and concurrency semantics. A tool must
not turn a rejected transaction command into a successful natural-language
result.

The first version can use one commerce adapter with multiple capabilities. A
later version may split catalog, order, inventory, payment, and fulfillment
adapters when independent ownership or scaling requires it.

A demo customer-notification adapter should provide a safe delivery capability
and a delivery receipt. It must use synthetic addresses and messages. This keeps
customer communication verifiable without requiring a real email, SMS, or
customer-support account.

In TruvaG3 terms, the first slice therefore contains exactly one
`core.BaseAgent` and two passive `core.BaseTool` services:

1. The Order Operations Agent is bootstrapped from `my-async-agent` using the
   combined push-event API-and-worker pattern.
2. Commerce Core is one `BaseTool` with multiple commerce, policy, command,
   verification, and synthetic-notification capabilities.
3. Domain Knowledge Gateway is one `BaseTool` with search, traversal,
   exact-read, and release capabilities.

A registered capability is not another tool service. LadybugDB is embedded
inside the gateway and is not a TruvaG3 component. Inventory Agent, Customer
Recovery Agent, a separate policy tool, and a separate notification tool remain
deferred. The implementation must adapt the maintained `my-*` scaffolds without
depending on those sibling example directories at runtime. Those scaffolds are
read-only templates for this solution: copy them into the new self-contained
example and modify only the copies. The original `truva-g3/examples/my-*` directories
must remain unchanged.

### 10.3 Existing examples that can be reused or adapted

- `event-driven-agent` for event intake and durable processing patterns
- `agent-with-human-approval` for approval and resume patterns
- `scheduler-tool` and `scheduled-executor` for delayed verification
- `agentic-memory-tool` for prior recovery and investigation knowledge
- Currency tools for cross-currency display or comparison
- Slack, Jira, and Confluence tools for operational collaboration
- Registry Viewer and the observability stack for inspection

The runnable solution must own the mock commerce components required for its
core scenario. Optional general tools can be deployed separately and discovered
through their public capabilities, as they are for other agents. Source code
must not import files from sibling examples.

## 11. Skills

Skills express business procedures and policies without hardcoding discovered
tool or capability names.

Candidate skill packages:

- `commerce/order-exception-resolution`
- `commerce/inventory-allocation`
- `commerce/delivery-promise`
- `commerce/customer-compensation`
- `commerce/recovery-verification`
- `commerce/customer-communication`

An order-recovery instruction might say:

> Determine the current order promise and the reason it is at risk. Compare
> feasible alternatives using authoritative inventory, fulfillment, cost, and
> customer-impact evidence. Prefer the least disruptive option that preserves
> the promise and remains inside the configured authority. Require approval
> before exceeding an authority boundary. After execution, independently verify
> order, inventory, payment, and delivery state before reporting success.

It should not say which service or capability name to call. Dynamic discovery
selects from the capabilities available at execution time.

Skills are explicitly bound by the agent developer. Git stores the desired
packages under the agent's standard `skills/packages/<namespace>/` layout, and
setup publishes them through the provider-neutral Skills API.

Skill resources can contain bounded, progressively loaded material such as:

- Delivery promise policy
- Fulfillment cost thresholds
- Compensation matrix
- Order-state interpretation guide
- Verification checklist
- Customer communication patterns

Financial limits and frequently changing operational values should come from
authoritative policy data when possible, rather than being copied into long
instruction bodies.

Skill resources are not a general document corpus. A package can pass its
authoring limits while some content is omitted from an execution because the
active-skill or phase projection budget is smaller. Large manuals, contracts,
regulatory libraries, product data, and historical records belong behind
retrieval capabilities. See the
[Agent Skills Guide](../../truva-g3/docs/orchestration/AGENT_SKILLS_GUIDE.md#authoring-sizes-and-recommendations)
for the authoring and runtime distinction.

## 12. Domain Knowledge, Retrieval, and Policy Enforcement

Large domain knowledge requires an application-level knowledge architecture in
addition to skills. It must not expand the Skills V1 registry into a general
retrieval-augmented generation (RAG) system.

This solution consumes knowledge through a provider-neutral **Domain Knowledge
Gateway service**. The gateway exposes bounded evidence from an immutable,
published knowledge release through discoverable capabilities. It does not make
document ingestion, parsing, entity resolution, expert review, or index
publication part of the commerce control tower.

Those data-preparation responsibilities belong to the independent
[Enterprise Knowledge Foundry](ENTERPRISE_KNOWLEDGE_FOUNDRY.md) workstream. The
foundry can serve commerce, insurance, healthcare, legal, manufacturing, or
other domains without depending on TruvaG3. The only required integration is a
published-release and retrieval contract.

### 12.1 Classify content by responsibility

| Content | Commerce example | Correct owner |
|---|---|---|
| Procedural guidance | How to investigate an inventory failure | Skill |
| Small conditional procedure | Delivery-recovery checklist | Skill resource |
| Large reference corpus | Carrier contracts or operations manuals | Knowledge Foundry for preparation; Domain Knowledge Gateway for serving |
| Live business fact | Current stock, price, order, or shipment | Transaction service |
| Enforceable rule | Maximum autonomous cost or compensation | Policy and transaction services |
| Historical experience | Similar prior recovery and its result | Memory or investigation knowledge |

The distinction is behavioral, not only about size. A short current inventory
value still belongs to the inventory service. A long but stable recovery method
may belong in several focused skill resources. A financial limit that must
always hold belongs in deterministic enforcement even if it is also explained
in a skill.

### 12.2 Separate the knowledge layers

The technologies in this area are often compared as if they were substitutes,
but they solve different problems:

| Layer | Purpose | Candidate mechanisms |
|---|---|---|
| Procedure | Tell an agent how to perform a task | TruvaG3 skills |
| Representation and exchange | Make curated knowledge portable, reviewable, and readable | OKF bundles |
| Storage and indexing | Store documents, terms, vectors, nodes, and relationships | Search index, relational database, vector database, graph database |
| Retrieval | Select the most useful bounded evidence | Exact read, lexical, semantic, hybrid, hierarchical, or graph retrieval |
| Live facts | Return current business state | Commerce APIs and transaction datastore |
| Enforcement | Allow, deny, or require approval | Deterministic policy and transaction services |
| Experience | Retain useful prior outcomes | Agent memory or investigation knowledge |

Newer retrieval techniques improve one or more of these layers; they do not
remove the need to keep their responsibilities separate.

### 12.3 Published knowledge-release dependency

The control tower starts with a published knowledge release. It does not know
whether the foundry created that release from OKF bundles, documents, database
records, or another governed source. It also does not know which parser,
embedding model, graph database, or search product built the release.

The release supplied to the Domain Knowledge Gateway must pin:

- Immutable release identity
- Source and evidence versions
- Effective dates and applicability metadata
- Integrity and provenance records
- Verification and lifecycle state
- Exact-evidence, lexical, semantic, and graph projection generations that are
  safe to use together
- Evaluation and approval status

The separate
[Enterprise Knowledge Foundry blueprint](ENTERPRISE_KNOWLEDGE_FOUNDRY.md)
defines the recommended source, compilation, validation, review, evaluation,
and publication lifecycle. The commerce example consumes a pre-published
`commerce-us` domain pack that conforms to the consumer contract. The pack is
compiled outside the runtime and loaded as one immutable release; the Kind
cluster does not run the foundry pipeline.

### 12.4 Domain packs and source composition

Domain agnosticism applies to the release and runtime contracts, not to domain
semantics. The Domain Knowledge Gateway, release manifest, provenance model,
evidence envelope, retrieval lifecycle, and evaluation harness are reusable.
Each domain supplies its own sources, ontology, relationship vocabulary,
operational policies, tools, and skills.

The initial `commerce-us` pack should combine:

- Authoritative public regulation, including
  [16 CFR Part 435](https://www.ecfr.gov/current/title-16/chapter-I/subchapter-D/part-435)
  for shipping promises, delays, consent, cancellation, and refunds
- Authoritative public regulation, including
  [49 CFR 173.185](https://www.ecfr.gov/current/title-49/subtitle-B/chapter-I/subchapter-C/part-173/subpart-E/section-173.185)
  for lithium-battery transport requirements
- Approved public guidance, including the
  [FTC business guide to the Mail, Internet, or Telephone Order Merchandise Rule](https://www.ftc.gov/business-guidance/resources/business-guide-ftcs-mail-internet-or-telephone-order-merchandise-rule)
- Clearly labelled synthetic or customer-approved internal fulfillment,
  carrier-routing, compensation, and autonomy policies
- At least 50 version-controlled gold evaluation cases covering retrieval and
  final business decisions

Real internal recovery thresholds, carrier contracts, and escalation rules are
normally unavailable for redistribution. Synthetic internal policies are
therefore a deliberate confidentiality boundary, not a substitute for using
real public regulatory evidence. A source must not be bundled merely because
it is publicly readable; the pack manifest records its redistribution status.

Illustrative pack layout:

```text
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
```

Every source record should include authority class, jurisdiction, validity
interval, source URI, acquisition time, content hash, redistribution status,
and verification state. Corpus reporting should use document, section, entity,
relationship, version, hard-negative, and evaluation-case counts rather than a
byte-size target.

A portable base evidence model can use `Source`, `Document`, `Section`,
`Assertion`, and `Entity`, with relationships such as `SUPPORTED_BY`,
`DERIVED_FROM`, `SUPERSEDES`, `APPLIES_TO`, and `CONFLICTS_WITH`. A domain pack
can add commerce relationships without adding commerce types to TruvaG3 or to
the provider-neutral gateway contract.

### 12.5 Graph database and GraphRAG roles

A **knowledge graph** is the entity-and-relationship model. A **graph database**
stores and queries that model. **GraphRAG** is a retrieval method that uses
graph structure, often together with source text and vector search, to assemble
LLM context. GraphRAG does not require a dedicated graph database; for example,
Microsoft GraphRAG stores its generated tables as Parquet by default and writes
embeddings to a configured vector store.

Graph retrieval is valuable in commerce because relevant facts are frequently
connected without using similar language:

```text
Order
  -> CONTAINS -> SKU
  -> PROMISED_BY -> Delivery Commitment

SKU
  -> CONTAINS -> Lithium-ion Battery
  -> FULFILLABLE_FROM -> Warehouse

Lithium-ion Battery
  -> GOVERNED_BY -> 49 CFR 173.185

Delayed Shipment
  -> GOVERNED_BY -> 16 CFR Part 435

Recovery Option
  -> USES -> Carrier Service
  -> REQUIRES_APPROVAL -> Internal Cost Policy
```

It is a strong fit for supply-chain impact, warehouse and carrier alternatives,
product substitution, contract applicability, jurisdiction, fraud patterns,
and explanations that depend on a multi-hop relationship path.

The solution should maintain two clearly identified relationship classes:

- **Verified domain relationships** come from transaction schemas, reviewed
  release metadata, or deterministic compilation rules. They can support
  business evidence.
- **AI-derived relationships** come from document extraction. They are
  advisory retrieval hints until a deterministic process or a person verifies
  them.

Every returned graph path must retain source concept, section, version, hash,
and relationship provenance. An inferred edge must never silently become a
business rule. The source text remains available for exact reading; the graph
is not a substitute for citations.

The graph must also not become the authority for current order, inventory,
payment, price, or shipment state. It may contain a timestamped read projection
for investigation, but the agent must read and mutate live state through the
Commerce Core.

### 12.6 Recommended runtime knowledge architecture

```text
          ENTERPRISE KNOWLEDGE FOUNDRY
 source intake, validation, review, evaluation, publication
                         |
                         v
          Immutable published domain pack
   exact evidence + lexical + semantic + typed graph projections
                         |
                         v
              DOMAIN KNOWLEDGE GATEWAY
                         |
       +-----------------+------------------+
       |                 |                  |
 search evidence   traverse relations   read exact section
       +-----------------+------------------+
                         |
              Discoverable capabilities
                         |
                Agent and active skills
                         |
             +-----------+------------+
             |                        |
      Commerce Core          Deterministic policy
      current facts          allow / deny / approval
```

The foundry owns ingestion and publication. The gateway owns runtime retrieval
and bounded evidence shaping. Neither owns agent procedures, live commerce
transactions, or policy enforcement. For the local demonstration, the
independently deployed gateway embeds LadybugDB in its own process for graph,
full-text, and vector retrieval. LadybugDB is not embedded in the agent and does
not add a separate database pod. The same application contract can instead be
backed by Neo4j, PostgreSQL, Amazon Neptune, Spanner Graph, an enterprise search
system, or another implementation.

### 12.7 Provider-neutral domain knowledge capabilities

The application should expose three bounded behavior-level operations as
discoverable capabilities:

- Search for evidence using exact, lexical, semantic, hybrid, or automatic
  retrieval.
- Traverse related concepts through a bounded, typed relationship path.
- Read an exact concept version and section.

The agent and its skills must not depend on which storage or retrieval provider
implements these operations.

Illustrative search input:

```json
{
  "query": "warehouse reassignment for a next-day order",
  "retrieval_modes": ["lexical", "semantic", "graph"],
  "domains": ["fulfillment", "delivery-promise"],
  "jurisdictions": ["US-IL"],
  "effective_at": "2026-08-18T18:30:00Z",
  "max_results": 5,
  "max_content_tokens": 2500
}
```

Illustrative bounded result:

```json
{
  "evidence": [
    {
      "document_id": "delivery-policy-us",
      "document_version": "2026.08",
      "release_id": "commerce-us-2026.08",
      "authority_class": "internal_policy",
      "jurisdiction": "US-IL",
      "effective_from": "2026-08-01T00:00:00Z",
      "effective_to": null,
      "section_id": "4.3",
      "title": "Warehouse reassignment",
      "excerpt": "A bounded exact section or excerpt...",
      "content_hash": "sha256:...",
      "source_reference": "policy://delivery-policy-us/2026.08/4.3",
      "source_uri": "https://example.invalid/policy/delivery-policy-us",
      "verification_status": "verified",
      "retrieval_method": "graph",
      "relationship_path": [
        {
          "from": "warehouse/chi-01",
          "type": "GOVERNED_BY",
          "to": "policy/delivery-policy-us",
          "provenance": "knowledge://release-2026.08/source/delivery-promise"
        }
      ]
    }
  ],
  "truncated": false
}
```

These names and shapes are design examples, not implemented framework or
application contracts. Implementation planning must validate them against the
real capability and tool APIs.

Every evidence item should provide:

- Stable release, source, concept, document, and section identity
- Immutable or exactly retrievable version identity
- Effective date or validity interval when applicable
- Authority class, jurisdiction, source URI, and acquisition identity
- Integrity hash
- Provenance, verification, lifecycle, and staleness signals when present
- Retrieval method and typed relationship provenance when applicable
- Bounded content
- Safe source reference suitable for audit evidence

The knowledge adapter should return an already bounded result. Exact policy
evidence should not rely on generic result distillation to decide which clauses
survive. If a document is too large, the service should return a body-free
section index or continuation cursor so the agent can request exact sections.

### 12.8 Retrieval strategies and alternatives

No single retrieval method is best for every query. The gateway should support
several projections while keeping a small initial implementation.

| Method | Strong use | Limitation | Initial decision |
|---|---|---|---|
| Exact-section read | Contract clauses, policy sections, known concept IDs | Requires the identity to be known | Required |
| Lexical or BM25 search | SKUs, error codes, policy names, exact terms | Weak on paraphrases | Required |
| Dense vector search | Semantic similarity and paraphrases | Can miss exact terms and relationship paths | Required as one input to bounded hybrid retrieval |
| Hybrid search and deterministic fusion | Balances exact and semantic evidence | Adds an embedding dependency and index cost | Required general-search baseline |
| Contextual retrieval | Adds document context to otherwise ambiguous chunks | Adds ingestion cost and generated metadata | Evaluate for contracts and manuals |
| Late-interaction retrieval | Fine token-level matching, such as ColBERT-style retrieval | Larger, specialized indexes | Defer until simpler retrieval misses are measured |
| Hierarchical retrieval | Whole-manual or cross-corpus summary questions | Summarization cost and update complexity | Defer to global-analysis scenarios |
| Graph retrieval | Multi-hop and relationship-dependent questions | Requires relationship quality and provenance | Required for a small, typed commerce relationship slice |
| Agentic or corrective retrieval | Retries or changes strategy after weak evidence | More model calls, latency, and routing complexity | Defer until observed failures justify it |
| Long-context or cache-augmented generation | Small, stable, bounded corpus | Provider-dependent cache behavior, invalidation, cost, and uneven long-context use | Optional optimization, never the sole design |
| Structured query or semantic layer | Current facts, aggregates, and approved computations | Requires a governed schema or query contract | Use instead of document retrieval for structured facts |

The practical baseline for this accuracy-oriented demonstration is:

```text
applicability, jurisdiction, and effective-date filters
                         |
                         v
          lexical/BM25 + dense retrieval
                         |
                         v
         deterministic bounded result fusion
                         |
                         v
        one- or two-hop typed graph expansion
                         |
                         v
               exact section reads
                         |
                         v
       sufficiency, conflict, and authority checks
```

Semantic results supplement rather than replace exact matching. Reciprocal-rank
fusion or another reproducible deterministic method is sufficient initially;
the runtime does not need a separate LLM reranker. Graph traversal is selected
when the question depends on relationships. A global GraphRAG or hierarchical
query is reserved for questions about an entire corpus, not routine order
recovery.

Long model context does not remove the need for retrieval. It can simplify a
small and stable corpus, especially where a provider supports prompt or context
caching, but it increases provider coupling and invalidation work. Models can
also use information unevenly as contexts grow. The gateway must therefore
continue to return small, high-quality evidence even when the final model has a
large context window.

Fine-tuning is not a knowledge-store replacement. It can teach stable behavior
or terminology, but it is unsuitable for exact, frequently changing,
versioned, or auditable commerce facts.

### 12.9 Storage and retrieval providers

Provider selection belongs behind the foundry and gateway contracts. The
commerce agent, skill packages, and TruvaG3 framework do not depend on a graph
database, vector database, parser, or search product.

The local commerce demonstration embeds LadybugDB inside the independently
deployed Domain Knowledge Gateway. This consolidates the small typed graph,
full-text index, and vector index without adding a database workload. It is a
demonstration packaging decision, not a TruvaG3 dependency or a production
recommendation.

Provider decoupling is intentionally one small application-internal seam, not a
new framework abstraction. The gateway owns applicability filtering,
deterministic rank fusion, traversal bounds, exact-evidence shaping,
sufficiency, and conflict handling. A `KnowledgeStore` interface beneath that
logic supplies only release metadata, lexical candidates, vector candidates,
bounded paths, and exact section reads. Ladybug- or Neo4j-specific query syntax,
indexes, identifiers, scores, connections, and release loading remain inside
their adapters. The gateway exposes only stable domain-pack identities and
provider-neutral result models.

The canonical domain pack is the source of truth; a Ladybug database or Neo4j
database is a disposable serving projection rebuilt from that pack. A backend
change therefore rebuilds a new projection and runs the same conformance and
gold evaluations instead of converting a live database. Agent logic, skills,
gateway capabilities, domain packs, policy rules, and evaluation cases do not
change. The
[Enterprise Knowledge Foundry blueprint](ENTERPRISE_KNOWLEDGE_FOUNDRY.md#17-provider-neutral-boundaries)
records the provider classes and selection boundary so that this commerce
blueprint does not duplicate a separate platform decision.

### 12.10 Runtime retrieval lifecycle

```text
1. A bound skill activates and supplies procedural guidance.
                         |
2. The agent identifies the evidence category it needs.
                         |
3. Dynamic discovery selects an available knowledge capability.
                         |
4. The capability returns bounded, versioned evidence.
                         |
5. Orchestration treats retrieved text as external data, not skill instructions.
                         |
6. The agent compares alternatives using the evidence and live facts.
                         |
7. A deterministic policy service allows, denies, or requires approval.
                         |
8. The transaction service validates authority and current state again.
```

The skill describes the type and quality of evidence required. It must not
hardcode a knowledge backend, retrieval algorithm, graph query, or capability
name. Dynamic discovery remains responsible for finding the implementation
available to the agent. The gateway may select or combine retrieval projections
deterministically from the requested evidence category; an additional LLM
router is not required for the initial implementation.

### 12.11 Multi-phase and HITL consistency

An execution should retain bounded evidence references, versions, hashes, and
effective dates. It should not copy a complete source document into execution
state.

- A later phase can reuse an already retrieved bounded excerpt when its exact
  evidence identity remains applicable.
- Additional retrieval should request only the new sections needed at that
  boundary.
- An exact-version read should reproduce evidence used before a continuation or
  suspension.
- A consequential decision should accept an exact citation only with an
  integrity receipt from the knowledge boundary, bound to release, section,
  tenant, jurisdiction, channel, and decision time. This proves the exact read
  occurred; it is not a substitute for service authentication.
- Live order, inventory, price, payment, and fulfillment facts must always be
  read again before mutation.
- When an applicable policy changes during HITL suspension, the resumed action
  is evaluated against the policy currently effective for the new mutation.
- A material change to policy, evidence, price, inventory, or customer impact
  invalidates the old execution decision. The agent must replan and, when the
  approval basis changed, request approval again.

The execution history keeps the earlier evidence and approval for audit. It
does not treat that historical approval as authority for a materially different
current action.

### 12.12 Missing, stale, and conflicting knowledge

The knowledge layer must return structured outcomes instead of presenting an
empty result as proof that no rule exists.

| Condition | Required behavior |
|---|---|
| No applicable evidence | Mark evidence insufficient; use a safe path or escalate |
| Required source unavailable | Use bounded retry, then suspend, fail, or escalate |
| Expired or not-yet-effective document | Exclude it and record a bounded reason |
| Integrity mismatch | Reject the evidence and report an integrity failure |
| Conflicting authoritative sources | Apply deterministic source precedence or escalate |
| AI-derived edge is unverified | Use only as an advisory retrieval hint; do not treat it as authority |
| Graph path has missing provenance | Exclude the path from authoritative evidence |
| Result exceeds the requested bound | Return a section index or continuation cursor |
| Optional contextual source unavailable | Continue only when required evidence remains sufficient |

Retrieved text remains external data even when it comes from an internal
document system. It cannot override framework, agent, skill, platform, policy,
or approval instructions.

### 12.13 Deterministic policy enforcement

Knowledge helps the agent understand a rule; it does not enforce the rule. A
policy decision capability should return a structured result such as:

```json
{
  "decision": "approval_required",
  "decision_id": "policy-decision-01J...",
  "policy_id": "order-recovery-authority",
  "policy_version": "2026.08",
  "reason_code": "fulfillment_cost_over_autonomy_limit",
  "source_evidence_refs": [
    "evidence://commerce-us-2026.08/internal/order-recovery-authority/4.2"
  ],
  "evaluated_values": {
    "additional_cost_minor_units": 1450,
    "currency": "USD"
  }
}
```

The policy service, not an LLM, determines `allowed`, `denied`, or
`approval_required`. The transaction service must validate the decision,
approval, actor, command, and current state before applying a mutation.
For an approval-required decision, the transaction boundary must require proof
bound to the approved checkpoint, exact decision, and idempotent command
identity. A framework
checkpoint alone is insufficient if a caller can invoke the mutation directly.

For the first Kind slice, the deterministic policy capability can share the
Commerce Core deployable and datastore while retaining a separate interface,
decision record, and tests. The logical policy boundary does not require a
separate pod for the capability demonstration.

Regulatory documents support applicability, explanation, and audit evidence;
they do not become executable merely because an LLM retrieved or interpreted
them. The initial regulatory restrictions should be manually operationalized
as deterministic policy rules. Each such rule retains exact source-section and
release references. The graph can locate applicable evidence, while the policy
service enforces the reviewed operational rule.

### 12.14 Observability and data handling

Knowledge operations should expose bounded operational evidence:

- Retrieval category and lifecycle boundary
- Result count and truncation status
- Document, version, section, effective-date, and hash references
- Knowledge release, source, concept, and lifecycle identity when applicable
- Retrieval mode and verified or inferred relationship classification
- Bounded graph path and edge-provenance references when applicable
- Policy decision identity, version, and bounded reason code
- Latency, retries, and structured outcome
- Whether evidence was reused or read again

Ordinary logs, traces, metrics, and skill debug records should not contain full
document bodies, unrestricted user queries, customer data, or high-cardinality
identifiers as metric labels. Registry Viewer can display bounded evidence
references and policy outcomes; prompt or tool-call evidence remains the
controlled place for any admitted excerpt.

### 12.15 Initial implementation boundary

The first commerce implementation should add:

- A pre-published `commerce-us` domain pack containing real public regulatory
  evidence, approved guidance, synthetic or customer-approved internal
  policies, and a version-controlled evaluation set
- Provider-neutral application capabilities for search, relationship
  traversal, and exact-version section reads
- One embedded LadybugDB projection inside the Domain Knowledge Gateway for
  reproducible full-text, vector, and typed graph retrieval; no separate graph
  database pod
- One small application-internal `KnowledgeStore` boundary with a Ladybug
  adapter, stable pack identities, and a provider-conformance suite reusable by
  a later Neo4j adapter
- Precomputed document embeddings and a configured query-embedding provider;
  no separate embedding-model pod is required for the initial demonstration
- Deterministic fusion of bounded lexical and semantic candidates followed by
  one- or two-hop graph expansion and exact-section reads
- A small reviewed graph projection for the primary order-recovery scenario
- A deterministic policy service
- Gold retrieval and decision evaluations that compare lexical-only,
  hybrid, and hybrid-plus-graph behavior

It should not implement source connectors, document parsing, AI extraction,
entity resolution, expert review, or general knowledge publication. Those are
part of the separate Knowledge Foundry workstream. It should also not add a
generic RAG interface, OKF parser, graph type, vector-store dependency, or
commerce-specific type to TruvaG3 framework modules. The knowledge gateway is
an application service reached through ordinary discoverable tool capabilities.

A reusable framework abstraction should be considered only after multiple
independent applications demonstrate the same stable lifecycle and contract.

### 12.16 Research basis and decision trail

The ingestion, parsing, graph-construction, provenance, retrieval, evaluation,
and publication research completed on 2026-08-18 is preserved in the
[Enterprise Knowledge Foundry research trail](ENTERPRISE_KNOWLEDGE_FOUNDRY.md#22-research-basis-and-decision-trail).
This commerce section retains only the consumer-side conclusions that affect
agent behavior, policy enforcement, execution consistency, and observability.
The domain-pack, hybrid-retrieval, Ladybug-first local packaging, and
Neo4j-compatible adapter boundary in this blueprint apply those conclusions to
the capability demonstration; they do not replace or narrow the broader
research, provider alternatives, or open production decisions recorded in the
foundry document.

The capability-demo refinement completed on 2026-08-19 also records these
consumer-side findings:

- [Microsoft GraphRAG's default dataflow](https://github.com/microsoft/graphrag/blob/main/docs/index/default_dataflow.md)
  preserves documents and text units alongside entities and relationships, and
  its [local search](https://microsoft.github.io/graphrag/query/local_search/)
  combines graph context with linked source text. This supports exact evidence
  reads after graph-assisted retrieval rather than answering from graph facts
  alone.
- The [AWS GraphRAG Toolkit lexical graph](https://awslabs.github.io/graphrag-toolkit/lexical-graph/overview/)
  similarly preserves a source-to-chunk-to-statement-to-fact-to-entity path,
  reinforcing the provenance model used by the gateway.
- The [Neo4j Knowledge Graph Builder](https://neo4j.com/docs/neo4j-graphrag-python/current/user_guide_kg_builder.html)
  documents an extraction pipeline but does not make an LLM-produced schema
  strict. The demonstration therefore reviews every exercised relationship and
  treats unverified AI-derived edges only as retrieval hints.
- Neo4j provides both
  [full-text and vector indexes](https://neo4j.com/docs/cypher-manual/current/indexes/),
  so it remains a practical server-based migration target without becoming the
  gateway contract.
- [LadybugDB](https://github.com/LadybugDB/ladybug) is MIT-licensed, embedded,
  disk-backed, Cypher-oriented, and advertises native full-text and vector
  indexes. Its [official Go binding](https://github.com/LadybugDB/go-ladybug)
  uses CGO and native libraries. The first milestone therefore must prove the
  pinned library, target Kind architecture, and offline extension packaging
  before LadybugDB is accepted as the demo default.
- LadybugDB documents full-text and vector capabilities through its
  [extension mechanism](https://docs.ladybugdb.com/extensions/). The gateway
  image must bundle every required pinned artifact; a cold deployment cannot
  depend on downloading an extension at runtime.
- Public readability does not imply redistribution permission. The pack records
  source licensing explicitly; for example,
  [USPS Publication 52](https://pe.usps.com/text/pub52/pub52tl.htm) carries an
  explicit copyright notice and should remain reference-only unless permission
  permits bundling. US government works are generally public domain, subject to
  third-party-content exceptions described by [GovInfo](https://www.govinfo.gov/about).

## 13. Autonomy and Approval

Every mutating decision must pass through an explicit authority envelope. The
example should make the policy visible and configurable.

| Action class | Example default behavior |
|---|---|
| Read order, inventory, and fulfillment data | Autonomous |
| Select an equivalent location at equal or lower cost | Autonomous |
| Add a small bounded fulfillment cost | Configurable |
| Split a shipment | Configurable based on promise and cost |
| Offer a bounded account credit | Configurable |
| Change the charged total | Approval required |
| Exceed a compensation or fulfillment threshold | Approval required |
| Use a substitute the customer did not authorize | Customer decision required |
| Execute a real payment or refund | Out of scope for the initial sandbox |

These are example policy classes, not universal commerce rules. The runnable
example must use clearly documented synthetic internal thresholds while
retaining exact references to the real external evidence that motivated any
operationalized regulatory rule.

A human approval checkpoint should contain:

- Proposed business action
- Current authoritative state version
- Evidence and alternatives considered
- Customer and delivery impact
- Financial effect
- Applicable policy and exceeded boundary
- Expiration time

On resume, the agent must read the current business state again. Approval
permits the proposed class of action; it does not make stale inventory or
pricing valid.

Identity, authentication, and infrastructure authorization remain deployment
and application concerns. The framework must not be presented as providing
them. The transaction service must receive a verified actor and enforce its own
authorization before applying a command.

## 14. Primary Workflow

### 14.1 Inventory shortfall recovery

```text
1. Receive inventory_reservation_failed
                    |
2. Load bound skill candidates and pin exact versions
                    |
3. Read current order, inventory, and fulfillment facts
                    |
4. Identify required domain evidence from the active guidance
                    |
5. Retrieve bounded, versioned knowledge sections
                    |
6. Determine whether the facts and evidence are sufficient
                    |
7. Compare currently valid recovery alternatives
                    |
8. Select a preferred option and request a policy decision
             +------+------+
             |             |
          allowed     approval required
             |             |
             |        create HITL checkpoint
             |             |
             |        wait for decision
             |             |
             +------+------+
                    |
9. Read live state and current policy again; replan if they changed
                    |
10. Issue an idempotent command with its authority reference
                    |
11. Verify order, inventory, fulfillment, and payment state
                    |
12. Use the notification capability, or a later Customer Recovery Agent, to
    communicate the confirmed outcome
                    |
13. Schedule later verification and store reusable learning
```

### 14.2 Multi-phase behavior

The execution naturally exercises multiple phases:

- **Understand:** classify the exception and business impact.
- **Investigate:** gather only the live facts and versioned domain evidence
  needed for the current decision.
- **Decide:** compare alternatives and obtain a deterministic policy decision.
- **Act:** execute an autonomous or approved command.
- **Verify:** independently check the business outcome.
- **Learn:** record a bounded investigation summary and follow-up.

Skill instructions and resources should be projected only into the phases that
need them. Retrieved excerpts are external evidence rather than skill content.
Exact skill versions and bounded knowledge references remain part of the
execution evidence.

## 15. Transaction and Concurrency Safety

Every mutating command should include:

```json
{
  "command_id": "cmd-01J...",
  "order_id": "ord-1042",
  "expected_order_version": 7,
  "actor": {
    "type": "agent",
    "id": "order-operations-agent"
  },
  "authority_reference": "policy-evaluation-or-approval-id",
  "action": "reassign_fulfillment",
  "parameters": {
    "line_id": "line-2",
    "from_location": "warehouse-north",
    "to_location": "warehouse-west"
  }
}
```

The service should return structured outcomes such as:

- Applied
- Already applied
- Version conflict
- Inventory unavailable
- Policy or authority rejected
- Invalid state transition
- Temporary dependency failure

Retries are safe only when the command is idempotent. A model-generated textual
description is not an idempotency key and must not be used as one.

## 16. Failure and Recovery

The solution must define behavior for:

- Duplicate and out-of-order events
- AI provider timeout or unavailability
- A capability disappearing after planning
- Required domain knowledge being unavailable, stale, or conflicting
- A knowledge version becoming unavailable during continuation or resume
- Applicable policy changing during approval
- Inventory changing during approval
- Approval expiration
- Agent pod restart during execution
- Tool success followed by response loss
- Customer communication failure after a successful recovery
- Partial verification or conflicting business state

Expected principles:

- Persist work before acknowledging an event.
- Use deterministic deduplication and command idempotency.
- Resolve capabilities again at an appropriate execution boundary.
- Reject unverifiable knowledge and distinguish no result from proof of absence.
- Re-evaluate current policy and live facts immediately before mutation.
- Preserve completed transaction facts across replanning.
- Never report success from an unverified natural-language response.
- Use model-provider fallback only for AI work; it does not replace a failed
  transaction service.
- Escalate unresolved or inconsistent state with complete evidence.
- Keep the normal deterministic order path available when agents are degraded.

## 17. Observability and Auditability

### 17.1 Registry Viewer

The execution view should make it possible to answer:

- Which event started the execution?
- Which agent owned each business decision?
- Which skills and exact versions were selected and loaded?
- Which knowledge documents, versions, sections, and hashes supplied evidence?
- Which policy decision and version governed the action?
- Which alternatives were considered?
- Why was approval required or skipped?
- Which command was issued, and was it applied exactly once?
- How was the result verified?
- What customer communication followed?

The viewer should use bounded metadata and references. Payment data, secrets,
full customer records, and unnecessarily large business payloads must not be
copied into execution debug records.

### 17.2 Traces and logs

A distributed trace should connect:

```text
business event
  -> agent execution
  -> planning and skill lifecycle
  -> domain knowledge retrieval
  -> deterministic policy decision
  -> discovered capability calls
  -> transaction command
  -> verification
  -> collaboration or customer communication
```

Logs should remain structured, correlated, sanitized, and useful without
duplicating prompt or business-record bodies.

### 17.3 Business metrics

Candidate metrics include:

- Order exceptions received
- Autonomous resolution rate
- Approval rate and approval waiting time
- Recovery duration
- Delivery promises preserved
- Orders lost or cancelled after an exception
- Additional fulfillment cost
- Compensation cost
- Customer notification delay
- Insufficient-evidence and knowledge-retrieval failures
- Policy changes detected before mutation
- Stale-state replans
- Duplicate commands safely rejected
- Verification failures

Business dimensions must be bounded before they become telemetry labels. Order
IDs, customer IDs, and free-form reason text do not belong in metric labels.

## 18. Local Demonstration

The final example should deploy from one top-level `setup.sh` and reproduce the
scenario without external business accounts.

Proposed layout:

```text
truvaagents/
  truva-g3/
    examples/k8-deployment/      # shared framework setup helper
  truva-g3-domain-examples/
    agentic-commerce/
      README.md
      ARCHITECTURE.md
      setup.sh
      .env.example
      k8-deployment.yaml
      commerce-core/
        policy/
      domain-knowledge-gateway/
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
    order-operations-agent/
      skills/
        packages/
          commerce/
    event-simulator/
```

This layout is illustrative. The implementation must follow the examples
portability contract and should minimize deployables until boundaries are
proved useful. The example's `setup.sh` should deploy the Domain Knowledge
Gateway with a pinned LadybugDB library and the immutable domain pack in its
image. A gateway init container should validate the pack and build its
disposable Ladybug serving projection on a shared `emptyDir`; the main gateway
container opens the activated projection for serving. This adds no separate
database pod or durable knowledge volume. Document acquisition, parsing,
extraction, review, embedding generation, and release publication remain
offline build activities and are not cluster services.

### 18.1 Demonstration scenario

1. Load the immutable `commerce-us` domain pack, its real regulatory sources,
   synthetic internal policies, reviewed graph, indexes, and gold evaluations.
2. Seed products, including a lithium-ion-battery SKU with structured product
   classification, two warehouses, inventory, carrier options, deterministic
   authority rules, and a synthetic customer.
3. Place a next-day order and reserve its inventory at the preferred warehouse.
4. Trigger a warehouse disruption or inventory correction.
5. Read current alternatives from Commerce Core and retrieve bounded evidence
   about shipping promises, delays, battery transport, routing, and internal
   recovery authority.
6. Traverse the relevant product-to-regulation-to-restriction-to-recovery paths
   and read the exact supporting sections.
7. Observe deterministic policy reject an ineligible transport option rather
   than relying on an LLM interpretation of regulatory prose.
8. Let a compliant low-cost recovery proceed autonomously.
9. Run a second variation that requires customer delay consent,
   cancellation/refund handling, or human approval.
10. Approve or reject the action through the existing HITL interface.
11. Verify final commerce state and customer communication.
12. Inspect skill projections, exact evidence and graph paths, policy decisions,
    commands, and verification in Registry Viewer and Jaeger.

The simulator must identify itself as a demo-only component and must not expose
fault-injection controls as production patterns.

## 19. Implementation Phases

The phases below preserve the architectural sequence. They are historical
design guidance, not the execution source of truth. Current behavior is
authoritative in the [runnable example](../agentic-commerce/), its capability
contracts, scenario assertions, and tests.

### Phase 0: Validate the slice

- Confirm the minimal business state model.
- Confirm which existing event, HITL, scheduling, memory, and skill patterns can
  be reused without framework changes.
- Classify initial content as skill guidance, domain knowledge, live fact,
  memory, or enforced policy.
- Confirm the published knowledge-release contract and keep Knowledge Foundry
  ingestion and publication outside the control-tower implementation scope.
- Define evidence sufficiency, source precedence, version consistency, and
  resume behavior.
- Confirm the redistribution status and snapshot policy for every proposed
  public source; exclude or reference-only any document that cannot be bundled.
- Define the `commerce-us` pack manifest and the provider-neutral domain-pack
  conformance contract.
- Define the gold retrieval and decision cases before selecting evaluation
  thresholds.
- Define latency and reliability targets.
- Define the lithium-battery warehouse-disruption scenario, its variations, and
  expected evidence, relationship paths, policy decisions, and outcomes.

### Phase 1: Deterministic Commerce Core

- Implement products, orders, inventory, fulfillment alternatives, and payment
  sandbox state.
- Implement valid state transitions, optimistic concurrency, and idempotent
  commands.
- Emit business events.
- Add deterministic tests before introducing an agent.

### Phase 2: Runtime Knowledge and Policy

- Add an immutable `commerce-us` domain pack containing real public regulatory
  evidence, approved guidance, synthetic or customer-approved internal
  policies, exact sections, embeddings, a reviewed typed graph, and gold
  evaluations. Treat it as output from the separate Knowledge Foundry.
- Define provider-neutral application capability contracts for bounded search,
  relationship traversal, and exact-version section reads.
- Embed LadybugDB in the Domain Knowledge Gateway and load the published pack
  through a Ladybug adapter without implementing document ingestion or
  compilation. Keep Ladybug query syntax and native identifiers inside that
  adapter.
- Keep the gateway's retrieval orchestration above one minimal
  `KnowledgeStore` interface and apply a backend-independent conformance suite
  to the Ladybug adapter. Document how the same canonical pack maps to a future
  Neo4j adapter; do not implement both backends merely to prove the seam.
- Implement applicability filters, lexical and vector retrieval, deterministic
  result fusion, bounded graph expansion, and exact-section reads.
- Record the provenance and verification state of every relationship; keep
  AI-derived relationships advisory until verified.
- Manually review every relationship and regulatory rule used by the
  demonstration scenarios; link each operationalized rule to exact evidence.
- Establish lexical-only, hybrid, and hybrid-plus-graph evaluation baselines;
  defer model reranking, contextual retrieval, hierarchical retrieval, and
  corrective retrieval until measured failures justify them.
- Add deterministic source precedence, effective-date, and integrity checks.
- Add a deterministic authority-policy capability with structured decisions,
  hosted in the Commerce Core deployable for the initial Kind slice.
- Test missing, stale, conflicting, excessive, unavailable, unverified, and
  provenance-free knowledge.
- Prove that changing the retrieval or storage backend does not change skill or
  agent configuration.
- Load a small non-commerce conformance fixture through the same gateway binary
  to verify domain-pack portability without deploying a second application.
- Confirm that no parser, source connector, extraction model, entity resolver,
  or knowledge-review workflow is added to the control-tower runtime.

### Phase 3: Order Operations Agent

- Add event intake.
- Add explicit skill bindings and Git-owned skill packages.
- Discover commerce, knowledge, and policy capabilities dynamically.
- Investigate, decide, execute, and verify one inventory-shortfall path.
- Preserve bounded evidence references without storing complete document bodies
  in execution debug state.
- Confirm the deterministic order path works when skills or AI are disabled.

### Phase 4: Governed Exceptions

- Add policy evaluation and authority evidence.
- Add HITL checkpoint and resume.
- Revalidate transaction state, current policy, and required evidence after
  resume.
- Request a new approval when the basis of an earlier approval changes.
- Test approval, rejection, expiration, knowledge or policy change, state
  conflict, and duplicate delivery.

### Phase 5: Customer Recovery

- Add a distinct Customer Recovery Agent only after its independent objective is
  justified.
- Communicate only confirmed transaction outcomes.
- Add bounded compensation and customer-choice paths.

### Phase 6: Operational Proof

- Add delayed verification and agentic memory.
- Add Registry Viewer-friendly business, knowledge, and policy evidence.
- Verify traces, logs, metrics, skill lifecycle data, bounded knowledge
  references, and policy decisions.
- Run repeated live scenarios, provider failure, pod restart, and replica tests.

### Phase 7: External Interoperability

- Evaluate a UCP or ACP gateway after the internal commerce lifecycle is stable.
- Keep protocol-specific types out of framework core and orchestration packages.
- Use an adapter, gateway, or tool boundary.

## 20. Acceptance Criteria

The first runnable version is successful when:

- A fresh Kind deployment recreates all required data and skill packages.
- The cluster runs only three solution workloads: Order Operations Agent,
  Commerce Core, and Domain Knowledge Gateway. The gateway embeds LadybugDB for
  graph, full-text, and vector retrieval without running a separate database,
  foundry, or ingestion workload.
- A normal order completes without an LLM dependency.
- An inventory-shortfall event starts one durable agent execution.
- The agent discovers capabilities without skill instructions naming them.
- Large manuals and policy corpora remain outside skill packages.
- A Git-owned `commerce-us` domain pack can recreate the local gateway's serving
  state on a cold start without running a foundry pipeline.
- The domain pack contains real authoritative public regulatory evidence,
  approved guidance, and clearly labelled synthetic or customer-approved
  internal policies.
- Every bundled source has recorded provenance, acquisition identity,
  effective dates, integrity, authority class, and redistribution status.
- The knowledge capability returns only bounded, relevant, versioned sections.
- Retrieved evidence includes document, version, effective-date, section, and
  integrity identity.
- Relationship-dependent retrieval returns a bounded typed path with source and
  verification provenance.
- AI-derived relationships remain visibly advisory until verified.
- Every graph relationship and operationalized regulatory rule exercised by
  the demonstration is reviewed and linked to exact supporting evidence.
- Applicability filtering, hybrid lexical and semantic retrieval, deterministic
  result fusion, bounded graph expansion, and exact-section reads are visible
  as separate steps in the execution evidence.
- Required missing, stale, conflicting, or unverifiable evidence produces the
  documented safe behavior rather than a guessed decision.
- At least 50 version-controlled evaluation cases cover exact terms,
  paraphrases, aliases, jurisdiction and effective dates, superseded sources,
  multi-hop paths, hard negatives, conflicts, and unanswerable questions.
- Evaluation reports exact-section recall at a declared `k`, correct graph-path
  rate, jurisdiction and version accuracy, citation faithfulness,
  no-answer/abstention accuracy, unsupported-claim rate, and final
  business-decision accuracy.
- The evaluation compares lexical-only, hybrid, and hybrid-plus-graph behavior
  so the value and failure modes of each projection are measurable.
- The retrieval backend can change without changing skills, agent logic,
  gateway capability contracts, canonical packs, policy logic, or gold
  evaluations. Backend-specific work is confined to a store adapter, projection
  loader, configuration, and Kubernetes resources.
- The same Domain Knowledge Gateway binary loads a small non-commerce
  conformance pack without code changes; only the pack, skills, domain tools,
  and operational policies are allowed to vary.
- A deterministic policy decision, not an LLM response, determines whether an
  action is allowed, denied, or requires approval.
- An autonomous recovery stays within its configured authority.
- An exceptional recovery suspends and resumes through HITL.
- Resume revalidates current business state, evidence, and policy before
  mutation and obtains new approval when the decision basis materially changes.
- Duplicate events and retries do not duplicate transaction changes.
- The final result is independently verified against authoritative services.
- Skills, agents, knowledge references, policy decisions, capability calls,
  approval, commands, and verification are visible in Registry Viewer and
  traces.
- Complete knowledge documents are not copied into skills, execution debug
  state, ordinary traces, metrics, or logs.
- Sensitive and high-cardinality business data is absent from unsafe
  observability surfaces.
- Provider failure does not prevent normal deterministic commerce operations.
- The demo reports business outcome and cost, not only technical success.

## 21. Later Extensions

After the initial recovery flow is reliable, possible extensions include:

- Consumer shopping and product-discovery agent
- Agent-initiated checkout using scoped payment tokens
- Inventory rebalancing across locations
- Supplier and replenishment agents
- Delivery disruption and carrier selection
- Promotion and merchandising governance
- B2B purchase-order intake and exception negotiation
- Returns and refund recovery
- Multi-currency and cross-border fulfillment
- Customer retention and proactive service recovery

Each extension must retain the same boundary: agents coordinate adaptive work;
authoritative services enforce transaction facts and invariants.

## 22. Open Design Decisions

The blueprint intentionally leaves these decisions for implementation planning:

- Production transaction datastore; the local demonstration uses SQLite on a
  demo-only persistent volume
- Production durable event transport; the local demonstration reuses the Redis
  task-queue and task-store pattern
- Production domain-knowledge storage and retrieval adapters; the local
  demonstration embeds LadybugDB behind the provider-neutral gateway, with
  Neo4j retained as the first documented server-based migration target
- Query-embedding provider and embedding model used to compile and query the
  first immutable pack
- The commerce relationship vocabulary represented in the published fixture
- The final source versions, acquisition process, and redistribution status for
  the `commerce-us` pack
- Source-authority and conflict-precedence model
- Consumer-side exact-section, citation, release-pinning, and
  version-retention contracts
- The evaluation thresholds required for release and the measured failures that
  would justify model reranking, contextual retrieval, hierarchical retrieval,
  or corrective retrieval
- Knowledge token and result-count budgets for each execution boundary
- Packaging boundary between the knowledge adapter and deterministic policy
  service
- Whether the first commerce adapter is one tool or several
- Exact business state machine and event schema versioning
- Synthetic autonomy and compensation thresholds
- How customer choice is represented during suspended execution
- Which existing agent pattern should seed the first implementation
- Whether the first UI is a minimal storefront, an operations console, or both
- Which external commerce protocol, if any, should be demonstrated later
- Whether experience from multiple applications eventually justifies a generic
  framework retrieval abstraction

Source connectors, parser selection, Knowledge Document IR, extraction,
entity-resolution rules, expert-review workflow, and release-publication
technology are decisions of the separate
[Enterprise Knowledge Foundry](ENTERPRISE_KNOWLEDGE_FOUNDRY.md), not open
decisions for the control-tower runtime.

These choices should be resolved using the real framework and example code at
the time of implementation. They should not be inferred from the illustrative
directory names or pseudocode in this document.

## 23. Related Industry Direction

The design is compatible with current agentic-commerce work without depending
on it:

- [Google Universal Commerce Protocol](https://developers.googleblog.com/under-the-hood-universal-commerce-protocol-ucp/)
  describes discovery and commerce capabilities across product discovery,
  checkout, and order management, with API, A2A, and MCP bindings.
- [Stripe agentic commerce documentation](https://docs.stripe.com/agentic-commerce/protocol)
  keeps the seller's data model and payment processing authoritative while an
  agent coordinates the buyer-facing checkout lifecycle.
- [Visa Intelligent Commerce](https://www.visa.com/en-us/solutions/intelligent-commerce)
  emphasizes agent identity, payment controls, authentication, spending limits,
  and approval requirements.

External protocol support should be implemented through an application adapter
or gateway. This solution must not introduce commerce-specific dependencies
into TruvaG3 framework modules.
