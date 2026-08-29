# Registry Viewer and Jaeger Observability Audit — Agentic Commerce

**Audit status:** RV-OBS-001 through RV-OBS-007, RV-OBS-009, and RV-OBS-010
resolved; live APIs and deployed assets verified on 2026-08-27; AC-RET-001 and
RV-OBS-008 remain open outside this remediation\
**Captured at:** 2026-08-21T22:49:42Z\
**Environment:** Local Kind cluster, namespace `truvag3-examples`\
**Audit purpose:** Preserve reproducible proof of discrepancies found while
reconciling one Agentic Commerce execution across Registry Viewer and Jaeger.

**Repository note:** This audit was captured before the domain example moved
from `truva-g3/examples/agentic-commerce` into
`truva-g3-domain-examples/agentic-commerce`. Historical commands and captured
paths below retain their original spelling as audit evidence. Source links now
target the relocated example or the expected sibling `truva-g3` checkout.

## Audited execution

| Identifier | Value |
|---|---|
| Scenario command | `./setup.sh demo usps-express-late` |
| Task ID | `order-event-demo-usps-express-late-1787325104` |
| Execution request ID | `order-operations-1787325104910014933` |
| Execution trace ID | `a297affcd9f89557ec1552ca6c4c469a` |
| Trigger trace ID | `36be3fe5ef27ebaf134effbf1a0948ad` |
| Trigger span ID | `840d753a609ea2eb` |
| Command ID | `c2710f34598efe526628af82fca73654` |
| Notification ID | `f23634363a1c0af7a3064cdfe04be418` |

The setup script constructs a unique event ID and posts the scenario to
`/webhook/order-risk`; see
[`agentic-commerce/setup.sh`](../../setup.sh#L420-L435).
The receiver creates and enqueues the asynchronous task and returns HTTP 202
with its status URL; see
[`agentic-commerce/order-operations-agent/webhook.go`](../../order-operations-agent/webhook.go#L145-L177).

## Evidence sources

The audit used these read-only API surfaces:

- `GET http://registry.localhost/api/health`
- `GET http://registry.localhost/api/services`
- `GET http://registry.localhost/api/executions/{request_id}`
- `GET http://registry.localhost/api/executions/{request_id}/unified`
- `GET http://registry.localhost/api/executions/{request_id}/dag`
- `GET http://registry.localhost/api/llm-debug/{request_id}`
- `GET http://registry.localhost/api/hitl/checkpoints`
- `GET http://jaeger.localhost/api/traces/{trace_id}`

Direct links while the local data remains retained:

- [Registry unified execution](http://registry.localhost/api/executions/order-operations-1787325104910014933/unified)
- [Registry DAG](http://registry.localhost/api/executions/order-operations-1787325104910014933/dag)
- [Registry LLM record](http://registry.localhost/api/llm-debug/order-operations-1787325104910014933)
- [Jaeger execution trace](http://jaeger.localhost/trace/a297affcd9f89557ec1552ca6c4c469a)
- [Jaeger trigger trace](http://jaeger.localhost/trace/36be3fe5ef27ebaf134effbf1a0948ad)

Registry debug data and Jaeger traces have finite retention. The sanitized
excerpts below are the durable audit evidence. Full responses were deliberately
not copied into this document because they contain complete LLM prompts,
responses, signed evidence receipts, and decision tokens.

## Confirmed execution integrity

The business execution itself is complete and internally consistent. The set
of step IDs is identical across the stored plans, runtime results, Registry DAG,
and Jaeger tool-server spans:

```json
{
  "planned": ["step-1", "step-2", "step-3", "step-4", "step-5", "step-6", "step-7", "step-8", "step-9"],
  "results": ["step-1", "step-2", "step-3", "step-4", "step-5", "step-6", "step-7", "step-8", "step-9"],
  "dag": ["step-1", "step-2", "step-3", "step-4", "step-5", "step-6", "step-7", "step-8", "step-9"],
  "trace_tool_servers": ["step-1", "step-2", "step-3", "step-4", "step-5", "step-6", "step-7", "step-8", "step-9"],
  "all_equal": true
}
```

Each tool step has a linked orchestrator step span, agent HTTP client span, and
tool HTTP server span. All nine client and server calls returned HTTP 200:

| Step | Phase | Tool | Capability | Client | Server |
|---|---:|---|---|---:|---:|
| `step-1` | 1 | `commerce-core` | `get_order_context` | 200 | 200 |
| `step-2` | 1 | `commerce-core` | `list_fulfillment_options` | 200 | 200 |
| `step-3` | 2 | `domain-knowledge-gateway` | `search_evidence` | 200 | 200 |
| `step-4` | 3 | `domain-knowledge-gateway` | `read_exact_section` | 200 | 200 |
| `step-5` | 3 | `domain-knowledge-gateway` | `read_exact_section` | 200 | 200 |
| `step-6` | 3 | `commerce-core` | `evaluate_recovery_policy` | 200 | 200 |
| `step-7` | 4 | `commerce-core` | `execute_recovery_command` | 200 | 200 |
| `step-8` | 4 | `commerce-core` | `verify_order_outcome` | 200 | 200 |
| `step-9` | 4 | `commerce-core` | `send_synthetic_notification` | 200 | 200 |

Other reconciled facts:

- Registry recorded four phase plans and nine successful runtime results.
- Jaeger recorded 110 spans: 101 from the agent, six from Commerce Core, and
  three from Domain Knowledge Gateway.
- Jaeger contained zero error spans and no HTTP status at or above 400.
- Registry and Jaeger both recorded 13 successful OpenAI calls.
- Token totals match exactly: 68,406 input tokens and 5,653 output tokens.
- The skill record shows
  `order-operations/evidence-grounded-recovery` version 2, a verified manifest
  hash, one activation, five verified content loads, and projections for
  planning, continuation, regeneration, and synthesis.
- Phase 4 initially failed plan validation because `step-9` referenced
  `step-7` without declaring it in `depends_on`. Registry captured the original
  and regenerated plan IDs plus the validation error. The regenerated plan
  correctly declares `step-9` dependencies on `step-7` and `step-8`.
- `has_hitl_data=false` and the HITL list was empty. This is correct for this
  execution because deterministic evaluation returned
  `approval_required=false`.
- The three participating services were present in the service registry as
  healthy with the expected capability counts: agent 2, Commerce Core 6, and
  Domain Knowledge Gateway 4.

### Reviewer's comment

Independently re-verified against the live Registry Viewer APIs, the Jaeger
trace, and the source. Every claim in this section reproduced exactly, with no
corrections:

- Span counts: 101 agent, six Commerce Core, three Domain Knowledge Gateway,
  110 total.
- `task.process` 70,677,352 µs; `orchestrator.process_request` 70,675,644 µs.
- `/api/llm-debug` returns 13 interactions, all successful, all `openai`,
  summing to 68,406 prompt and 5,653 completion tokens.
- Zero error spans; the only HTTP status present anywhere in the execution
  trace is 200.
- Stored phase plans confirm the scheduling structure: phase 1 `step-1` and
  `step-2`; phase 2 `step-3`; phase 3 `step-4` and `step-5` then `step-6`;
  phase 4 `step-7` then `step-8` then `step-9`, with `step-9` declaring
  `depends_on` of both `step-7` and `step-8`.
- Skills record: version 2, one activation, five manifest content loads, and
  six projections covering planning, continuation, regeneration, and synthesis.
- Service registry reports all three services `healthy` with capability counts
  of 2, 6, and 4.
- The trigger trace contains exactly one span, HTTP 202, 2,241 µs, referenced
  from `task.process` by `FOLLOWS_FROM` span `840d753a609ea2eb`.

The `setup.sh` and `webhook.go` line references both resolve to the code they
are cited for.

## Finding RV-OBS-001 — Total duration is double-counted

**Classification:** Registry Viewer display accuracy\
**Severity:** High for performance analysis\
**Status:** Resolved; live API and deployed asset verified 2026-08-27

### Observed

The execution-list API returned:

```json
{
  "total_duration_ms": 59343,
  "llm_total_duration_ms": 70555
}
```

The UI adds these values and displays approximately 129.898 seconds:

```text
59,343 ms + 70,555 ms = 129,898 ms
```

Jaeger shows the actual asynchronous task wall-clock duration as 70,677,352
microseconds, or approximately 70.677 seconds. The nested
`orchestrator.process_request` span is 70,675,644 microseconds.

`total_duration_ms` is the phase-loop/execution-result duration. It already
contains planning, continuation planning, micro-resolution, and tool work that
occurred before final synthesis. `llm_total_duration_ms` aggregates all 13 LLM
calls, including those already inside that interval. The values are therefore
not additive.

### Code proof

The UI explicitly treats them as additive in:

- [`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L894-L896)
- [`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L968)
- [`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L1182)
- [`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L2003-L2007)
- [`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L4240-L4249)

The unified backend derives `total_duration_ms` directly from
`ExecutionResult.TotalDuration` in
[`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go#L2432-L2436); it does not define that value as tool-only
time.

### Reproduction

```bash
curl -sS 'http://registry.localhost/api/executions?limit=200' |
  jq '.executions[] |
      select(.request_id=="order-operations-1787325104910014933") |
      {total_duration_ms, llm_total_duration_ms,
       displayed_sum:(.total_duration_ms + (.llm_total_duration_ms // 0))}'
```

```bash
curl -sS 'http://jaeger.localhost/api/traces/a297affcd9f89557ec1552ca6c4c469a' |
  jq '.data[0].spans[] |
      select(.operationName=="task.process" or
             .operationName=="orchestrator.process_request") |
      {operation:.operationName,duration_us:.duration}'
```

### Expected behavior and acceptance criteria

- Display a clearly named wall-clock duration for the request or task.
- Keep aggregate LLM duration as a separate diagnostic metric.
- Do not add overlapping durations.
- For this record, the primary end-to-end value should be approximately
  70.677 seconds, not 129.898 seconds.

### Reviewer's comment

Confirmed. All five cited frontend sites do add the two values, and the
severity rating is appropriate.

The overlap is quantifiable rather than approximate. `result.total_duration` is
59,343.02 ms, which equals wall clock minus final synthesis
(70,675.6 − 11,320.9 = 59,354.7 ms). The 13 LLM spans sum to 70,557.5 ms, of
which 59,236.6 ms fall inside phases 1 through 4. Essentially the entire
59,343 ms interval is LLM time that `llm_total_duration_ms` counts a second
time, so the displayed 129,898 ms overstates wall clock by roughly 59.2
seconds — the size of the overlap, not a rounding artifact.

Worth adding to the remediation: the root cause is a stated premise, not an
oversight. [`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L2003-L2004)
carries the comment "These are additive because executor handles tool
invocations, LLM handles AI planning/synthesis". Unless that comment is
corrected alongside the arithmetic, the same defect is likely to be
reintroduced.

One design consideration for the replacement metric: this execution is
approximately 99.8% LLM time (70,557 ms of 70,677 ms), with all nine tool calls
totalling about 50 ms. An "executor time plus LLM time" split will always be
dominated by the LLM component, so a wall-clock figure with LLM time expressed
as a share of it communicates more than two side-by-side components.

## Finding RV-OBS-002 — The backend DAG API reports zero step durations

**Classification:** Registry Viewer API accuracy\
**Severity:** Medium\
**Status:** Resolved; live API and deployed asset verified 2026-08-27

### Observed

Raw execution results populate `duration` in nanoseconds and leave
`duration_ms` absent. Examples:

```json
[
  {
    "step_id": "step-3",
    "duration": 26518421,
    "duration_ms": null
  },
  {
    "step_id": "step-6",
    "duration": 6918398341,
    "duration_ms": null
  },
  {
    "step_id": "step-9",
    "duration": 1760287430,
    "duration_ms": null
  }
]
```

The `/api/executions/{request_id}/dag` response reports `duration_ms: 0` for
all nine nodes.

### Code proof

The backend DAG builder reads only `result.DurationMs` in
[`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go#L2020-L2050). The frontend already contains the required
nanosecond fallback in
[`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L2360-L2365), which proves
that both stored forms are expected.

### Reproduction

```bash
curl -sS \
  'http://registry.localhost/api/executions/order-operations-1787325104910014933' |
  jq '.result.steps | map({step_id,capability,duration,duration_ms})'
```

```bash
curl -sS \
  'http://registry.localhost/api/executions/order-operations-1787325104910014933/dag' |
  jq '.nodes | map(select((.node_type // "step")=="step") |
                       {id,duration_ms})'
```

### Expected behavior and acceptance criteria

- When `duration_ms` is absent, convert `duration / 1_000_000`.
- The backend DAG API and frontend step view must report the same duration.
- Add a backend test using a `StepResult` populated only through `Duration`.

### Reviewer's comment

Confirmed, and the framing understates the scope.

`duration_ms` is not occasionally absent; it can never be present. The upstream
producer type `orchestration.StepResult` declares only
`Duration time.Duration` — see
[`../../orchestration/interfaces.go`](../../../../truva-g3/orchestration/interfaces.go#L220).
There is no `DurationMs` field anywhere upstream, so although the viewer's own
`StepResult` declares one with `omitempty`, the field is structurally
unreachable for every live execution rather than missing from this record in
particular. All nine steps here omit the JSON key entirely.

The reason this reached production is visible in the viewer itself: the mock
fixtures do populate `DurationMs`
(see [`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go#L2833)), so the DAG API returns plausible durations in
mock mode and zeros against live Redis. Any regression test that exercises only
the mock path will continue to pass.

Suggested amendment to the acceptance criteria: rather than "when `duration_ms`
is absent, convert", state that `duration_ms` is always absent for live
executions and that `duration` in nanoseconds is the sole source of truth. The
proposed backend test is the right one and would have caught this.

## Finding RV-OBS-003 — Multi-phase DAG statistics imply impossible parallelism

**Classification:** Registry Viewer API and Steps-view topology accuracy\
**Severity:** High for execution analysis\
**Status:** Resolved; live API and deployed asset verified 2026-08-27

### Observed

The backend DAG response reports:

```json
{
  "depth": 3,
  "max_parallelism": 6,
  "levels": [
    ["step-1", "step-2", "step-3", "step-4", "step-5", "step-7"],
    ["step-6", "step-8"],
    ["step-9"]
  ]
}
```

This makes steps from four different phases appear runnable together. The
stored phase plans prove the actual scheduling structure was:

```text
Phase 1: step-1 || step-2
Phase 2: step-3
Phase 3: step-4 || step-5 -> step-6
Phase 4: step-7 -> step-8 -> step-9
                         \----------> step-9
```

The maximum actual same-phase tool parallelism was two, not six.

### Code proof

The backend computes levels from `depends_on` before phase-transition nodes and
edges are inserted; see [`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go#L1962-L2012). Phase transitions are
added later in [`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go#L2065-L2140), after `depth`, node levels, and
the level sets have already been determined.

The frontend Full Flow mode has a more accurate phase-local implementation in
[`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L2478-L2501). However, the
Steps view still computes a global level set in
[`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L2386-L2392), and the backend
DAG API remains inaccurate.

### Reproduction

```bash
curl -sS \
  'http://registry.localhost/api/executions/order-operations-1787325104910014933/dag' |
  jq '{statistics,levels,edges}'
```

```bash
curl -sS \
  'http://registry.localhost/api/executions/order-operations-1787325104910014933' |
  jq '.phase_plans | map({phase_number,
                          step_ids:[.steps[].step_id],
                          dependencies:[.steps[] |
                            {step_id,depends_on:(.depends_on // [])}]})'
```

### Expected behavior and acceptance criteria

- DAG levels and statistics must respect phase sequencing.
- `max_parallelism` must be computed within executable phase boundaries or
  from a full graph containing phase-transition edges.
- The backend API, Steps view, and Full Flow view must use compatible topology
  semantics.
- For this execution, no statistic may imply that steps from separate phases
  ran concurrently; same-phase maximum parallelism is two.

### Reviewer's comment

Confirmed on both the backend API and the Steps view; the code proof is
accurate and the reported statistics reproduce exactly.

Mechanism verified in detail: `levels` is built by Kahn's algorithm over
`depends_on` alone; the phase-transition edges are appended to `edges`
afterwards and are never fed back into `inDegree` or `levels`; `max_parallelism`
is then computed from that stale array at [`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go#L2144-L2147). The
phase edges are present in the response, so the graph carries the correct
sequencing while the derived statistics do not reflect it.

A second symptom appears in the same response: `statistics.total_nodes` is 9
while the `nodes` array holds 12. The statistics block is finalized before the
three `phase_boundary` nodes are appended, so the node count and the node array
disagree.

The Steps-view claim holds, and the mechanism is worth recording because it is
not obvious from the cited lines alone: the Steps view is not reading a
single-phase plan. [`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js#L1537-L1541)
replaces `selected.plan.steps` with `phase_plans.flatMap(...)` when the
execution has more than one phase, so `computeStepLevels(selected.plan.steps)`
receives all nine steps carrying phase-local `depends_on` and reproduces the
identical level-0 collapse of `step-1`, `step-2`, `step-3`, `step-4`, `step-5`,
and `step-7`. The backend API and the Steps view therefore share one root
cause, and the phase-scoped implementation in Full Flow is the correct
reference for both.

## Finding RV-OBS-004 — Registry Viewer does not surface the asynchronous trigger trace

**Classification:** Trace-navigation completeness\
**Severity:** Medium\
**Status:** Resolved; live API and deployed asset verified 2026-08-27

### Observed

The Registry unified response exposes only the execution trace:

```json
{
  "request_id": "order-operations-1787325104910014933",
  "trace_id": "a297affcd9f89557ec1552ca6c4c469a"
}
```

Jaeger shows that the root-like `task.process` span in that trace has this
cross-trace reference:

```json
{
  "operation": "task.process",
  "task.id": "order-event-demo-usps-express-late-1787325104",
  "reference": {
    "refType": "FOLLOWS_FROM",
    "traceID": "36be3fe5ef27ebaf134effbf1a0948ad",
    "spanID": "840d753a609ea2eb"
  }
}
```

The linked trigger trace contains one server span:

```json
{
  "operation": "HTTP POST /webhook/order-risk",
  "http.request.method": "POST",
  "url.path": "/webhook/order-risk",
  "http.response.status_code": 202,
  "duration_us": 2241
}
```

The trace data is correct, but a user starting in Registry Viewer cannot see or
navigate to the submission trace or task ID without manually inspecting the
execution trace in Jaeger.

### Code proof

`UnifiedExecutionView` has one `TraceID` field and no linked-trace or task-ID
fields; see [`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go#L402-L445). `buildUnifiedView` copies only
`execution.TraceID`; see [`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go#L2414-L2430).

### Reproduction

```bash
curl -sS \
  'http://registry.localhost/api/executions/order-operations-1787325104910014933/unified' |
  jq '{request_id,trace_id,top_level_keys:keys}'
```

```bash
curl -sS \
  'http://jaeger.localhost/api/traces/a297affcd9f89557ec1552ca6c4c469a' |
  jq '.data[0].spans[] |
      select(.operationName=="task.process") |
      {operation:.operationName,references,
       tags:(reduce (.tags[]?) as $tag
             ({}; .[$tag.key]=$tag.value))}'
```

### Expected behavior and acceptance criteria

- Surface the asynchronous task ID in the unified execution view.
- Surface linked trigger trace IDs, at least for `FOLLOWS_FROM` relationships.
- Provide navigation from the execution to both the processing trace and the
  originating submission trace.
- Keep this optional so synchronous executions remain backward-compatible.

### Reviewer's comment

Confirmed. `UnifiedExecutionView` carries a single `TraceID` with no task-ID or
linked-trace field, `buildUnifiedView` copies only `execution.TraceID`, and the
trigger-trace facts reproduce exactly: one span, `POST /webhook/order-risk`,
HTTP 202, 2,241 µs, referenced by `FOLLOWS_FROM`.

One qualification on scope, which supports rather than weakens the finding: the
task ID is not lost anywhere else. It is carried on the `task.process` span as
the `task.id` tag, and the webhook returns it to the submitting caller inside
`status_url` — see
[`../agentic-commerce/order-operations-agent/webhook.go`](../../order-operations-agent/webhook.go#L176-L177).
Registry Viewer is the only surface from which it cannot be reached, so the gap
is navigational exactly as described.

Implementation note: `task.process` carries both the `task.id` tag and the
`FOLLOWS_FROM` reference, so one span lookup supplies both proposed fields.

## Observation AC-RET-001 — Explicit USPS search still returned peer and unrelated sources

**Classification:** Execution-quality observation; not a Registry Viewer defect\
**Severity:** Medium for retrieval precision\
**Status:** Open for Agentic Commerce

### Observed

Registry correctly preserved the complete `search_evidence` response. The two
USPS sections ranked first, but a limit-20 explicit USPS query returned nine
candidates, including FedEx and merchant-return policies:

```json
[
  "usps-priority-express-claim-window",
  "usps-priority-express-eligibility",
  "demo-evidence-precedence",
  "fedex-guarantee-service-scope",
  "walmart-general-return-window",
  "demo-approval-thresholds",
  "target-general-return-window",
  "target-electronics-return-window",
  "walmart-electronics-marketplace-exceptions"
]
```

The agent exact-read only the two correct USPS sections, so the deterministic
decision was not contaminated. This is nevertheless a precision issue worth
retaining as an audit observation.

The likely cause is an identity-alias mismatch: the query uses `USPS`, while
source affinity tokenizes the publisher and title, which contain `United States
Postal Service` and `Domestic Mail Manual` rather than the acronym. This is an
inference from the captured query, candidate list, pack metadata, and
source-affinity implementation; it is not a demonstrated Registry Viewer bug.

### Reproduction

```bash
curl -sS \
  'http://registry.localhost/api/executions/order-operations-1787325104910014933' |
  jq -r '.result.steps[] |
         select(.step_id=="step-3") |
         (.response | fromjson) |
         [.data.candidates[].section.section_id]'
```

### Expected behavior and acceptance criteria

- Add domain-pack source identity aliases or an equivalent normalized identity
  mechanism without hard-coding commerce brands into the gateway.
- An explicit USPS query should exclude peer-carrier evidence such as FedEx
  unless the query asks for a comparison.
- Preserve the two USPS sections as the highest-ranked decisive results.

### Reviewer's comment

The observation is accurate and the candidate list reproduces exactly,
including its order. The agent did exact-read only the two USPS sections, so
the conclusion that the deterministic decision was uncontaminated holds.

The stated cause does not hold, and the proposed remediation would not change
the result. Running the gateway's own tokenizer and stemmer over the captured
query and the pack's source strings gives the following affinity values, which
`preferExplicitSourceMatches` computes per source over `Publisher + " " + Title`:

| Source | `source_type` | Affinity | Matched terms |
|---|---|---:|---|
| `usps-dmm-604` | carrier-public-policy | 2 | `mail`, `service` |
| `fedex-money-back-guarantee` | carrier-public-policy | 1 | `service` |
| `target-return-policy` | merchant-public-policy | 0 | — |
| `walmart-return-policy` | merchant-public-policy | 0 | — |

The elimination rule removes a candidate only when
`maxByType[source_type] > 0 && affinity[source_id] == 0`. Two consequences
follow:

1. USPS was never unmatched. It scored highest within its source type, through
   the generic tokens `mail` and `service` rather than the acronym. The absence
   of `USPS` from the publisher and title did not affect this outcome.
2. FedEx cannot be eliminated by adding aliases. Its affinity is 1, earned from
   the generic token `service`, and the rule requires exactly 0. Raising USPS
   from 2 to 3 leaves FedEx untouched, so the acceptance criterion "an explicit
   USPS query should exclude peer-carrier evidence such as FedEx" would still
   fail after implementing the stated fix.

The underlying defect is that generic vocabulary is counted as identity
evidence. A corrective fix must either restrict affinity to distinctive
identity tokens or replace the zero test with a relative margin. Note also that
`preferExplicitSourceMatches` calls `tokenize(query, nil)` with nil aliases, so
the pack's existing `semantic_aliases` map is not consulted on this path;
adding source aliases would require wiring as well as pack data.

The observation also merges two distinct mechanisms. FedEx survived because it
holds nonzero generic affinity, whereas Target and Walmart survived because
their entire source type scored zero, leaving the rule inert for merchants.
These require different remedies.

Separately, the pool description deserves a qualification. This was issued as a
limit-20 query, but `HybridSearch` derives a per-retriever limit of 20 × 3 = 60,
and `rankCandidates` treats any limit above 50 the same as zero and resets it to
10. Each retriever therefore contributed at most 10 candidates before fusion.
Nothing decisive was lost here, since the release holds 16 sections and both
USPS sections ranked first, but precision conclusions drawn from this result set
describe a 10-plus-10 pool rather than a 60-plus-60 one.

## Follow-up live verification — 2026-08-22

The control-tower implementation was rebuilt through
`examples/agentic-commerce/setup.sh rebuild` and manually exercised after the
scenario-groundwork changes. The five operator-paired scenarios were
deliberately not triggered: conflicting evidence, insufficient evidence,
policy changed during HITL, state changed during HITL, and live alternate-domain
switching. Their fixtures, exact expected outcomes, control actions, component
tests, and operator runbook are present under
`examples/agentic-commerce/scenarios/`.

### Approval and immediate resume

| Identifier | Value |
|---|---|
| Event/task | `manual-approval-governed-20260822-a10` / `order-event-manual-approval-governed-20260822-a10` |
| Initial request / trace | `order-operations-1787431818524531555` / `524e1fa80d7e5f6e5ffc21523966c956` |
| Checkpoint | `cp-171eded2-66d8-42` |
| Resume request / trace | `order-operations-1787431933621096526` / `207425146712467f4bdb4c10f0f2326c` |
| Command / notification | `fe9dca8f095a40f98b140129ad12c606` / `994deeea095c5748b0be2aa97c86dac3` |

The initial execution recorded the required skill at version 8, one activation,
nine successful pre-checkpoint steps, and a before-step checkpoint containing
the complete chain:

```json
[
  {"step_id":"step-9-governed-execute","capability":"execute_recovery_command"},
  {"step_id":"step-9-governed-verify","capability":"verify_order_outcome","depends_on":["step-9-governed-execute"]},
  {"step_id":"step-9-governed-notify","capability":"send_synthetic_notification","depends_on":["step-9-governed-execute","step-9-governed-verify"]}
]
```

Before approval, `ORD-1004` was version 1 with
`fulfillment_status=missing_in_transit` and no recovery. The approved resume
returned HTTP 200 with `terminal_outcome=recovered`,
`policy_outcome=approval_required`, a deterministic governed response, one
completed command, `verified=true`, and one notification. The final order was
version 2 with `fulfillment_status=replacement_created` and the same command
ID. The resume Registry record contains all 12 accumulated steps, with the last
three marked `plan_source=hitl_resume`. Jaeger contains 114 spans for the
initial execution and 34 for resume; the resume trace has zero error spans.

The initial trace retained one rejected traversal attempt as two error spans
(client and server) with HTTP 400 and `traversal bounds are outside the
supported range`; a later attempt succeeded. This was an application planning
issue, not lost telemetry. The final plan guard now binds the one-hop traversal
to `max_depth=1` and `limit=20`; the post-fix duplicate trace below proves the
same traversal completes once with no error span.

### Deterministic denial

| Identifier | Value |
|---|---|
| Event/task | `manual-denied-20260822-d1` / `order-event-manual-denied-20260822-d1` |
| Request / trace | `order-operations-1787432041838325142` / `92f9a8a46fc8acfb587ee4f12de92668` |

The task returned `terminal_outcome=denied` and `policy_outcome=denied`, with
the reason `restricted merchandise blocks replacement:
lithium_battery_air_transport`. Registry recorded skill version 8 and eight
successful read/evaluation steps, with zero execute, verify, or notification
steps. `ORD-1005` remained version 1, restricted, and without a recovery
record. Jaeger contained 107 spans and no mutation operation. As in the
approval run, it retained one recovered, pre-fix traversal validation attempt.

### Duplicate active and completed delivery

| Identifier | Value |
|---|---|
| Event/task | `manual-duplicate-20260822-u1` / `order-event-manual-duplicate-20260822-u1` |
| Request / trace | `order-operations-1787432364863440680` / `86950528276054615c029fda63d0dd2f` |
| Command / notification | `1985a4ff9205291e0c04b6622896a9f1` / `2e4af527afd9fc8db13274bc32104b29` |

The first delivery returned HTTP 202. An immediate identical delivery returned
HTTP 409 with `task_status=running`; a third identical delivery after completion
returned HTTP 409 with `task_status=completed`. Agent logs contain one
`status=accepted` event and the two corresponding `status=duplicate` events for
the same event and task IDs.

The accepted task returned `terminal_outcome=recovered` and
`policy_outcome=allowed`. Registry recorded exactly one execute, one verify, and
one notification. `ORD-1003` changed once from version 1 to version 2 and holds
one recovery record with the command ID above. The Jaeger trace contains 140
spans across all three services, skill pin/activation/load spans, one traversal,
one command, one verification, one notification, and zero error spans.

## Finding RV-OBS-005 — HITL lifecycle is split into unreconciled execution snapshots

**Classification:** Registry Viewer HITL accuracy and navigation\
**Severity:** High for approval audit\
**Status:** Resolved; live API and deployed asset verified 2026-08-27

The initial approval execution's unified record continues to report:

```json
{
  "request_id": "order-operations-1787431818524531555",
  "has_hitl_data": true,
  "checkpoint": {"checkpoint_id":"cp-171eded2-66d8-42","status":"pending"}
}
```

The live checkpoint endpoint reports that same checkpoint as `completed`. The
resume execution, which has the correct `original_request_id`, reports:

```json
{
  "request_id": "order-operations-1787431933621096526",
  "original_request_id": "order-operations-1787431818524531555",
  "has_hitl_data": false,
  "checkpoint": null
}
```

Jaeger independently proves this second execution is a HITL resume through the
`hitl.resume` span and `HTTP POST /hitl/resume/cp-171eded2-66d8-42`. The stored
initial snapshot is useful historical truth, but presenting it as the only
checkpoint status makes a completed approval appear pending, while the resume
record appears unrelated to HITL unless the operator manually joins IDs.

Acceptance criteria:

- Preserve the interruption snapshot while also exposing current checkpoint
  status.
- Mark the resume execution as HITL-derived and surface its checkpoint ID.
- Link initial and resume DAGs in both directions using `original_request_id`
  and checkpoint identity.

## Finding RV-OBS-006 — Unified checkpoint projection drops `implicit_deps`

**Classification:** Registry Viewer DAG provenance\
**Severity:** Medium\
**Status:** Resolved; live API and deployed asset verified 2026-08-27

The authoritative checkpoint returned by the agent includes this prior-phase
dependency:

```json
{
  "step_id":"step-9-governed-execute",
  "implicit_deps":["step-9"],
  "decision_token":"{{step-9.response.data.decision_token}}"
}
```

The Registry unified projection of the same checkpoint and step returns
`implicit_deps: null`. The execute step therefore retains its parameter
template but loses the declared data-provenance edge to the deterministic
evaluation. Preserve `implicit_deps` in the backend model and render it as a
cross-phase data edge distinct from same-phase scheduler dependencies.

## Finding RV-OBS-007 — Registry exposes the pre-hook model draft, not the governed final response

**Classification:** Registry Viewer response semantics\
**Severity:** High when model narration conflicts with deterministic tools\
**Status:** Resolved; live API and deployed asset verified 2026-08-27

The earlier approved resume `order-operations-1787398175602150032` succeeded,
but its stored synthesis draft began with:

```text
Outcome: insufficient_evidence — no mutation should be treated as authorized or completed.
```

The actual deterministic tool results showed an approved, executed, verified
recovery. This was the contradictory narrative observed in the Execution DAG's
LLM Calls section. The application now records tool results into a safe audit
projection and replaces synthesis output after the LLM call. The verified
resume `order-operations-1787431933621096526` returned a governed final response
starting `Governed order recovery outcome: recovered`, and its raw model draft
happened to agree.

Registry still persists and displays only the raw LLM interaction; it does not
persist the post-hook response returned by the application. A future
contradiction would therefore remain visible without a clearly labelled
authoritative final response beside it.

Acceptance criteria:

- Label LLM synthesis content as an untrusted draft.
- Persist and display the post-hook application response separately.
- When the two differ, make the governed response visually authoritative and
  retain the draft solely for diagnosis.

## Resolved observation AC-PLAN-002 — Approval decision could terminate before checkpoint creation

The failed safe run `order-operations-1787427155435614501` (trace
`06582030380776521ec9340f8dfd6758`) recorded an authoritative
`approval_required` evaluation, followed by an empty terminal phase and
`has_hitl_data=false`. The task failed with `approval-required recovery plan
ended without a governed HITL checkpoint`, and no order mutation occurred.

The request-scoped governed plan hook now converts authoritative `allowed` or
`approval_required` decisions into the exact execute/verify/notify chain and
forces denied, insufficient, or conflicting decisions to a zero-step terminal
plan. The approval and duplicate executions above prove both execution classes;
the denial execution proves the no-mutation class.

## Paired live verification of the five reserved scenarios — 2026-08-22

The five operator-paired scenarios in `examples/agentic-commerce/scenarios`
were executed manually against the local Kind deployment. The Commerce state
and active domain pack were reset before each scenario and restored to
`commerce-us-2026-08-20` / `commerce-demo` after the test. No evidence receipt,
approval proof, control key, or provider credential is reproduced here.

### Conflicting evidence — pass after scoped-retrieval correction

| Identifier | Value |
|---|---|
| Event/task | `live-conflicting-evidence-20260822-02` / `order-event-live-conflicting-evidence-20260822-02` |
| Request / trace | `order-operations-1787439074847452187` / `dbf7accb62950449cfce2ea032ceee61` |

The deterministic terminal and policy outcomes were both
`conflicting_evidence`. Registry shows exact reads of
`demo-replacement-conflict-allow`, `demo-replacement-conflict-deny`, and
`demo-evidence-precedence`, followed by the Commerce evaluator. It contains no
execute, verify, or notification step. `ORD-1006` remained version 1 without a
recovery record. Jaeger contains 111 spans across the three services and no
error or mutation span.

The first safe attempt, request `order-operations-1787438642358823693` and
trace `1caeb0762be1beaa4de1f53a6607f593`, incorrectly returned
`insufficient_evidence`: the model supplied an `order_interaction` search
channel that excluded the marketplace conflict rules. The governed plan hook
now binds evidence scope to authoritative order state. The passing rerun used
the observed `marketplace` channel and proves that the correction changes the
retrieval set without weakening the deterministic no-mutation boundary.

### Properly classified insufficient evidence — pass after option-scope correction

| Identifier | Value |
|---|---|
| Event/task | `live-insufficient-evidence-20260822-02` / `order-event-live-insufficient-evidence-20260822-02` |
| Request / trace | `order-operations-1787439551802679081` / `d1cf189f6f71e0a3a4f09f60809bc7e0` |

The terminal and policy outcomes were both `insufficient_evidence`, not the
generic `needs_review`. Registry shows a shipping-scoped exact read of
`fedex-guarantee-service-scope` and the deterministic evaluator records the
missing payer, lane, ship-date, and exclusion facts. There is no execute,
verify, notification, or Commerce mutation. `ORD-1004` remained version 1.
Jaeger contains 101 spans across the three services with no error span.

The first safe attempt, request `order-operations-1787439187148751536` and
trace `6d8b16fdb2f8ad96a60e9f6dc9b71faf`, abstained correctly but retrieved only
generic evidence because the order's `online` channel was also applied to the
carrier-claim evidence. Commerce Core now declares the evidence channel on
each bounded fulfillment option; the agent binds the selected option's
authoritative scope. This keeps the mechanism domain-neutral while making the
carrier-specific abstention auditable.

### Policy changed during HITL — pass

| Identifier | Value |
|---|---|
| Event/task | `live-policy-changed-hitl-20260822-02` / `order-event-live-policy-changed-hitl-20260822-02` |
| Initial request / trace | `order-operations-1787440197169628906` / `c7797dbdc5842904e8bca4fb92de0aaf` |
| Checkpoint | `cp-be8a7944-5739-49` |
| Resume request / trace | `order-operations-1787440270395713106` / `1075e35b7628797b1f482763ec2e877b` |
| Same-event retry request / trace | `order-operations-1787440436604533537` / `426130538cca64d7c5ba9d1a7ffb8d13` |

The pending approval pinned order version 1 and baseline release/hash. Both
the gateway and Commerce evaluator were switched to
`commerce-us-2026-08-20-policy-change` before approval. Resume returned HTTP
409 with `status=replan_required`,
`terminal_outcome=policy_changed_during_hitl`, and
`retry_same_event=true`. The checkpoint endpoint reports `aborted`; before the
retry, `ORD-1004` was still version 1 with no recovery record. Registry's
resume execution preserves `POLICY_BASIS_CHANGED` on the failed execute step,
and marks verification and notification skipped due to that dependency. Its
32-span Jaeger trace has three expected error spans: the client POST, Commerce
execute endpoint, and governed execute step.

Reposting the same event returned `retry:true` and made a fresh decision
against the changed release. The new 300 USD replacement threshold produced
`allowed`; one command executed, verified, and notified at order version 2.
This proves the stale approval was rejected and was not replayed as the new
decision.

The earlier safe run, initial request
`order-operations-1787439686727357202` and resume request
`order-operations-1787439810264645344`, exposed a framework recording defect:
an early non-retryable exit lost the sanitized component error from the step
result, so the response hook could not classify `POLICY_BASIS_CHANGED`. The
executor now records the sanitized error before all retry-analysis exits, with
a regression test for a non-retryable HTTP 409. The run above proves the fix in
the deployed framework.

### State changed during HITL — pass

| Identifier | Value |
|---|---|
| Event/task | `live-state-changed-hitl-20260822-01` / `order-event-live-state-changed-hitl-20260822-01` |
| Initial request / trace | `order-operations-1787440545538037388` / `1cd7f1e90f885aea24905197f945f21c` |
| Checkpoint | `cp-f579e644-e793-43` |
| Resume request / trace | `order-operations-1787440643623311145` / `5c8ab2c5dd494adf39425a66b27dbc0f` |
| Same-event retry request / trace | `order-operations-1787440740400731219` / `9cf06363aa6c1e1ec9e1f8c87f93455e` |

The checkpoint pinned order version 1. The explicit external control changed
inventory availability and advanced `ORD-1004` to version 2 before approval.
Resume returned HTTP 409 with `status=replan_required`,
`terminal_outcome=state_changed_during_hitl`, and `retry_same_event=true`.
The checkpoint is `aborted`; the order remains version 2,
`inventory_available=false`, `fulfillment_status=missing_in_transit`, and has
no recovery record. Registry preserves `ORDER_STATE_CHANGED` on execute and
shows verify/notification skipped. Jaeger contains 32 spans and the same three
expected guarded-error operations as the policy-race resume.

The same event was then accepted with `retry:true`. Registry proves the new
request read version 2 with unavailable inventory; the task ended
`no_mutation` using Commerce state only. This is a fresh plan from current
state, not a replay of the version-1 approval.

### Alternate-domain switching — application pass, observability partial

| Identifier | Value |
|---|---|
| Correlated request prefix | `live-alternate-domain-20260822-02-*` |
| Release trace | `5eaa22e189a98203821891b6d8648a79` |
| Search trace | `d1bb00a6b64dee8e15833dd4ed9a574e` |
| Traversal trace | `8436859d5cfc213db601f98aefe722f4` |
| Gateway pod / UID | `domain-knowledge-gateway-76d64bf984-tvrkb` / `aa96a529-85ef-4008-996c-0356f9c28a43` |

Without a rebuild or restart, the same gateway pod activated
`it-access-2026-08-20` in domain `it-access-governance` for tenant `it-demo`.
Hybrid retrieval ranked `departed-user-revocation` first, and traversal
returned `departed-user-revocation IMPLEMENTED_BY offboarding-verification`.
The pod UID did not change, readiness stayed true, and restart count stayed
zero through activation and restoration to `commerce-demo`.

Jaeger contains one successful server span for each direct capability call and
retains the native `X-TruvaG3-Request-ID` values. It does not retain the IT
release, tenant, section, or relation identifiers as span attributes. Registry
contains no corresponding execution because the manual contract calls the tool
directly and no orchestrator execution exists. The application portability
contract passes; the scenario's combined Registry/Jaeger criterion does not.

The policy and state races also reproduce RV-OBS-005: their authoritative
checkpoint endpoints report `aborted`, while each initial Registry interruption
snapshot remains `pending`; only a manually joined resume sibling explains the
final state.

## Finding RV-OBS-008 — Direct portability calls cannot satisfy the stated Registry/Jaeger criterion

**Classification:** Scenario observability contract and safe tool telemetry\
**Severity:** Medium\
**Status:** Open

The alternate-domain runbook requires Registry and Jaeger to retain release,
tenant, section, and relation identifiers. The runbook invokes gateway
capabilities directly, so Registry correctly has no orchestrator execution to
store. The gateway's Jaeger spans retain operation and framework request ID but
not the safe domain identifiers returned by the successful operations.

Acceptance criteria:

- Decide whether the portability check remains a direct tool contract. If so,
  mark Registry as not applicable; otherwise provide a domain-neutral agent or
  execution envelope that produces a Registry execution.
- Add bounded, non-secret gateway span attributes for active release, tenant,
  returned section IDs, and relation types, with cardinality limits.
- Document and use `X-TruvaG3-Request-ID` in the manual scenario commands.
- Rerun the IT switch and prove the identifiers from the response can be joined
  to the same Jaeger traces without relying on wall-clock ordering.

## Finding RV-OBS-009 — Registry persists unredacted approval proofs

**Classification:** Sensitive HITL execution data\
**Severity:** High\
**Status:** Resolved 2026-08-22 local / 2026-08-23 UTC

The original guarded resume records persisted a non-redacted 64-character approval proof
inside the `execute_recovery_command` step parameters:

| Resume request | Proof length | Registry redacted | Exact value in Jaeger | Recent agent/tool logs |
|---|---:|---|---|---|
| `order-operations-1787440270395713106` | 64 | no | no | no match |
| `order-operations-1787440643623311145` | 64 | no | no | no match |

The audit compared the value in memory and did not print or copy it. Jaeger's
policy-resume diagnostic text mentions the parameter name, but an exact-value
comparison proves the proof itself is absent from both resume traces. Searches
of the current agent and Commerce logs found no approval-proof, control-key, or
provider-credential pattern. Registry is the confirmed leakage surface, which
violates the reserved scenarios' explicit no-approval-proof criterion.

Acceptance criteria:

- Redact or omit `approval_proof` before persisting execution step parameters,
  checkpoint projections, debug snapshots, or LLM/debug interactions.
- Retain only non-secret audit facts such as checkpoint ID, approver identity,
  decision type, decision time, and a one-way proof fingerprint if correlation
  is required.
- Add a Registry regression test that stores a resumed step and asserts the raw
  proof cannot be recovered from any API projection.
- Rerun both HITL race scenarios and scan Registry, Jaeger, and logs for the
  exact generated proof values without emitting those values in test output.

### Resolution verification

Framework persistence now redacts sensitive parameter keys in the detached
`StepResult.Parameters` projection while preserving the original value only on
the runtime dispatch path. The same key-aware redaction applies to
parameter-resolution metadata and executor log fields. LLM error repair and
semantic retry are skipped whenever a dispatched parameter set contains a
credential-bearing value, so the approval proof is not sent to an LLM after a
guarded failure. Both memory and Redis LLM debug stores also sanitize prompt,
response, error, description, adjustment, audit-entity, and metadata text as a
defense-in-depth persistence boundary.

The closing live run used event/task
`live-redaction-final-1787446865` /
`order-event-live-redaction-final-1787446865`, checkpoint
`cp-0c76ccb8-c51f-42`, initial request
`order-operations-1787446865811131891`, and successful resume request
`order-operations-1787446910647606296`. The resumed task ended `recovered`
under an `approval_required` decision, and the checkpoint endpoint ended
`completed`.

The Registry unified projection contains exactly one
`result.steps.7.parameters.approval_proof` field and one corresponding
resolution-metadata value; both equal the ten-character marker `[REDACTED]`.
A count-only scan of the entire projection found zero proof-shaped 64-character
values. The checkpoint projection contained no approval-proof field and zero
proof-shaped values. Current agent, Commerce, and gateway pod logs each
contained zero proof-shaped values; the agent log contained three expected
redaction markers. Jaeger trace `f70e2b6e45b084178567ff91dd000b53`
contains 29 spans across `order-operations-agent` and `commerce-core`, zero
error spans, and zero proof-shaped values. No proof value was emitted during
any verification command.

The first stale-race reruns after structured redaction exposed a second layer:
resume requests `order-operations-1787454609059141646` and
`order-operations-1787454725651798666` each had redacted step and resolution
fields but retained two proof-shaped values in `error_analysis` and
`semantic_retry` prompts. The values were never printed. This evidence drove
the LLM-boundary fix above rather than treating debug-store redaction alone as
sufficient.

The final race-specific verification used:

| Race | Initial request | Resume request / trace | Checkpoint |
|---|---|---|---|
| State changed | `order-operations-1787455268746308032` | `order-operations-1787455319493659830` / `c05ae0bfcdf2b907eb7b565b63561ce7` | `cp-3b2dd5b7-4c02-49` |
| Policy changed | `order-operations-1787455334827615843` | `order-operations-1787455389634165126` / `80f5d8d9cede1f8ae010d0aba248e871` | `cp-d1f96f7b-94c2-45` |

Both resume projections contain one redacted step parameter and one redacted
resolution value, zero proof-shaped values across the complete unified JSON,
and no `error_analysis` or `semantic_retry` interaction. Their remaining two
LLM records are `result_distillation` and `synthesis`, and their stored prompts
also contain zero proof-shaped values. Both checkpoint projections contain zero
matches. Each Jaeger resume trace has 22 spans, the expected three guarded-error
spans, and zero matches. Count-only scans of the current agent, Commerce, and
gateway logs found zero matches; the agent log contains six expected redaction
markers. No proof value was emitted by any verification command.

Unit coverage now proves the original value reaches the HTTP tool dispatch but
not the persisted step, resolution metadata, logs, LLM error-repair path,
memory debug store, or Redis debug store. These final results close the
Registry leak and the broader executor/LLM exposure discovered during
remediation. All historical rows remain intact as evidence of the defects.

## Functional closure verification

The remaining initial-development behaviors were exercised individually and
then through the default outcome runner. These checks deliberately exclude the
deferred 50-case evaluation, alternate database adapters, and Registry Viewer
UI defects.

| Behavior | Authoritative live evidence | Result |
|---|---|---|
| Explicit rejection | task `order-event-live-hitl-reject-1787445068`; request `order-operations-1787445068234586699`; checkpoint `cp-d14768e3-1068-43`; trace `d292efda7d9e669cb81f71fa98e4cc1e` | task completed as `approval_rejected`; no command executed; order remained version 1 |
| Approval expiry | task `order-event-live-hitl-expiry-1787445211`; request `order-operations-1787445211258687939`; checkpoint `cp-fb1a7977-3c10-49`; trace `7b54eb2a1ab46f4090c1c37cd46ea713` | task completed as `approval_expired`; checkpoint became `expired_rejected`; no command executed |
| State changed during HITL | task `order-event-live-state-no-llm-1787455268`; initial/resume requests `order-operations-1787455268746308032` and `order-operations-1787455319493659830`; checkpoint `cp-3b2dd5b7-4c02-49` | HTTP 409 `state_changed_during_hitl`; checkpoint aborted; explicit control produced version 2; no recovery executed |
| Policy changed during HITL | task `order-event-live-policy-no-llm-1787455334`; initial/resume requests `order-operations-1787455334827615843` and `order-operations-1787455389634165126`; checkpoint `cp-d1f96f7b-94c2-45` | HTTP 409 `policy_changed_during_hitl`; checkpoint aborted; order remained version 1; no recovery executed |
| Fresh reapproval after policy change | task `order-event-live-policy-reapproval-fixed-1787445946`; old/new/resume requests `order-operations-1787445946381889583`, `order-operations-1787446024682060648`, and `order-operations-1787446065452372818` | stale approval was rejected; same-event retry produced a distinct checkpoint against current policy; fresh approval recovered `ORD-1007` exactly once |
| Deterministic normal order | default runner direct `process_normal_order` result for `ORD-1000` | `normal_order_confirmed`, `agent_invoked=false`, `llm_invoked=false`, no mutation |
| Default successful recovery | task `order-event-demo-automatic-recovery-1787446133-56209`; request `order-operations-1787446133236523689`; trace `f3952cb932773e34bedaff89cd2df4b5` | `recovered` / `allowed` |
| Default approval path | task `order-event-demo-high-value-missing-1787446198-56209`; request `order-operations-1787446198299432013`; checkpoint `cp-d038488b-15be-48`; trace `acce2e2a405618eb739a02469d820a8b` | `approval_required`; checkpoint intentionally left pending for the operator |
| Default deterministic denial | task `order-event-demo-denied-option-1787446249-56209`; request `order-operations-1787446249150766084`; trace `305816f77fbb7cf99121a031dd1b648c` | `denied` / `denied`; no mutation |
| Default abstention | task `order-event-demo-insufficient-evidence-1787446320-56209`; request `order-operations-1787446320312228629`; trace `d43594ea36ada7b9d408a8ec5bc23355` | `insufficient_evidence`; no mutation |

The default runner completed all five required outcome classes after resetting
synthetic state. Each orchestrated initial request recorded one skill
activation, and the listed Jaeger traces contained the participating service
spans without unexpected error spans. The approval checkpoint intentionally
remains pending because operator choice is part of the demonstration.

One deliberately delayed state-race attempt ended safely as
`decision_basis_expired` because its generic decision-expiry guard was evaluated
before the already-true order-version guard. Commerce now gives concrete policy
or state changes precedence over generic time expiry when multiple guards are
true, while still enforcing expiry before evidence reuse or mutation. Unit
tests cover both changed-plus-expired combinations, and the final state race
above proves the intended classification live.

The task and checkpoint APIs were functionally complete at the time of this
earlier test. RV-OBS-005 has since been resolved by the Registry remediation
recorded below: the immutable interruption snapshot, live checkpoint state,
and interruption/resume executions are now joined without overwriting history.

## Registry Viewer remediation closure — 2026-08-27

### Scope and verification executions

This remediation closes the Registry-owned findings RV-OBS-001 through
RV-OBS-007. It does not close AC-RET-001, which belongs to retrieval scoring in
Domain Knowledge Gateway, or RV-OBS-008, which describes observability absent
from direct tool calls that bypass the agent and framework.

The updated Agentic Commerce producer and Registry Viewer were rebuilt and
deployed through their supported scripts:

```text
examples/agentic-commerce/setup.sh rebuild             PASS
examples/registry-viewer-app/setup.sh rebuild           PASS
examples/agentic-commerce/setup.sh smoke                PASS
```

The first Registry rebuild exposed a deployment defect: `k8-deployment.yaml`
required `JAEGER_QUERY_URL`, but `setup.sh` recreated
`registry-viewer-config` without that key. Kubernetes left the new pod in
`CreateContainerConfigError` with this event:

```text
Error: couldn't find key JAEGER_QUERY_URL in ConfigMap
truvag3-examples/registry-viewer-config
```

`setup.sh` now writes the same default as the manifest,
`http://jaeger-query`. A complete rerun rolled out successfully. This proof is
included because trace enrichment is not complete if a fresh deployment cannot
start.

Two fresh synthetic workflows supplied the closing evidence:

| Purpose | Identifier |
|---|---|
| Automatic recovery task | `order-event-demo-automatic-recovery-1787844624` |
| Automatic recovery execution | `order-operations-1787844624035395290` |
| Processing trace | `b1036fd4f6fb0493c7ab5926041dc1a2` |
| Submission trace | `65f0c19a9c2d45e083d370be121aa6ff` |
| Approval task | `order-event-demo-high-value-missing-1787844868` |
| Initial approval execution | `order-operations-1787844868392776305` |
| Approval checkpoint | `cp-3b077f59-56ff-45` |
| Approved resume execution | `order-operations-1787844987556516340` |

The approval and resume were performed manually through the documented
synthetic HITL endpoints. The resume completed one replacement, verification,
and notification for `ORD-1004`; the command carried checkpoint identity and
the order advanced from version 1 to version 2.

### Implementation map

| Finding | Corrective implementation |
|---|---|
| RV-OBS-001 | `ExecutionSummary` and `UnifiedExecutionView` expose non-additive wall-clock duration and its source. Enrichment is lower-bound-safe: a step/LLM envelope cannot replace a longer phase-loop measurement. Resume envelopes begin with work performed by that resume rather than restored pre-checkpoint steps. `dag.js` uses `primaryDurationMs` everywhere and displays aggregate LLM call time separately. |
| RV-OBS-002 | Backend DAG duration uses live `StepResult.Duration` nanoseconds, with `DurationMs` retained only for viewer fixtures. |
| RV-OBS-003 | Backend and Steps-view level construction is phase-local and inserts explicit phase-boundary levels before computing depth, node count, and maximum parallelism. |
| RV-OBS-004 | `jaeger_trace.go` reads the execution trace's `task.process` span and exposes `task.id` plus cross-trace `FOLLOWS_FROM` links. The UI links both processing and submission traces. |
| RV-OBS-005 | `buildHITLLifecycle` preserves the stored interruption snapshot, reads current checkpoint status, identifies resumes from checkpoint metadata, and links both executions in both directions. |
| RV-OBS-006 | Viewer routing models retain `implicit_deps`; backend and frontend render them as `implicit_dependency`/“Earlier-step results,” explicitly described as data lineage that does not control execution order. |
| RV-OBS-007 | Orchestration persists the successful post-`AfterSynthesis` application response. Registry labels model synthesis as an untrusted draft and renders the governed response as authoritative. |
| RV-OBS-010 | `jaeger_trace.go` extracts every recognized `pipeline.hook.*` span into a provider-neutral `pipeline_hooks` projection. The Viewer uses those trace-backed invocations, independently of LLM-debug records, to activate Pre/Post tabs and render deterministic hooks in Full Flow. |

The terminal-response change is intentionally in the framework's
`orchestration` module because Registry cannot reconstruct a response after an
application hook has changed it. The framework executor also restores its
existing result invariant by clearing an earlier attempt's error when a retry
succeeds. Domain-specific HITL task projection remains inside
`examples/agentic-commerce/order-operations-agent` as an application-owned
`InterruptController` decorator; no public framework callback was added for
that projection. All other observability remediation is contained in
`examples/registry-viewer-app`.

### RV-OBS-001 and RV-OBS-004 — elapsed time and trigger linkage

The fresh automatic-recovery list record keeps elapsed and aggregate call time
separate:

```json
{
  "wall_clock_duration_ms": 82888,
  "total_duration_ms": 64121,
  "llm_total_duration_ms": 82797,
  "duration_source": "llm_and_step_time_envelope"
}
```

The unified view enriches that record from the `task.process` span:

```json
{
  "wall_clock_duration_ms": 82901,
  "duration_source": "jaeger_task.process",
  "async_task_id": "order-event-demo-automatic-recovery-1787844624",
  "trace_enrichment_status": "available",
  "linked_traces": [{
    "trace_id": "65f0c19a9c2d45e083d370be121aa6ff",
    "relationship": "FOLLOWS_FROM",
    "source_operation": "task.process"
  }]
}
```

The UI headline consumes the wall-clock field. It no longer adds 64,121 ms of
phase-loop time to 82,797 ms of overlapping LLM-call time.

### RV-OBS-002, RV-OBS-003, and RV-OBS-006 — executable topology

The fresh backend DAG contains 16 nodes: 12 executed steps and four phase
boundaries. Its node count agrees with the returned node array, and its 11
levels preserve all five phases:

```json
{
  "statistics": {
    "total_nodes": 16,
    "completed_nodes": 12,
    "max_parallelism": 4,
    "depth": 11
  },
  "levels": [
    ["step-1", "step-2"],
    ["phase_boundary_1"],
    ["step-3", "step-4"],
    ["phase_boundary_2"],
    ["step-5", "step-6", "step-7", "step-8"],
    ["phase_boundary_3"],
    ["step-9"],
    ["phase_boundary_4"],
    ["step-9-governed-execute"],
    ["step-9-governed-verify"],
    ["step-9-governed-notify"]
  ]
}
```

The live durations are now populated from nanoseconds. Representative nodes
were `step-2=5 ms`, `step-4=11 ms`, `step-8=7 ms`, and
`step-9-governed-notify=2110 ms`. A positive duration below one millisecond may
still serialize as zero after integer millisecond conversion; this is normal
precision loss, not the prior all-zero live-data defect.

The same DAG returned 21 `implicit_dependency` edges. For example:

```json
{
  "source": "step-9",
  "target": "step-9-governed-execute",
  "edge_type": "implicit_dependency"
}
```

This records that the governed execute step consumes prior-phase decision
data. It does not incorrectly make that provenance edge a same-phase scheduler
dependency.

### RV-OBS-005 — one reconciled HITL lifecycle

Before approval, the initial execution reported both stored and live status as
`pending`. After the explicit approval and successful resume, the same initial
execution reported:

```json
{
  "request_id": "order-operations-1787844868392776305",
  "interrupted": true,
  "has_hitl_data": true,
  "hitl_lifecycle": {
    "checkpoint_id": "cp-3b077f59-56ff-45",
    "is_resume": false,
    "snapshot_status": "pending",
    "current_status": "completed",
    "current_status_source": "agent_checkpoint_api",
    "related_executions": [{
      "request_id": "order-operations-1787844987556516340",
      "role": "resume",
      "success": true
    }]
  }
}
```

The resume view reports `has_hitl_data=true`, the same checkpoint ID, and a
back-link to the interrupted execution. The historical `pending` snapshot was
not overwritten; it is explicitly distinguished from the live `completed`
status.

### RV-OBS-007 — diagnostic draft versus authoritative response

The fresh automatic-recovery record stores two different response artifacts:

```json
{
  "raw_synthesis_draft_prefix":
    "## Recovery completed and verified — ORD-1003",
  "governed_response_prefix":
    "Governed order recovery outcome: recovered.",
  "final_response_source": "after_synthesis_hooks"
}
```

The deployed UI asset labels the first artifact `Raw LLM synthesis draft` and
`Untrusted draft`. It renders the post-hook response in a separate governed
application-response panel in the Post-Execution tab. The LLM Calls tab keeps
only model activity and the pre-hook synthesis draft; it does not duplicate the
authoritative application response. Historical records that predate
terminal-response persistence show that the authoritative response is
unavailable instead of mislabeling the draft as final.

### Verification gates and visual boundary

The following checks passed after the final edits:

```text
registry-viewer: go test ./...
orchestration:   go test ./...
JavaScript:      node --check static/js/views/dag.js
setup script:    bash -n setup.sh
changed Go:      gofmt/goimports and git diff --check
lint:            golangci-lint run --new
security:        govulncheck ./... (Registry Viewer and orchestration)
```

The running application served the updated `js/views/dag.js`; the deployed
asset contains the elapsed-duration selector, processing/submission trace
links, prior-phase-data legend, bidirectional HITL navigation handler, and
untrusted-draft labels. No in-app browser connection was available in the
verification session, so screenshot-level layout and click testing remain a
manual smoke check. API behavior, deployed assets, and the two live workflows
were verified; this document does not claim an automated visual-browser pass.

### Post-implementation maintainability review — 2026-08-27

A separate source review was completed after the functional fixes and their
first live verification. It found and corrected the following implementation
edges:

- HITL sibling discovery now joins executions by the exact checkpoint ID. An
  execution that happens to share an `original_request_id` but belongs to a
  different checkpoint is not presented as part of the lifecycle.
- Registry Viewer fan-out calls now inherit the incoming HTTP request context,
  so cancellation and deadlines stop downstream checkpoint and LLM-debug reads.
- Registry-discovered HITL URLs are checked for a valid HTTP(S) scheme and host
  before use. The internal service-registry trust boundary is documented at the
  call site.
- The governed application response is rendered even when no LLM-debug record
  exists. Diagnostic-data availability no longer controls whether the actual
  application result can be seen.
- New HITL and dependency fields are HTML-escaped before insertion into the DAG
  page. Cross-trace links require valid hexadecimal trace and span identifiers
  before a Jaeger URL is produced.
- The framework-side terminal response source uses the named
  `FinalResponseSourceAfterSynthesisHooks` constant, avoiding duplicated magic
  strings between persistence paths and tests.
- A compatibility helper made unused by context propagation was removed rather
  than retained as dead code.

Focused verification after those corrections passed:

```text
Registry Viewer: go test ./..., go vet ./..., golangci-lint run --new
orchestration:   go test ./..., go vet ./..., golangci-lint run ./orchestration/...
security:        orchestration gosec ./...; new Registry Viewer G704 path clean
JavaScript:      node --check static/js/views/dag.js
source hygiene:  gofmt and git diff --check
deployment:      registry-viewer image sha256:a9e5c9412d1b3424f3396e25ce075ec2d8ff0a057be6e45c7e004930838d2ed8
```

The rebuilt live API again returned `final_response_source` equal to
`after_synthesis_hooks` and Jaeger-backed `duration_source` equal to
`jaeger_task.process` for
`order-operations-1787844624035395290`. It also returned the exact checkpoint
`cp-3b077f59-56ff-45` in both directions between interruption
`order-operations-1787844868392776305` and resume
`order-operations-1787844987556516340`. The former links to the latter as role
`resume`; the latter links back as role `interrupted`.

The Registry Viewer still has older general lint/security debt outside the
changed paths (for example, unchecked response writes and the pre-existing HITL
resume POST trust-boundary warning). Those are not regressions from this fix and
were not silently represented as resolved by this review.

### Reviewer follow-up corrections — 2026-08-27

A later semantic review found that the first duration implementation could
replace a known phase-loop duration with a smaller timestamp envelope, and that
a resume envelope included restored pre-checkpoint steps. It also found that a
successful executor retry retained an earlier attempt's error, and that an
application task-projection callback had been added to the public framework API
without being listed in the implementation map. The earlier maintainability
review was incomplete on these points. All four were corrected before closure.

#### Lower-bound-safe elapsed time

Before the correction, retained execution
`orch-1787283469631150640` reported:

```json
{
  "total_duration_ms": 183184,
  "wall_clock_duration_ms": 55469,
  "duration_source": "step_time_envelope",
  "has_llm_data": false
}
```

The timestamp envelope observed only the steps and omitted planning and other
work included in the phase-loop measurement. After deployment of the corrected
selector, the same record reports:

```json
{
  "total_duration_ms": 183184,
  "wall_clock_duration_ms": 183184,
  "duration_source": "phase_loop_fallback",
  "has_llm_data": false
}
```

Every step/LLM enrichment now competes with the current duration through a
shared maximum selector. A scan of all 18 retained list records after deployment
returned an empty set for `wall_clock_duration_ms < total_duration_ms`.

#### Resume invocation timing

Before the correction, resume execution
`order-operations-1787844987556516340` reported 134,750 ms because its result
contained restored steps beginning at 15:34:36, even though the resumed work
began at 15:36:27. After the correction, the same record reports:

```json
{
  "total_duration_ms": 2370,
  "wall_clock_duration_ms": 24130,
  "duration_source": "llm_and_step_time_envelope",
  "has_llm_data": true
}
```

For a record carrying `resume_checkpoint_id`, the step envelope now begins with
the earliest `hitl_resume` step and includes later phases from the same
invocation. Restored earlier steps no longer add the operator wait to active
processing time. The resume record's own LLM interactions still extend the
current-invocation envelope.

#### Successful retry invariant

The executor now clears `StepResult.Error` on its successful terminal attempt.
Focused retry tests assert that a two-attempt success has an empty error. The
DAG UI also suppresses and HTML-escapes an error when reading an older
internally inconsistent record whose `success` field is true.

#### Application-owned HITL projection

The proposed exported `HITLCommandProcessedCallback` and
`WithHITLCommandProcessedCallback` framework API were removed. The commerce
agent instead wraps its framework `InterruptController` with an example-owned
decorator that projects only successfully processed reject/abort commands into
the async task. Compaction, memory, skills, and guardrails are unaffected: the
decorator runs after the authoritative HITL command and performs no LLM or
planning operation.

Fresh live rejection proof after rebuilding both workloads:

```text
task:       order-event-review-reject-20260827-1639
execution:  order-operations-1787848771571619220
checkpoint: cp-1988e848-e58f-48
command:    reject (should_resume=false)
task result: approval_rejected
checkpoint current status: rejected
order:      ORD-1004, version 1, recovery=null
```

The Registry unified view preserves the checkpoint's historical `pending`
snapshot while reporting current status `rejected` from `agent_checkpoint_api`.

Final verification for this follow-up passed:

```text
Registry Viewer: tests, vet, build, changed-code lint, JavaScript syntax
orchestration:   tests, vet, build, lint, gosec, govulncheck
commerce agent:  tests, vet, build, changed-code lint, gosec, govulncheck
repository root: tests, vet, build, lint, gosec, govulncheck
deployment:      Registry Viewer and Agentic Commerce setup.sh rebuild
Registry image:  sha256:07b888aeab66360c402ae272dae61f8157bc404796047972a9e69d89f5b1864f
agent image:     sha256:a4aa79a6c0d0187a4477f684575d5643218e2990908ed2c75895ab6f7ce14e38
```

### Lower-priority reviewer follow-up — 2026-08-27

Seven later comments were verified against source and the live deployment.
Only three warranted changes. The duration-source difference was retained as
an intentional precision trade-off, the current unified-view cost was retained
for this local capability demo, the malformed-cycle claim overstated the
frontend effect, and the HITL checkpoint-identity issue had already been fixed.

#### Browser-facing Jaeger links

The frontend no longer embeds `http://jaeger.localhost`. It now links to a
same-origin Registry Viewer route, `/api/traces/{trace_id}`. The backend
validates the hexadecimal trace ID and the operator-controlled HTTP(S) Jaeger
base URL, then returns a temporary redirect. `JAEGER_UI_URL` is separate from
the cluster-internal `JAEGER_QUERY_URL` and is present in both the manifest and
the setup-generated ConfigMap.

Live proof after rebuild:

```text
ConfigMap JAEGER_UI_URL: http://jaeger.localhost
GET /api/traces/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: HTTP 307
Location: http://jaeger.localhost/trace/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
deployed JS contains relative /api/traces/ route: yes
deployed JS contains hardcoded Jaeger trace URL: no
```

#### Missing governed-response wording

The viewer no longer says that every execution without a stored
`final_response` is historical. The message now states only what the record
proves: no post-hook application response was stored, and any synthesis shown
has not passed through application governance hooks. This is accurate for both
older records and current `ExecutePlanWithSynthesis` workflow-mode records,
which intentionally do not run the pipeline-hook lifecycle.

#### Persisted final-response redaction

The execution-store boundary now makes a detached copy of `FinalResponse` and
applies `core.RedactSensitiveText` before either storage implementation
serializes it. The response returned to the caller and the orchestrator's
request-local record are not mutated. A regression test stores
`approval_proof=proof-secret`, proves that persistence contains
`approval_proof=[REDACTED]`, and proves the caller's original string is intact.

This closes the specific `FinalResponse` asymmetry. It does not redefine the
entire execution store as a secret-free store: original requests and arbitrary
tool response bodies remain diagnostic payloads and must not be allowed to
carry credentials. Avoiding secret-bearing payloads remains the primary
control; text redaction is defense in depth.

#### Verified decisions on the four unchanged comments

- The retained automatic execution
  `order-operations-1787844624035395290` reports 82,888 ms in the list from the
  LLM/step envelope and 82,901 ms in `/unified` from Jaeger's `task.process`.
  The 13 ms detail precision difference does not justify adding Jaeger calls to
  the list path.
- Healthy live `/unified` loads measured approximately 97 ms for the automatic
  execution and 191 ms for the HITL resume. Jaeger enrichment remains uncached
  with a three-second timeout; HITL fan-out is bounded and parallel. This is an
  accepted demo trade-off, not a current remediation item.
- A malformed cyclic plan can have inaccurate frontend level statistics, but
  its nodes are still constructed and remain visible. Valid routing plans are
  acyclic, so no extra frontend fallback was added for this diagnostic-only
  edge.
- `buildHITLLifecycle` already compares the exact checkpoint ID in both the
  interruption and resume arms. Its test includes a sibling interruption for a
  different checkpoint and proves that sibling is excluded.

Verification and deployment evidence:

```text
workspace:       tests, vet, build, goimports, gosec, govulncheck
orchestration:   full tests, vet, build, lint, gosec, govulncheck
Registry Viewer: full tests, vet, build, changed-code lint, JavaScript syntax
source hygiene:  git diff --check; removed strings absent
live smoke:      agent, commerce core, and knowledge gateway responded
Registry image:  sha256:64b12362e492502beecc93c6651c559377b8f9b666d18fa52340469a49ee305c
agent image:     sha256:4e2af36082e618a22dee2f3aaa9cc078e30198ac9b941131e76a13c66cf26787
```

The full workspace linter currently reports five unrelated existing findings
in `core/agent.go` and `ai/providers/azureopenai/factory.go`; the Registry
Viewer's full lint also retains its previously documented general debt.
Changed-code lint for this implementation and full orchestration lint both
report zero issues. These unrelated findings were not altered or represented
as part of this closure.

## Finding RV-OBS-010 — Deterministic pipeline hooks were invisible without an LLM call

**Classification:** Registry Viewer pipeline-governance observability\
**Severity:** High for governed-outcome audit\
**Status:** Resolved; live API and browser interaction verified 2026-08-27

### Observed discrepancy

The Agentic Commerce resume execution
`order-operations-1787844987556516340` stored an authoritative final response
with:

```json
{
  "final_response_source": "after_synthesis_hooks"
}
```

Jaeger independently retained both deterministic response-hook invocations:

```text
pipeline.hook.after_execution.governed-order-response   387 µs
pipeline.hook.after_synthesis.governed-order-response    7 µs
```

Neither invocation appeared in the Execution DAG's Full Flow or in a
Post-Execution tab. The tab itself was absent. The interrupted parent execution
`order-operations-1787844868392776305` had the matching pre-execution evidence:
five successful
`pipeline.hook.after_planning.governed-order-plan-binding` spans, one for each
planning phase, but it likewise had no Pre-Execution tab.

The hook implementation was not missing. Agentic Commerce registers
`governedOrderPlanHook` and `governedOrderResponseHook` in
[`../agentic-commerce/order-operations-agent/order_agent.go`](../../order-operations-agent/order_agent.go),
and the response hook implements both `AfterExecution` and `AfterSynthesis` in
[`../agentic-commerce/order-operations-agent/governed_response_hook.go`](../../order-operations-agent/governed_response_hook.go).

### Root cause

Registry Viewer's hook model was derived only from `LLMInteraction` records.
The frontend made Pre/Post tabs visible only when an interaction carried
`hook_phase=pre` or `hook_phase=post`. Full Flow likewise special-cased
LLM-backed conversation-history and user-memory activity.

This happened to work for Travel Chat because its user-memory hooks emit
`user_memory_*` interaction records around their LLM, vector, and storage work.
Agentic Commerce governance hooks are deterministic Go code. They make no LLM
call, correctly produce no `LLMInteraction`, and therefore disappeared from a
Viewer that incorrectly treated "no hook LLM call" as "no hook execution."

The mismatch was entirely in Registry Viewer. Framework pipeline-hook dispatch
and trace emission were already correct.

### Corrective implementation

The existing bounded Jaeger request made by `/unified` now also extracts
recognized framework operations with these prefixes:

```text
pipeline.hook.before_planning.*
pipeline.hook.after_planning.*
pipeline.hook.after_execution.*
pipeline.hook.after_synthesis.*
```

[`jaeger_trace.go`](../../../../truva-g3/examples/registry-viewer-app/jaeger_trace.go) projects each span as a structured
`PipelineHookExecution` containing trace/span identity, exact operation, hook
name, lifecycle phase, start time, microsecond duration, and completed/failed
status. [`main.go`](../../../../truva-g3/examples/registry-viewer-app/main.go) exposes the observations as `pipeline_hooks` on
`UnifiedExecutionView`. This reuses the existing Jaeger query and timeout; it
does not add another network fan-out or change the framework execution schema.

[`static/js/views/dag.js`](../../../../truva-g3/examples/registry-viewer-app/static/js/views/dag.js) now:

- activates Pre-Execution for `BeforePlanning` or `AfterPlanning` spans;
- activates Post-Execution for `AfterExecution` or `AfterSynthesis` spans, or
  when a stored post-hook application response exists;
- shows trace-backed hook cards separately from hook-internal LLM/debug
  operations, so a deterministic hook can truthfully report zero LLM calls;
- keeps the post-hook application response only in Post-Execution, rather than
  duplicating application output in the LLM Calls tab;
- places `AfterPlanning` nodes after the phase plan identified by authoritative
  plan/span timestamps;
- places deterministic `AfterExecution` nodes between terminal step leaves and
  synthesis, and `AfterSynthesis` nodes between synthesis and the application
  response;
- suppresses a trace wrapper in Full Flow only for known framework memory hooks
  that already have a richer internal-operation node, avoiding a false visual
  impression that Travel's memory hook ran twice; and
- provides a hook-node popup with phase, hook name, status, microsecond
  duration, exact trace operation, span ID, and trace navigation.

The HITL lifecycle remains historically accurate. The five `AfterPlanning`
invocations are shown on the interrupted parent where they ran. The
`AfterExecution` and `AfterSynthesis` invocations are shown on the resume where
they ran. The Viewer does not copy parent spans into the resume and imply that
the plan hook executed twice.

If Jaeger enrichment is unavailable or the trace has expired,
`trace_enrichment_status=unavailable` remains explicit and no synthetic hook
record is invented. A persisted `final_response_source=after_synthesis_hooks`
still makes the Post-Execution tab and authoritative response visible, but the
individual trace-backed invocation cards cannot be reconstructed after trace
retention expires.

### Live API proof

After rebuilding Registry Viewer, the resume unified endpoint returned:

```json
{
  "request_id": "order-operations-1787844987556516340",
  "trace_enrichment_status": "available",
  "final_response_source": "after_synthesis_hooks",
  "pipeline_hooks": [
    {
      "span_id": "21b2e1f6b8bc9cc8",
      "phase": "after_execution",
      "hook_name": "governed-order-response",
      "status": "completed",
      "duration_us": 387
    },
    {
      "span_id": "0e4bb2e546ead5ed",
      "phase": "after_synthesis",
      "hook_name": "governed-order-response",
      "status": "completed",
      "duration_us": 7
    }
  ]
}
```

The parent unified endpoint returned five completed `after_planning`
invocations named `governed-order-plan-binding`, with durations of 4, 4, 13,
16, and 17 µs. Their span IDs are, in chronological order:

```text
48ccd03d22fe374c
eda7358dc81608d4
d28944837e3b1a15
af35fecd864cc154
31ab84641ab7fdac
```

### Live browser proof

The in-app browser opened the deployed Execution DAG and verified:

- the resume has a visible `Post-Execution` tab;
- its summary says `2 hook invocations | 0 recorded operations | 0 LLM calls`;
- the tab shows completed `AfterExecution` and `AfterSynthesis` cards with the
  exact operation names and span IDs above;
- the same tab shows the authoritative `Post-hook application response` and
  states that the application changed the pre-hook synthesis output;
- Full Flow contains a dashed `AfterExecution / governed-order-response` node
  after the governed terminal steps, a synthesis node, a dashed
  `AfterSynthesis / governed-order-response` node, and then the completed
  response;
- clicking the AfterSynthesis node opens a popup that reports `COMPLETED`,
  `7µs`, operation
  `pipeline.hook.after_synthesis.governed-order-response`, span
  `0e4bb2e546ead5ed`, and an `Open trace` link; and
- the interrupted parent has a visible `Pre-Execution` tab containing all five
  completed `AfterPlanning` invocations and no false Post-Execution tab.

Travel regression execution `orch-1787858006025274901` retained both tabs. Its
Pre-Execution view now distinguishes one trace-backed
`BeforePlanning / user-memory-enrichment` invocation from seven recorded
internal operations. Its Post-Execution view distinguishes one trace-backed
`AfterSynthesis / user-memory-extraction` invocation from three recorded
operations, including two LLM calls. Existing Travel detail was preserved; the
new hook card explains the lifecycle wrapper around that detail.

### Verification gates

```text
Registry Viewer setup.sh rebuild:                         PASS
Registry Viewer Go tests, vet, and build:                 PASS
goimports on changed Go files:                            PASS
JavaScript syntax and focused utility/LLM tests:          PASS
golangci-lint run --new ./...:                            0 issues
govulncheck ./...:                                        no vulnerabilities
deployed image: sha256:24f4418e0dc9d04ce3c581b8e3a3b54402e48a38fa4a08f752bc1ca94bf380b3
```

The full Registry Viewer lint and `gosec` scans still reproduce the existing
general baseline already documented in this audit: unchecked response writes,
the server-timeout finding, the HITL-resume SSRF trust-boundary warning, and
log-injection warnings. Neither scan reports a new issue in
`jaeger_trace.go`, and changed-code lint reports zero issues. Those unrelated
items were not modified or represented as resolved by RV-OBS-010.

### Acceptance criteria — completed

- Deterministic hooks are visible without manufacturing an LLM call.
- Hook lifecycle phase, name, outcome, timing, trace, and span identity are
  available in the unified API and UI.
- Full Flow places the commerce response hooks on the correct sides of
  synthesis.
- Pre/Post tabs distinguish hook invocation evidence from hook-internal
  operations and aggregate LLM activity.
- HITL parent and resume observations remain attached to the execution where
  each hook actually ran.
- Travel's existing LLM-backed hook detail remains available.
- The authoritative application response appears in Post-Execution and is not
  duplicated in LLM Calls.

## Recommended remediation order

1. **Completed:** redact approval proofs from Registry persistence,
   projections, executor telemetry, and LLM/debug paths.
2. **Completed 2026-08-27:** correct duration semantics and stop additive
   double-counting.
3. **Completed 2026-08-27:** correct backend DAG duration conversion.
4. **Completed 2026-08-27:** make API and Steps-view topology phase-aware.
5. **Completed 2026-08-27:** surface asynchronous task and trigger-trace
   linkage.
6. **Completed 2026-08-27:** reconcile HITL interruption and resume records,
   including current status.
7. **Completed 2026-08-27:** preserve cross-phase `implicit_deps` in unified
   DAG data.
8. **Completed 2026-08-27:** distinguish raw LLM synthesis drafts from
   governed final responses.
9. **Completed 2026-08-27:** surface deterministic pipeline-hook executions
   independently of LLM-debug interactions.
10. **Open outside Registry Viewer:** correct the source-affinity scoring rule
   described in AC-RET-001.

After remediation, rerun the same scenario from a reset deployment and repeat
the four-way step reconciliation: plan, result, Registry DAG, and Jaeger tool
spans. The audit is closed only when the acceptance criteria above pass and the
new execution remains evidence-grounded, idempotent, verified, and fully
traceable from HTTP 202 submission through final notification.

### Reviewer's comment

Items 1 through 4 are sound as written and are supported by the verification
recorded under each finding.

Item 5 should be restated before it is worked. As shown under AC-RET-001,
tightening source-identity aliasing in the domain pack would not remove FedEx
from that result set, because elimination requires an affinity of exactly zero
and FedEx earns a nonzero score from the generic token `service`. The
corrective target is the affinity scoring rule in the gateway's
`preferExplicitSourceMatches`, not alias data in the pack. As currently phrased,
item 5 would produce a change that passes review and leaves the observed
behavior unaltered.

Suggested reordering consideration: RV-OBS-002 is cheap and self-contained,
while RV-OBS-001 and RV-OBS-003 both require agreeing on semantics first
(what a headline duration means, and whether topology is global or phase-local).
Landing RV-OBS-002 ahead of them removes one confounder from any before-and-after
comparison during the closing re-run.
