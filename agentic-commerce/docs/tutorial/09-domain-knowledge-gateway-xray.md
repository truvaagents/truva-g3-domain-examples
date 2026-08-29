# Part 9: Domain Knowledge Gateway X-ray

This chapter opens the Domain Knowledge Gateway from the outside and the
inside. It uses one fresh execution from the live local Kind cluster, the exact
gateway calls recorded for that execution, direct gateway calls, the active
`pack.json`, and the Go implementation.

The short answer is:

> The gateway is a deterministic, scoped policy-retrieval service. The AI agent
> decides what evidence to ask for. The gateway decides how to filter, rank,
> traverse, and prove that evidence. The gateway never decides whether the
> business action is allowed.

## 1. Evidence used in this chapter

The synthetic state was reset, the `commerce-demo` pack was activated, and one
new webhook event was submitted on August 26, 2026 CDT (August 27 UTC).

| Evidence | Identifier |
|---|---|
| Submitted event | `xray-gateway-1787796603482513000` |
| Task | `order-event-xray-gateway-1787796603482513000` |
| Registry execution | `order-operations-1787796611243177502` |
| Jaeger trace | `a5a2d1f925ba11caa89ebcb35dff5fae` |
| Order | `ORD-1003` |
| Active release | `commerce-us-2026-08-20` |
| Release hash | `sha256:49643f3bb48d35aa04ea786ab1b0205475eadcbfd247cd74cc8c55a4a5153d28` |

Open the [Registry Viewer](http://registry.localhost/) and search for the full
execution ID. Open the
[Jaeger trace](http://jaeger.localhost/trace/a5a2d1f925ba11caa89ebcb35dff5fae)
to see the corresponding network calls.

## 2. How the execution was triggered

The event was submitted through the public agent ingress. The gateway was not
called by the test driver at this point.

```http
POST http://agentic-commerce.localhost/webhook/order-risk
Content-Type: application/json
```

```json
{
  "event_id": "xray-gateway-1787796603482513000",
  "order_id": "ORD-1003",
  "signal": "package is missing in transit and replacement inventory is available",
  "requested_outcome": "create and verify the bounded automatic replacement",
  "requested_option_id": "same-sku-replacement",
  "decision_time": "2026-08-20T18:00:00Z"
}
```

The webhook accepted the asynchronous task:

```json
{
  "retry": false,
  "status": "accepted",
  "status_url": "/api/v1/tasks/order-event-xray-gateway-1787796603482513000",
  "task_id": "order-event-xray-gateway-1787796603482513000"
}
```

The task completed in about 54 seconds. Registry recorded 47,283 ms inside the
orchestrated execution. The difference is the webhook/task envelope outside
the Registry execution timer.

The final business result was:

```json
{
  "status": "completed",
  "terminal_outcome": "recovered",
  "policy_outcome": "allowed",
  "order_id": "ORD-1003",
  "verified": true,
  "evidence_section_ids": [
    "demo-auto-recovery-bounds",
    "demo-approval-thresholds",
    "demo-evidence-precedence"
  ],
  "order_version_before": 1,
  "order_version_after": 2,
  "fulfillment_status_after": "replacement_created"
}
```

## 3. The exact gateway calls made by the agent

Registry recorded five calls to the Domain Knowledge Gateway. Jaeger contains
five matching server spans. The three exact reads ran in parallel, so their
recorded order is not their logical dependency order.

| Agent step | Capability | Input focus | Registry duration | Jaeger server duration |
|---|---|---|---:|---:|
| 4 | `get_release_info` | Active release | 3.125 ms | 0.947 ms |
| 3 | `search_evidence` | Replacement authority and limits | 17.512 ms | 14.830 ms |
| 7 | `read_exact_section` | Evidence precedence | 4.706 ms | 2.229/0.949/0.470 ms across the three concurrent reads |
| 5 | `read_exact_section` | Automatic-recovery bounds | 5.109 ms | included above |
| 6 | `read_exact_section` | Approval thresholds | 5.829 ms | included above |

There was no separate `traverse_relations` call in this execution.
`search_evidence` automatically returned one-hop paths for its ranked
candidates. The agent used those paths to identify the prerequisite and
escalation sections.

The gateway pod logs agree with the other two evidence layers. For the same
request ID they show one successful release lookup, one successful search with
`candidate_count=10`, `path_count=8`, and `warning_count=0`, and three successful
exact reads for the same three section IDs. All five HTTP responses were 200.

### Call 1: pin the release

Request:

```json
{}
```

Response:

```json
{
  "success": true,
  "data": {
    "release_id": "commerce-us-2026-08-20",
    "domain": "commerce-order-recovery",
    "tenant_id": "commerce-demo",
    "published_at": "2026-08-20T16:00:00Z",
    "valid_from": "2026-08-20T00:00:00Z",
    "content_hash": "sha256:49643f3bb48d35aa04ea786ab1b0205475eadcbfd247cd74cc8c55a4a5153d28",
    "section_count": 18,
    "source_count": 7
  }
}
```

This pins the identity of the evidence set. It does not retrieve policy text.

### Call 2: search the scoped release

Request:

```json
{
  "query": "same-SKU replacement authorization for missing in transit orders: enterprise authority, inventory availability, low fraud risk, restricted merchandise exclusions, value thresholds, and approval requirements",
  "tenant_id": "commerce-demo",
  "jurisdiction": "US",
  "channel": "online",
  "as_of": "2026-08-20T18:00:00Z",
  "limit": 20
}
```

The response contained ten candidates. These are their final reciprocal-rank
fusion scores:

| Rank | Section | Score | Why it appeared |
|---:|---|---:|---|
| 1 | `demo-auto-recovery-bounds` | 0.032522 | Same-SKU replacement, inventory, risk, restriction, and value terms |
| 2 | `ftc-delay-consent-or-refund` | 0.031778 | Missing-order and merchandise terms |
| 3 | `demo-approval-thresholds` | 0.031754 | Replacement, restrictions, thresholds, and approval |
| 4 | `demo-missing-merchandise-refund` | 0.031025 | Missing-in-transit merchant remedy |
| 5 | `demo-evidence-precedence` | 0.031010 | Enterprise evidence and order terms |
| 6 | `walmart-general-return-window` | 0.030798 | General order/merchandise overlap |
| 7 | `ftc-shipment-representation` | 0.029644 | General order/merchandise overlap |
| 8 | `demo-verification-notification` | 0.029412 | Order completion procedure |
| 9 | `target-general-return-window` | 0.029211 | General merchandise overlap |
| 10 | `ftc-prompt-refund-timing` | 0.028778 | General order overlap |

Every candidate reported `retrieval: "rrf(bm25+semantic)"`. Here `semantic`
does not mean an embedding model. The demo uses reviewed aliases, hashed tokens
and bigrams, and cosine similarity. This is explained in section 8 below.

The same search response also returned eight one-hop paths. The three paths
from the top replacement section were decisive:

```text
demo-auto-recovery-bounds
  ├─ ESCALATES_TO → demo-approval-thresholds
  ├─ REQUIRES     → demo-evidence-precedence
  └─ FOLLOWED_BY  → demo-verification-notification
```

The agent used the first two links. It did not exact-read the notification
procedure as decision evidence because notification happens after the
authoritative decision, execution, and verification.

### Calls 3–5: exact-read the decisive sections

Each exact read used the same release and scope:

```json
{
  "tenant_id": "commerce-demo",
  "jurisdiction": "US",
  "channel": "online",
  "as_of": "2026-08-20T18:00:00Z",
  "release_id": "commerce-us-2026-08-20",
  "section_id": "<one exact section ID>"
}
```

The three returned sections were:

#### `demo-auto-recovery-bounds`

```json
{
  "title": "Bounded automatic recovery options",
  "content": "After eligibility is supported by cited evidence, the demo may automatically offer a same-SKU replacement or equivalent reroute when merchandise value is at most 75 USD, inventory is available, destination is unchanged, fraud risk is low, and no restricted category is involved. The command must carry an idempotency key and the evidence release hash.",
  "facts": {
    "max_auto_merchandise_value_usd": 75,
    "required": [
      "inventory_available",
      "unchanged_destination",
      "low_fraud_risk",
      "not_restricted",
      "idempotency_key",
      "release_hash"
    ]
  }
}
```

#### `demo-approval-thresholds`

```json
{
  "title": "Human approval thresholds",
  "content": "The demo requires human approval for a merchandise refund above 100 USD, a goodwill credit above 25 USD, any destination change, a policy override, restricted merchandise, or an action with conflicting or stale decisive evidence. Approval does not replace deterministic command validation or idempotency.",
  "facts": {
    "refund_over_usd": 100,
    "credit_over_usd": 25,
    "always_approve": [
      "destination_change",
      "policy_override",
      "restricted_merchandise",
      "conflicting_or_stale_evidence"
    ]
  }
}
```

#### `demo-evidence-precedence`

```json
{
  "title": "Evidence precedence and abstention",
  "content": "The demo evaluates applicable law first, then the order's seller and item-specific terms, then carrier rules, and finally the synthetic enterprise recovery standard. A narrower applicable exception overrides a general rule. Missing seller, category, effective-date, or consent evidence produces abstention or escalation rather than an invented eligibility decision.",
  "facts": {
    "precedence": [
      "applicable_law",
      "seller_item_terms",
      "carrier_rules",
      "enterprise_standard"
    ],
    "missing_decisive_evidence": "escalate"
  }
}
```

All three came from source `commerce-demo-recovery-standard`, with the
provenance URL `urn:truvag3:commerce-demo:recovery-standard:v1`. This is a
synthetic internal policy. The same pack also contains public FTC, eCFR,
Target, Walmart, USPS, and FedEx sources.

### What one signed receipt proves

The reusable HMAC segment is not copied into this tutorial. The readable claims
from the recorded automatic-recovery receipt were:

```json
{
  "version": 1,
  "section_id": "demo-auto-recovery-bounds",
  "release_id": "commerce-us-2026-08-20",
  "release_hash": "sha256:49643f3bb48d35aa04ea786ab1b0205475eadcbfd247cd74cc8c55a4a5153d28",
  "tenant_id": "commerce-demo",
  "jurisdiction": "US",
  "channel": "online",
  "as_of": "2026-08-20T18:00:00Z",
  "issued_at": "2026-08-27T02:10:39.187521213Z",
  "expires_at": "2026-08-27T02:40:39.187521213Z"
}
```

The receipt was 559 characters and had this shape:

```text
base64url(JSON claims).hex(HMAC-SHA256 signature)
```

It proves that the exact section was read under that release, scope, and
decision time. It does not prove that a replacement is allowed.

## 4. What happened after the gateway calls

The agent passed these three section IDs, their three receipts, the release ID,
the release hash, the order ID, the requested option, and the decision time to
Commerce Core's `evaluate_recovery_policy` capability.

Commerce Core—not the gateway and not the LLM—compared the evidence with the
current order facts:

```json
{
  "order_id": "ORD-1003",
  "item_value_usd": 69,
  "inventory_available": true,
  "fraud_risk": "low",
  "restricted": false,
  "fulfillment_status": "missing_in_transit",
  "version": 1
}
```

It returned:

```json
{
  "outcome": "allowed",
  "eligible": true,
  "approval_required": false,
  "reason": "replacement preconditions and enterprise policy evidence are satisfied"
}
```

The reason is easy to audit: 69 USD is below the 75 USD automatic-replacement
bound, inventory exists, risk is low, and the item is not restricted. Commerce
Core then executed once, verified order version 2, and recorded a notification.

## 5. The data actually loaded by the gateway

The active file is
[`domain-packs/commerce-demo/pack.json`](../../domain-packs/commerce-demo/pack.json).
It is 25,967 bytes in this checkout—not 2–10 MB.

| Pack element | Count | Purpose |
|---|---:|---|
| Sources | 7 | Provenance for public and synthetic policy documents |
| Sections | 18 | Searchable, scoped units of normalized policy |
| Relations | 14 | Authored links such as `REQUIRES` and `OVERRIDES` |
| Semantic aliases | 23 | Reviewed vocabulary normalization |
| Golden cases | 7 | Expected and forbidden retrieval results |

The seven sources are:

1. FTC Mail Order Rule guide;
2. 16 CFR Part 435 in eCFR;
3. Target Returns Help;
4. Walmart Standard Return Policy;
5. USPS Domestic Mail Manual 604;
6. FedEx Money-Back Guarantee;
7. the synthetic Commerce Demo Recovery Standard.

`pack.json` is the canonical source for the demo. LadybugDB is a disposable
projection of its sections and relations.

## 6. Direct calls to the running gateway

The gateway has no public ingress. That is intentional: agents discover and
call it inside the cluster. For local inspection, create a temporary
port-forward:

```bash
kubectl port-forward -n truvag3-examples \
  service/domain-knowledge-gateway-service 8396:80
```

### List its capabilities

```bash
curl -sS http://127.0.0.1:8396/api/capabilities | jq .
```

The public operations are:

- `get_release_info`
- `search_evidence`
- `traverse_relations`
- `read_exact_section`

The list also shows `manage_demo_domain_pack` with `internal: true`. It exists
only in development mode and is hidden from LLM planning.

### Repeat the execution's search

```bash
curl -sS -X POST \
  http://127.0.0.1:8396/api/capabilities/search_evidence \
  -H 'Content-Type: application/json' \
  -d '{
    "query":"same-SKU replacement authorization for missing in transit orders: enterprise authority, inventory availability, low fraud risk, restricted merchandise exclusions, value thresholds, and approval requirements",
    "tenant_id":"commerce-demo",
    "jurisdiction":"US",
    "channel":"online",
    "as_of":"2026-08-20T18:00:00Z",
    "limit":20
  }' | jq .
```

The direct call reproduced the same release hash, the same ten candidates in
the same order, and the same eight one-hop paths as the agent execution.

### Traverse one section explicitly

```bash
curl -sS -X POST \
  http://127.0.0.1:8396/api/capabilities/traverse_relations \
  -H 'Content-Type: application/json' \
  -d '{
    "section_ids":["demo-auto-recovery-bounds"],
    "tenant_id":"commerce-demo",
    "jurisdiction":"US",
    "channel":"online",
    "as_of":"2026-08-20T18:00:00Z",
    "release_id":"commerce-us-2026-08-20",
    "max_depth":1,
    "limit":20
  }' | jq .
```

Direct result:

```json
{
  "success": true,
  "data": {
    "paths": [
      {"relation_type":"ESCALATES_TO","to":{"section_id":"demo-approval-thresholds"},"depth":1},
      {"relation_type":"FOLLOWED_BY","to":{"section_id":"demo-verification-notification"},"depth":1},
      {"relation_type":"REQUIRES","to":{"section_id":"demo-evidence-precedence"},"depth":1}
    ]
  }
}
```

### Prove that scope is enforced before ranking

The same search with `tenant_id: "wrong-tenant"` returned HTTP 200 with no
candidates:

```json
{
  "success": true,
  "data": {
    "candidates": [],
    "warnings": [
      "no evidence matched the requested scope and effective date"
    ]
  }
}
```

An exact read of the online-only replacement section with `channel: "store"`
returned HTTP 404:

```json
{
  "success": false,
  "error": {
    "code": "NOT_FOUND",
    "message": "knowledge record not found",
    "retryable": false
  }
}
```

This is an important distinction. The gateway does not retrieve everything and
then ask the LLM to ignore the wrong material. It removes out-of-scope material
before scoring and refuses an out-of-scope exact read.

## 7. Startup: how `pack.json` becomes a running store

```text
pack.json
   │
   ├─ strict JSON decode; unknown fields fail
   ├─ validate sources, section IDs, dates, relations, aliases, golden cases
   ├─ canonicalize and verify SHA-256 release hash
   ├─ derive live source_count=7 and section_count=18
   │
   ├─ in-memory reference view
   │    ├─ source map
   │    ├─ section map
   │    ├─ BM25-style ranking
   │    └─ deterministic alias-vector ranking
   │
   └─ LadybugDB projection in the same pod
        ├─ Section nodes
        └─ typed LINK edges
```

The source file stores `section_count` and `source_count` as zero so that they
do not become independently maintained facts. The loader derives 18 and 7 from
the arrays after validation and includes those derived values in the canonical
hash.

## 8. Search: the exact algorithm

The request passes through these deterministic stages:

```text
JSON request
  → require query + tenant + jurisdiction + channel + as_of
  → pin active or requested immutable release
  → hard-filter sections by tenant, release, jurisdiction, channel, date, tags
  → rank filtered sections in two lanes
       ├─ BM25-style lexical score
       └─ alias-aware 128-bucket cosine score
  → reciprocal-rank fusion, k=60
  → named-source preference when a source is explicitly named
  → cap to requested limit, default 8 and maximum 20
  → attach at most 20 authored one-hop relation paths
  → return candidates + sources + paths + release provenance
```

### Lexical lane

The lexical lane lowercases and tokenizes the title, section content, and tags.
It removes a small stop-word list and applies a small suffix stemmer. It then
calculates a BM25-style term score and multiplies it by a reviewed priority
boost.

### Deterministic “semantic” lane

This lane is not neural search. It:

1. replaces reviewed aliases, such as `reship → replacement`;
2. stems the terms;
3. hashes each term into one of 128 numeric buckets;
4. hashes adjacent term pairs with half weight;
5. compares query and section vectors using cosine similarity.

It is cheap, deterministic, local, and explainable. It is also limited. It
cannot discover domain meaning that is absent from the reviewed alias map and
the section text.

### Fusion

Each candidate receives:

```text
1 / (60 + lexical_rank) + 1 / (60 + semantic_rank)
```

This explains why the displayed final values are close together. They are rank
fusion values, not probabilities and not confidence percentages.

## 9. LadybugDB's precise role

LadybugDB runs as an embedded library in the gateway process. It is not a
separate pod.

At startup the adapter creates:

- one `Section` node per pack section;
- one `LINK` edge per authored relation, with `relation_type` as a property.

In this implementation:

- filtering and relevance ranking use the portable in-memory reference store;
- exact reads are scoped in memory and then the section node is confirmed in
  LadybugDB;
- traversal is scoped in memory and then the expected edge triples are
  confirmed in LadybugDB.

All callers depend on the private `KnowledgeStore` interface, not on LadybugDB.
That interface contains release lookup, lexical search, vector search,
traversal, exact read, and close operations. A future Neo4j adapter can replace
the provider without changing the four public gateway capabilities.

## 10. What is AI and what is not

| Activity | Owner | AI? |
|---|---|---|
| Form the evidence question | Order Operations Agent | Yes, LLM-guided |
| Choose which candidates are decisive | Order Operations Agent | Yes, constrained by its skill |
| Validate scope | Gateway | No |
| Rank candidates | Gateway | No |
| Follow relations | Gateway | No |
| Issue signed exact-read receipts | Gateway | No |
| Decide `allowed` or `denied` | Commerce Core | No |
| Mutate the order | Commerce Core | No |

The AI is useful because a natural-language event can require an adaptive
investigation. The non-AI boundaries prevent the model from inventing policy or
granting itself permission.

## 11. How this relates to a large real-world corpus

This demo pack is only about 26 KB. A previous 2–10 MB figure was a rough
laptop-demo sizing expectation, not a requirement and not a credible estimate
for a large enterprise corpus.

A real company may have gigabytes or terabytes of raw documents. Those raw
files should not be placed in a skill or loaded into one prompt. A production
knowledge-foundry pipeline would:

1. acquire and version approved source documents;
2. parse, OCR, and normalize them offline;
3. split them into stable, reviewable sections;
4. attach tenant, jurisdiction, channel, product, seller, and effective dates;
5. create searchable lexical and embedding indexes;
6. create or curate explicit relationships;
7. publish an immutable release and evaluate it with golden cases;
8. expose the same bounded gateway API.

The public contract can stay the same while the implementation changes from an
in-memory view and embedded LadybugDB to production search, vector, document,
and graph services. Accuracy will depend far more on source coverage,
normalization quality, metadata, conflict handling, and evaluations than on the
choice of graph database alone.

## 12. The most important conclusion

The live execution proves this path:

```text
agent asks a scoped question
  → gateway returns ranked candidates and authored links
  → agent chooses three decisive section IDs
  → gateway exact-reads and signs those sections
  → Commerce Core verifies the receipts and applies fixed business rules
  → the command executes only after that boundary
```

The gateway is therefore neither a “dumb search box” nor an autonomous policy
decision-maker. It is a controlled evidence boundary between adaptive AI
reasoning and deterministic business authority.

Return to the [tutorial index](README.md), the
[illustrated tutorial](index.html), or the
[live-evidence guide](08-observability.md).
