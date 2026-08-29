# 0001: Bootstrap lineage and knowledge-store boundary

- Status: accepted for capability demo
- Date: 2026-08-20
- Bootstrap baseline: repository commit `3f612bcdf73458ae98f4b86466d9ffa9a62fd026`

## Decision

The implementation was initialized by copying, not editing, the repository
templates:

- `truva-g3/examples/my-async-agent` to `order-operations-agent`
- `truva-g3/examples/my-tool` to `commerce-core`
- `truva-g3/examples/my-tool` to `domain-knowledge-gateway`

The copied child deployment files were then consolidated into one parent
`setup.sh`, one manifest, and three workspace Dockerfiles. The source template
directories remain untouched.

The domain knowledge tool depends only on the private `KnowledgeStore` interface:
release lookup, lexical candidate retrieval, vector candidate retrieval, bounded
relation traversal, exact section read, and close. HTTP contracts, ranking policy,
domain packs, and the agent do not expose LadybugDB types or query language.

The demo runtime builds the Ladybug adapter behind Go build tags
`ladybug system_ladybug`, pins the Go binding and native library to v0.17.0, and
verifies the downloaded native archive checksum. The in-memory adapter remains a
reference implementation for fast tests. A later Neo4j adapter must implement the
same interface and pass the same golden retrieval and exact-read conformance suite.

## Consequences

- The local architecture stays at three application pods; LadybugDB is embedded
  in the gateway pod rather than deployed as a fourth service.
- Swapping LadybugDB for Neo4j changes composition and deployment configuration,
  not agent or tool capability contracts.
- A production Neo4j deployment would still add operational concerns such as
  credentials, network policy, persistence, backup, and schema migration; this
  demo intentionally does not model those concerns.
- Ports 8394, 8395, and 8396 are assigned to the agent, commerce tool, and
  knowledge tool respectively.
