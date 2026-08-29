# Tool capability contract

The demo exposes ten capabilities through two independently discoverable tool
services. Capability names and JSON fields are the stable boundary; neither the
agent nor callers depend on a graph database API.

| Service | Capability | Effect |
|---|---|---|
| `domain-knowledge-gateway` | `search_evidence` | Read-only hybrid retrieval with hard scope and time filters |
| `domain-knowledge-gateway` | `traverse_relations` | Read-only bounded traversal from exact section IDs |
| `domain-knowledge-gateway` | `read_exact_section` | Read-only exact evidence, provenance, and signed scoped receipt |
| `domain-knowledge-gateway` | `get_release_info` | Read-only release identity, validity, and hash |
| `commerce-core` | `get_order_context` | Read-only synthetic order state |
| `commerce-core` | `list_fulfillment_options` | Read-only bounded action candidates |
| `commerce-core` | `evaluate_recovery_policy` | Deterministic decision from paired exact section IDs and receipts |
| `commerce-core` | `execute_recovery_command` | Revalidated idempotent mutation; approval-class tokens require runtime approval proof |
| `commerce-core` | `verify_order_outcome` | Read-only action-specific postcondition check |
| `commerce-core` | `send_synthetic_notification` | Synthetic, local audit record; no external message is sent |

Every capability uses `POST` with JSON at
`/api/capabilities/<capability-name>`. Errors return a non-2xx HTTP status and a
structured `core.ToolResponse` error. The concrete request and response schemas
are published by each tool through `/openapi.json` when the example's development
configuration enables OpenAPI.

All knowledge search, traversal, and exact-read calls require `tenant_id`,
`jurisdiction`, `channel`, and one RFC3339 `as_of` decision time. Exact reads
return `evidence_receipt`; callers pair those receipts with
`evidence_section_ids` and the identical `decision_time` in
`evaluate_recovery_policy`. `approval_reference` and `approval_proof` are
optional only for automatic decisions: the agent runtime injects them for an
approved HITL resume, and ordinary callers cannot bypass that boundary.

Commerce Core also registers `process_normal_order` and `manage_demo_state` as
development-only `Internal` capabilities. They remain callable by the setup-host
demo driver but are excluded from tool discovery and therefore do not change the
ten-capability agent boundary above.
