# Part 7: Using the pattern in another domain

## Is the solution domain-agnostic?

The complete commerce application is not domain-agnostic. It contains order
state, fulfillment options, commerce policy, and commerce-specific outcomes.

The framework pattern and knowledge gateway are domain-agnostic.

That distinction is important:

| Reusable without domain logic changes | Supplied for each domain |
|---|---|
| TruvaG3 agent/tool framework | Domain event and task objective |
| Redis tasks and HITL | Domain transaction capabilities |
| Skill lifecycle and observability | Domain procedure skill and agent prompt |
| Gateway capability names and schemas | Versioned domain pack |
| Search, filtering, fusion, exact reads, and traversal | Domain sources, sections, relations, aliases, and gold cases |
| `KnowledgeStore` interface | Deterministic domain policy and mutation invariants |

The architecture is reusable; the business authority is intentionally
domain-specific.

## The live IT-access proof

The demo includes a tiny non-commerce pack for employee access governance. It
contains three sections and one relationship:

```text
departed-user-revocation
    -- IMPLEMENTED_BY -->
offboarding-verification
```

During the paired live test, the same running gateway pod switched from the
commerce pack to this IT pack. It was not rebuilt or restarted.

| Evidence | Value |
|---|---|
| Request prefix | `live-alternate-domain-20260822-02-*` |
| Activated release | `it-access-2026-08-20` |
| Domain / tenant | `it-access-governance` / `it-demo` |
| Top search result | `departed-user-revocation` |
| Traversal result | `IMPLEMENTED_BY offboarding-verification` |
| Release/search/traversal traces | `5eaa22e189a98203821891b6d8648a79`, `d1bb00a6b64dee8e15833dd4ed9a574e`, `8436859d5cfc213db601f98aefe722f4` |

The gateway pod UID stayed
`aa96a529-85ef-4008-996c-0356f9c28a43`, restart count stayed zero, and readiness
stayed true through activation and restoration to the commerce pack.

This proves a real application contract: one gateway binary can load, search,
read, and traverse a differently shaped domain pack without code changes.

## Is the direct gateway test realistic?

It is realistic as a service contract and operator test. Search services are
often tested directly to isolate indexing and retrieval from an agent's
planning variability.

It is not a complete end-user IT workflow. There is no IT offboarding agent or
IT account-mutation tool in this demo. Direct capability calls therefore prove
gateway portability, not a full alternate-domain business action.

This also explains the observability result: Jaeger contains the direct gateway
spans, but Registry has no orchestration execution because no agent orchestrator
was involved.

## What a real second domain would add

To build an IT-access control tower using the same pattern, add:

1. an IT domain pack conforming to the existing pack schema;
2. an IT transaction tool for identity state, bounded options, deterministic
   authorization, execution, and verification;
3. an IT procedure skill;
4. an agent prompt and event contract for an IT objective.

The agent still discovers tools through Redis. The gateway still exposes
`get_release_info`, `search_evidence`, `traverse_relations`, and
`read_exact_section`. HITL and observability still come from the framework.

## What happens with a large corpus?

There is no architectural 2–10 MB corpus limit. The bundled packs are small so
that a reviewer can inspect them and understand why a result was returned.

As data grows, the public gateway contract stays stable while implementation
choices move behind it:

- batch parsing and normalization;
- lexical and vector indexes;
- sharding or partitioning;
- durable object or graph storage;
- provider-generated embeddings;
- offline evaluation and release publication.

The agent still receives only a bounded candidate set and exact decisive
sections. It does not load the entire corpus into its prompt or skill.

The Enterprise Knowledge Foundry research describes the larger ingestion and
publication lifecycle; the local demo starts from a reviewed immutable pack.

## LadybugDB now, Neo4j later

The gateway owns a private `KnowledgeStore` interface. The current adapter
projects the pack into embedded LadybugDB. A Neo4j adapter would implement that
interface and translate internal search/read/traversal requests into Neo4j
operations.

The following should not change during that swap:

- the agent;
- the skill;
- public gateway capabilities;
- domain-pack records;
- deterministic Commerce policy;
- scenario and retrieval contracts.

Deployment would gain Neo4j-specific configuration and likely a database pod,
Service, Secret, and optional volume. Keeping that cost outside today's demo is
why LadybugDB is a good minimalist choice.

## Run the portable contract test

This test loads the IT pack through the same gateway contract without changing
the active live pack:

```bash
./setup.sh domain-conformance
```

For the deliberate live switch, use the exact port-forward, activation, search,
traversal, and restoration sequence in the
[manual scenario runbook](../../scenarios/README.md#alternate-domain-switching).

Next: [Part 8: Reading the live evidence](08-observability.md).
