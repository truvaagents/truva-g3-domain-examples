# Enterprise Knowledge Foundry

Status: Proposed research and architecture blueprint. This is not a runnable
example or an implemented TruvaG3 framework capability.

Last research review: 2026-08-18.

## Table of Contents

- [1. Purpose](#1-purpose)
- [2. Executive Recommendation](#2-executive-recommendation)
- [3. Scope and Relationship to TruvaG3](#3-scope-and-relationship-to-truvag3)
- [4. Ownership Model](#4-ownership-model)
- [5. Design Principles](#5-design-principles)
- [6. Proposed Architecture](#6-proposed-architecture)
- [7. Source Intake and Parsing](#7-source-intake-and-parsing)
- [8. Knowledge Document Intermediate Representation](#8-knowledge-document-intermediate-representation)
- [9. Separate Evidence, Search, and Graph Products](#9-separate-evidence-search-and-graph-products)
- [10. AI-Assisted Compilation and Human Review](#10-ai-assisted-compilation-and-human-review)
- [11. Entity Resolution and Graph Integrity](#11-entity-resolution-and-graph-integrity)
- [12. Validation and Provenance](#12-validation-and-provenance)
- [13. Versioned Publication](#13-versioned-publication)
- [14. Runtime Retrieval](#14-runtime-retrieval)
- [15. Evaluation and Release Gates](#15-evaluation-and-release-gates)
- [16. Scalable Operations](#16-scalable-operations)
- [17. Provider-Neutral Boundaries](#17-provider-neutral-boundaries)
- [18. Practical V1](#18-practical-v1)
- [19. Later Phases](#19-later-phases)
- [20. Consumer Contract](#20-consumer-contract)
- [21. Failure Handling](#21-failure-handling)
- [22. Research Basis and Decision Trail](#22-research-basis-and-decision-trail)
- [23. Open Design Decisions](#23-open-design-decisions)

## 1. Purpose

Enterprises commonly hold useful domain knowledge in policy documents,
contracts, manuals, product catalogs, spreadsheets, databases, and internal
systems. Those sources are rarely ready for accurate agent retrieval. They may
contain complex page layouts, tables, repeated terminology, overlapping
versions, implicit relationships, and rules whose meaning depends on effective
date, jurisdiction, product, or organization.

This blueprint describes a separate **Enterprise Knowledge Foundry** that turns
those sources into reviewed, versioned, and reproducible knowledge releases.
It covers source intake, layout-aware parsing, structured compilation, entity
resolution, human review, provenance, evaluation, publication, and retrieval
quality.

This is a reusable data and knowledge platform concern. It is not specific to
commerce and is not part of the TruvaG3 agent-orchestration framework. A
TruvaG3 application can consume its published output through an ordinary
discoverable tool or service boundary.

## 2. Executive Recommendation

An enterprise should supply and approve its authoritative knowledge, but it
should not manually convert every source into chunks, embeddings, or graph
records. An LLM should also not publish extracted knowledge directly.

The recommended system is a governed knowledge supply chain:

1. Preserve every original source and its identity.
2. Parse it into a loss-preserving, provider-neutral intermediate form.
3. Compile separate exact-evidence, search, and graph products.
4. Use deterministic rules first and AI for bounded candidate generation.
5. Route high-impact and ambiguous candidates to domain experts.
6. Validate source lineage, schema, effective dates, and referential integrity.
7. Evaluate the complete release against reviewed document and query sets.
8. Build all retrieval projections away from live traffic.
9. Publish them together by changing one active-release pointer atomically.
10. Serve only bounded evidence from a pinned published release.

The critical design decision is to keep authoritative evidence separate from
generated search aids and extracted graph relationships. Advanced retrieval
can then evolve without confusing a generated summary or inferred edge with
enterprise truth.

## 3. Scope and Relationship to TruvaG3

### 3.1 Knowledge Foundry workstream

The foundry owns offline or asynchronous work:

- Source connectors and uploads
- Source preservation and change detection
- Document parsing and OCR
- Structured intermediate representation
- Candidate concept, fact, and relationship extraction
- Entity resolution
- Domain-expert review
- Provenance and lineage
- Index and graph construction
- Retrieval evaluation
- Release publication and retention

### 3.2 Application and agent workstream

The consuming application owns runtime behavior:

- Requesting an applicable published knowledge release
- Searching for bounded evidence
- Reading exact evidence by immutable identity
- Using skills to decide when and why knowledge is required
- Applying retrieved evidence to an application decision
- Enforcing business rules in deterministic policy and transaction services
- Recording which evidence influenced an execution

### 3.3 Integration boundary

```text
Enterprise sources
       |
       v
Enterprise Knowledge Foundry       Independent data/knowledge workstream
       |
       v
Published Knowledge Release
       |
       v
Domain Knowledge Gateway           Runtime serving boundary
       |
       v
Discoverable capability or tool
       |
       v
TruvaG3 agent and active skills     Agent workstream
```

The foundry may use LLMs or agents internally, and TruvaG3 could implement some
review or exception workflows. That is an optional implementation choice. The
foundry architecture must not depend on TruvaG3, and TruvaG3 framework modules
must not acquire document-parser, graph-database, or vector-store dependencies
because an application consumes enterprise knowledge.

The proposed
[Agentic Commerce Control Tower](AGENTIC_COMMERCE_CONTROL_TOWER.md) is one
consumer. Its runtime starts with a published release and does not implement
this foundry pipeline.

## 4. Ownership Model

| Responsibility | Primary owner |
|---|---|
| Supply policies, manuals, contracts, catalogs, and structured data | Enterprise source owner |
| Identify the authoritative source and its business scope | Business or data owner |
| Parse documents and propose structure | Knowledge Foundry |
| Extract candidate concepts, assertions, and relationships | Deterministic compiler with optional AI assistance |
| Resolve ambiguous entities and high-impact assertions | Domain subject-matter expert |
| Enforce schema, provenance, version, and release gates | Knowledge Foundry |
| Publish synchronized retrieval projections | Automated release pipeline |
| Retrieve published evidence | Domain Knowledge Gateway |
| Apply evidence to an action | Application agent and its skills |
| Enforce transactional authority | Deterministic policy and transaction services |
| Observe behavior and report corrections | Operators and business users |

Domain experts review meaning. They should not need to understand embedding
models, graph storage, chunk size, or index construction.

Runtime agents are readers. They can submit bounded feedback or correction
requests, but they must not mutate published enterprise knowledge directly.

## 5. Design Principles

### 5.1 Preserve before transforming

Retain original source bytes, declared identity, business owner, acquisition
time, effective interval, media type, and content hash before parsing or
generation. A later compiler must be able to reproduce and explain its output.

### 5.2 Generated knowledge is a candidate

AI-generated summaries, labels, assertions, and relationships remain generated
artifacts until the applicable deterministic and human-review requirements
pass. Model confidence helps prioritize review; it does not establish truth.

### 5.3 Evidence and retrieval aids are different

Exact source spans are evidence. Chunks, embeddings, contextual summaries, and
inferred graph edges are retrieval aids. A retrieval aid can help locate an
answer but cannot silently replace its source.

### 5.4 Publication is atomic

A request must never combine an old graph with a new vector index or a new
document with an old effective-date catalog. One immutable release manifest
pins every projection used together.

### 5.5 Accuracy includes abstention

High accuracy is not only answering correctly when evidence exists. It also
means identifying missing, stale, conflicting, unverified, or inapplicable
evidence and declining to present it as authoritative.

### 5.6 Provider names stay behind adapters

Document parsers, object stores, queues, search indexes, vector stores, graph
databases, and models are deployment choices. The source, intermediate,
release, and retrieval contracts remain provider-neutral.

### 5.7 Live facts stay live

Prices, balances, current inventory, payment state, entitlement, and similar
operational facts belong to authoritative APIs and datastores. The foundry can
publish schemas and durable reference knowledge, but it must not turn a stale
index into the source of truth for live state.

### 5.8 Source authority remains a business decision

The foundry can preserve and validate fidelity to an approved source. It cannot
make an incorrect enterprise policy true. Business owners must identify source
authority, applicability, and precedence. When authoritative sources conflict,
the release records the conflict or applies a reviewed precedence rule; search
ranking must not decide which policy wins.

## 6. Proposed Architecture

```text
 ENTERPRISE SOURCES
 Policies  Contracts  Manuals  Catalogs  APIs  Databases  Spreadsheets
      |
      v
 CONNECTORS AND SOURCE INTAKE
 identity  ownership  access class  effective time  hash  change token
      |
      v
 IMMUTABLE SOURCE ARCHIVE
 original bytes + source manifest + acquisition record
      |
      v
 PARSER ROUTER
 native structured parser | layout parser | OCR | specialist table parser
      |
      v
 KNOWLEDGE DOCUMENT IR
 blocks + headings + pages + tables + exact spans + parser provenance
      |
      +--------------------+----------------------+-------------------+
      |                    |                      |
      v                    v                      v
 EXACT EVIDENCE       SEARCH PRODUCTS        GRAPH CANDIDATES
 sections/spans       chunks/terms/vectors   entities/assertions/edges
      |                    |                      |
      +--------------------+----------------------+
                           |
                           v
 VALIDATION AND RESOLUTION
 schema + lineage + dates + units + identifiers + entity resolution
                           |
                           v
 RISK-BASED REVIEW
 source comparison + proposed change + impact + approve/reject/correct
                           |
                           v
 EVALUATION AND RELEASE GATES
 parsing + extraction + retrieval + citation + abstention
                           |
                           v
 IMMUTABLE KNOWLEDGE RELEASE
 manifest pins sources, compilers, models, prompts, ontology, indexes, tests
                           |
               +-----------+-----------+-----------+
               |           |           |           |
               v           v           v           v
          EXACT EVIDENCE  LEXICAL    VECTOR       GRAPH
              STORE       INDEX      INDEX      PROJECTION
               |           |           |           |
               +-----------+-----------+-----------+
                           |
                           v
                DOMAIN KNOWLEDGE GATEWAY
           bounded search, traversal, and exact reads
```

The build plane and serving plane are separate. A document upload, parser
failure, or model outage must not alter the currently published release.

## 7. Source Intake and Parsing

### 7.1 Connector contract

A connector should produce a source envelope rather than writing directly to an
index:

```json
{
  "source_id": "returns-policy-us",
  "source_system": "policy-repository",
  "source_version": "2026-07-15",
  "source_uri": "enterprise://policies/returns/us/2026-07-15",
  "owner": "commerce-policy",
  "media_type": "application/pdf",
  "content_hash": "sha256:...",
  "modified_at": "2026-07-15T14:00:00Z",
  "effective_from": "2026-08-01T00:00:00Z",
  "effective_until": null,
  "classification": "internal",
  "change_token": "..."
}
```

The connector stores the original bytes in the source archive and submits an
idempotent import job. Repeating a job with the same source identity and content
hash must not produce duplicate published knowledge.

Useful connector modes include:

- Full reconciliation for initial load and deletion detection
- Incremental import using source change tokens or timestamps
- Event-driven update for systems that produce reliable change events
- Manual upload for controlled one-time sources

Google Cloud's metadata-import model uses scoped imports, full and incremental
synchronization, source timestamps, validation, and comparison with existing
state. Those are useful operational patterns even when the implementation does
not use Google Cloud.

### 7.2 Parser selection

Do not send every source through the same generic parser.

| Source class | Preferred first step |
|---|---|
| Markdown, HTML, JSON, XML, or CSV | Deterministic native parser |
| DOCX with ordinary structure | Structure-aware document parser |
| Digitally generated PDF | Layout-aware PDF parser |
| Scanned document | OCR plus layout reconstruction |
| Complex financial or policy table | Specialist table extraction with review |
| Database or API record | Schema-aware deterministic mapping |

Traditional text extraction can flatten headings, tables, lists, figures, and
reading order. Google Document AI's layout parser, Azure Document Intelligence,
AWS Textract, and IBM Docling all treat layout or structured document elements
as important output. Docling is a credible open-source baseline; managed parser
adapters can be evaluated for difficult scans and tables. Selection should be
based on a representative enterprise corpus rather than product claims.

### 7.3 Parser fallback

A parser should emit quality signals, not only output. The router can:

1. Use the cheapest deterministic parser appropriate for the media type.
2. Validate text coverage, reading order, section structure, and table shape.
3. Retry with a stronger parser or OCR path when quality falls below the
   document-class threshold.
4. Route the document to review when no parser meets the required quality.

Parser choice, version, configuration, and output hash belong in provenance.

## 8. Knowledge Document Intermediate Representation

Raw PDF-to-text output is not a stable knowledge contract. The foundry should
define a loss-preserving **Knowledge Document IR** between parsers and
compilers.

Illustrative shape:

```json
{
  "document": {
    "source_id": "returns-policy-us",
    "source_version": "2026-07-15",
    "content_hash": "sha256:...",
    "title": "Returns Policy",
    "language": "en-US",
    "effective_from": "2026-08-01T00:00:00Z"
  },
  "parser": {
    "name": "parser-adapter",
    "version": "1.2.0",
    "configuration_hash": "sha256:..."
  },
  "blocks": [
    {
      "block_id": "page-12-clause-4.2",
      "type": "paragraph",
      "heading_path": ["Returns", "Damaged goods"],
      "page": 12,
      "bounding_box": [72, 210, 518, 307],
      "reading_order": 47,
      "exact_text": "...",
      "exact_text_hash": "sha256:..."
    }
  ],
  "tables": [
    {
      "table_id": "page-18-table-2",
      "heading_path": ["Refund limits"],
      "cells": []
    }
  ]
}
```

The IR should preserve:

- Source and version identity
- Exact text and its source coordinates
- Page and reading order
- Heading hierarchy
- Lists and nesting
- Tables, rows, columns, headers, and merged cells
- Figures and captions when material
- Language and encoding
- Parser provenance and quality signals
- Unknown extension fields for forward compatibility

A provider-specific parser result is translated into this IR. Downstream
compilers therefore do not need to understand every parsing provider.

## 9. Separate Evidence, Search, and Graph Products

### 9.1 Exact evidence units

Evidence units are immutable sections, clauses, list items, table regions, or
source spans. They are what a consumer cites and what a reviewer verifies.

Each unit includes:

- Stable source, document, version, and section identity
- Exact content or an exact-content reference
- Effective interval and applicability metadata
- Integrity hash
- Source location such as page, heading path, block, or table cell
- Verification and lifecycle state

### 9.2 Search products

Search products are rebuildable artifacts optimized for discovery:

- Section-aware chunks
- Parent-child section mappings
- Normalized terms and aliases
- Lexical index records
- Embeddings
- Contextual chunk descriptions
- Search-only summaries
- Candidate questions and query expansions

Index small search units but return exact evidence units or their parent
sections. A generated contextual description can improve retrieval but must not
be returned as if it were the original policy language.

### 9.3 Graph products

Maintain two related graphs:

1. An **evidence graph** records source, document, section, statement, and
   derivation relationships. Much of it can be constructed deterministically.
2. A **domain graph** records typed enterprise entities and relationships such
   as products, policies, organizations, jurisdictions, and dependencies.

Illustrative assertion:

```json
{
  "assertion_id": "assertion-01J...",
  "subject": "damaged-item-return",
  "predicate": "requires",
  "object": "photographic-evidence",
  "evidence": [
    {
      "source_id": "returns-policy-us",
      "source_version": "2026-07-15",
      "block_id": "page-12-clause-4.2",
      "exact_text_hash": "sha256:..."
    }
  ],
  "derivation": "ai_extracted",
  "status": "verified",
  "effective_from": "2026-08-01T00:00:00Z"
}
```

Microsoft GraphRAG keeps documents, text units, entities, relationships,
claims, communities, and reports separate. Its source-linked text units provide
an important provenance path. The AWS GraphRAG Toolkit similarly separates
extraction and graph construction and models a hierarchy from source through
chunks, statements, facts, and entities.

### 9.4 Knowledge graph, graph database, and GraphRAG

These terms describe different responsibilities:

- A **knowledge graph** is the entity-and-relationship model.
- A **graph database** is one way to store and query that model.
- **GraphRAG** is a retrieval approach that uses graph structure, often with
  source text and vector retrieval, to assemble evidence for a model.

A knowledge graph does not require a dedicated graph database. A small graph
can use relational edge tables or immutable files. Microsoft GraphRAG, for
example, can materialize its graph-related tables outside a graph database.
Conversely, installing a graph database does not create a trustworthy knowledge
graph: the foundry still needs entity resolution, typed relationships,
provenance, verification, versioning, and evaluation.

Graph retrieval is useful when relevant evidence is connected without using
similar language, such as supply-chain dependencies, organizational ownership,
contract applicability, jurisdiction, product substitution, or regulatory
obligations. It is unnecessary overhead for an exact clause or identifier
lookup that lexical retrieval can answer directly.

## 10. AI-Assisted Compilation and Human Review

### 10.1 Appropriate AI work

AI can assist with:

- Suggesting section or concept boundaries
- Drafting OKF concepts from existing sources
- Extracting candidate entities, assertions, and relationships
- Mapping source terminology to a controlled vocabulary
- Producing contextual descriptions for search units
- Comparing document versions and highlighting likely semantic changes
- Proposing duplicate or equivalent entities
- Classifying candidates by review risk

### 10.2 Work that needs stronger authority

The following normally require deterministic derivation or explicit expert
review:

- Contractual or regulatory obligation
- Financial threshold
- Permission, entitlement, or prohibition
- Effective date and jurisdiction
- Measurement unit or currency conversion
- Legal-entity or product-identity merge
- Relationship that can permit an automated action

Neo4j's knowledge-graph builder documentation notes that an LLM can generate
labels, relationship types, or properties beyond the schema supplied as
guidance. The compiler must therefore place LLM output in a candidate area and
apply deterministic schema and evidence validation before publication.

### 10.3 Risk-based review

| Review class | Examples | Default publication rule |
|---|---|---|
| Authoritative | Policy limits, contractual rules, permissions, entity merges | Expert approval required |
| Derived factual | Product attributes, organization links, applicability mappings | Deterministic validation plus review or measured sampling |
| Search-only | Keywords, embeddings, contextual summaries | Automatic after technical checks; retain generated label |

The review interface should show the candidate beside:

- The original document
- Highlighted exact evidence
- Previous published value
- Proposed new value
- Source ownership and applicability
- Parser, compiler, model, and prompt identity
- Known downstream assertions or rules affected

For V1, generated files reviewed through a Git pull request are sufficient. A
dedicated review application becomes valuable when document volume and reviewer
specialization make file review inefficient.

## 11. Entity Resolution and Graph Integrity

Incorrectly merging two entities can contaminate many graph paths. Resolve
identity in this order:

1. Stable identifier supplied by an authoritative source system.
2. Exact normalized business identifier, such as SKU, policy ID, account ID,
   or legal-entity identifier.
3. Approved alias or crosswalk.
4. Deterministic composite key.
5. Fuzzy, lexical, or embedding-based candidate generation.
6. Human decision for ambiguous candidates.

Do not destructively merge an uncertain candidate. Record a reversible
`SAME_AS_CANDIDATE` relationship with its supporting evidence, method, score,
and review state.

Graph integrity rules should include:

- Versioned entity and relationship types
- Allowed source and target types for every predicate
- Evidence requirements by relationship class
- Effective and transaction time when needed
- Verified and inferred states that cannot be confused
- Reversible merge history
- Source support counts for assertions supported by several documents
- Safe retraction when one source is deleted or superseded

An inferred relationship can guide retrieval. It must not silently become an
enforceable business rule.

## 12. Validation and Provenance

### 12.1 Deterministic validation

Before review or publication, validators should confirm:

- Every cited source and block exists.
- The cited content hash matches the source archive.
- Required identities and applicability fields are present.
- Ontology types, predicates, and properties are allowed.
- Dates, currencies, quantities, and units parse correctly.
- Entity and relationship references are resolvable.
- Effective intervals are valid.
- Retired or superseded sources are not presented as current.
- The same identity is not published with incompatible definitions.
- Generated content is visibly classified as generated.

If an RDF representation is used, W3C SHACL is a suitable standard for graph
shape validation. Property-graph or JSON implementations should provide
equivalent validation without requiring RDF in V1.

### 12.2 Provenance model

Every published artifact should answer:

- Which source produced it?
- Which version and exact section?
- Which parser and configuration?
- Which compiler, rule, model, and prompt?
- Was it copied, calculated, generated, inferred, or verified?
- Who or what approved it?
- When does it become applicable?

W3C PROV provides a mature vocabulary based on entities, activities, agents,
derivation, generation, use, and attribution. V1 need not store RDF, but its
provider-neutral provenance records should retain equivalent relationships.

OKF v0.2 provenance, generated, verified, status, freshness, and attested
computation fields align with this approach. OKF is useful as a source and
exchange format; it does not replace fine-grained source spans, indexes, or the
serving runtime.

## 13. Versioned Publication

### 13.1 Immutable release manifest

One manifest pins all artifacts that can be used together:

```json
{
  "release_id": "enterprise-commerce-us-2026-08-18.1",
  "source_manifest_hash": "sha256:...",
  "knowledge_ir_version": "1.0",
  "ontology_version": "commerce-v3",
  "compiler_version": "knowledge-foundry-v1.2",
  "parser_versions": {
    "pdf": "parser-adapter-2.1"
  },
  "model_profiles": {
    "fact_extraction": "extraction-profile-v2",
    "context_generation": "context-profile-v1"
  },
  "model_behavior_fingerprints": {
    "fact_extraction": "sha256:...",
    "context_generation": "sha256:..."
  },
  "prompt_fingerprints": {
    "fact_extraction": "sha256:..."
  },
  "projections": {
    "evidence": "evidence-20260818-1",
    "lexical": "lexical-20260818-1",
    "vector": "vector-20260818-1",
    "graph": "graph-20260818-1"
  },
  "evaluation_report": "evaluation-20260818-1",
  "approved_at": "2026-08-18T20:00:00Z",
  "status": "published"
}
```

The manifest records behavior identity, not credentials or unrestricted source
content.

### 13.2 Atomic publication

Publication should:

1. Freeze the candidate source manifest.
2. Build exact, lexical, vector, and graph projections outside the active path.
3. Run structural and retrieval evaluations.
4. Confirm every required projection is ready.
5. Write the immutable release manifest.
6. Change one active-release pointer atomically.

Qdrant collection aliases are one provider example of atomically switching from
an old index to a newly built collection while requests continue. Other
providers can implement the same application contract with aliases, views,
versioned tables, or manifest pointers.

An unpublished candidate can be discarded and rebuilt. After publication, the
normal correction strategy is a new forward release. Older releases remain
readable for audit and pinned in-flight executions, not as the primary
operational editing surface.

## 14. Runtime Retrieval

### 14.1 Retrieval pipeline

```text
Query and required evidence category
        |
        v
Metadata constraints
tenant, domain, jurisdiction, effective time, status, verification
        |
        +----------------+----------------+
        |                |                |
        v                v                v
Lexical search      Dense search    Optional graph lookup
        +----------------+----------------+
                         |
                         v
                Candidate rank fusion
                         |
                         v
                    Reranking
                         |
                         v
              Exact evidence retrieval
                         |
                         v
             Evidence sufficiency check
                         |
                         v
       Bounded evidence bundle or explicit abstention
```

Apply tenant, jurisdiction, effective-time, lifecycle, and verification filters
before semantic ranking. Exact identifiers, error codes, policy names, and
contract language are often best found lexically; paraphrases benefit from
dense retrieval. Contextual Retrieval research provides evidence that lexical
and embedding retrieval plus contextualized chunks and reranking can complement
one another, but its reported improvements are corpus-specific and should not
replace application evaluation.

Graph traversal should be selected for relationship-dependent questions and
bounded, normally to one or two useful hops. Global GraphRAG or hierarchical
retrieval is appropriate only when the question genuinely concerns a whole
corpus or cross-document theme.

### 14.2 Retrieval rules

- Start with exact reads and lexical search.
- Add dense search only after measuring a lexical baseline.
- Fuse lexical and dense results rather than replacing exact matching.
- Rerank a bounded candidate set.
- Return exact evidence, not a generated search summary.
- Keep every graph path connected to source evidence.
- Do not use unverified edges as authority.
- Return a section index or cursor when the evidence bound is reached.
- Distinguish no result from proof that no applicable rule exists.
- Return stale, conflicting, or insufficient status explicitly.
- Fetch current operational facts from their authoritative APIs.

Long-context models and context caching can help with a small, stable corpus,
but they do not remove versioning, provenance, retrieval quality, or effective
date requirements. Fine-tuning can teach stable behavior and terminology; it is
not an auditable store for exact and changing enterprise facts.

## 15. Evaluation and Release Gates

### 15.1 Parsing evaluation

Measure by document class:

- Page and content coverage
- Reading-order accuracy
- Heading and section-boundary accuracy
- List hierarchy accuracy
- Table cell, header, and merged-cell accuracy
- OCR quality on reviewed samples
- Exact source-span round-trip
- Percentage of blocks with complete provenance

Maintain a fixed parsing corpus containing ordinary documents, scans, complex
tables, repeated headers, footnotes, multi-column pages, and known failures.

### 15.2 Extraction evaluation

Measure:

- Entity precision, recall, and F1
- Assertion and relationship precision, recall, and F1
- Evidence-span validity
- Date, unit, quantity, and currency accuracy
- Entity-resolution precision
- Unsupported assertion rate
- Schema and referential-integrity failures
- Verified, inferred, rejected, and unresolved proportions

For high-impact assertions, precision and safe abstention normally matter more
than maximizing automatic extraction coverage.

### 15.3 Retrieval evaluation

Measure:

- Recall@k and precision@k
- Mean reciprocal rank or nDCG
- Correct evidence-section rate
- Citation precision
- Effective-date and jurisdiction-filter accuracy
- Relationship-path accuracy
- No-answer and abstention accuracy
- Freshness and conflict handling
- Latency and result-size bounds

### 15.4 Answer evaluation

When an application generates an answer from the evidence, evaluate:

- Faithful use of the retrieved evidence
- Completeness
- Answer relevance
- Unsupported-claim rate
- Correct treatment of conflicts and insufficient evidence

ARES, RAGAS, and RAGChecker provide useful automated RAG evaluation patterns.
They should supplement, not replace, a human-reviewed evaluation set.

### 15.5 V1 gold set

Create 50 to 200 representative questions with domain experts. Each case should
identify:

- Expected source and section
- Expected relationship path when applicable
- Applicable date, jurisdiction, and domain
- Whether the correct result is an answer, qualification, conflict, or
  abstention

Include exact lookups, paraphrases, similar clauses, tables, scans, superseded
versions, conflicting sources, multi-hop questions, and deliberately missing
answers. Run this set whenever a parser, compiler, model, prompt, ontology,
chunking policy, retrieval method, or index implementation changes.

There is no universal numeric release threshold. A policy or contract corpus
should prioritize evidence precision and abstention. A discovery corpus may
accept lower precision in exchange for higher recall. Each domain records its
own reviewed thresholds in the release policy.

## 16. Scalable Operations

### 16.1 Idempotent document jobs

```text
Source discovered
  -> calculate identity and hash
  -> unchanged? record observation and stop
  -> archive original
  -> parse
  -> validate parse quality
  -> compile candidate products
  -> resolve entities
  -> route review
  -> evaluate affected cases
  -> include in candidate release
```

Jobs should support:

- Per-document idempotency
- Bounded retry
- Dead-letter handling
- Micro-batching
- Resumable extraction and projection building
- Safe cancellation
- Source-to-artifact dependency tracking
- Rebuilding only affected products
- Periodic full reconciliation for deletions and drift

The AWS GraphRAG Toolkit's separate, micro-batched extraction and build stages
provide one useful operational reference for continuous and resumable graph
construction.

### 16.2 Retraction and supersession

When a source is removed or superseded:

1. Do not mutate the active release in place.
2. Identify all evidence, search units, and assertions derived from it.
3. Remove only that source's support from multi-source assertions.
4. Re-evaluate assertions that no longer have sufficient support.
5. Rebuild affected projections.
6. Publish a new release after evaluation.

### 16.3 Operational measures

Track:

- Source-to-publish latency
- Import success and retry rates
- Parser fallback and review rates
- Review queue age
- Candidate rejection and correction rates
- Release build duration and cost
- Index generation skew
- Stale or unowned sources
- Retrieval latency and abstention rate
- Production feedback converted into evaluation cases

Production feedback can propose new test cases or correction jobs. It must not
automatically modify a published knowledge release.

## 17. Provider-Neutral Boundaries

Suggested behavior-level interfaces include:

```text
SourceConnector
  DiscoverChanges(scope, cursor) -> source envelopes and next cursor
  ReadSource(source identity) -> immutable bytes and metadata

DocumentParser
  Parse(source envelope, bytes) -> Knowledge Document IR

KnowledgeCompiler
  Compile(document IR, policy) -> candidate artifacts

CandidateValidator
  Validate(candidate set, schema, source archive) -> validation report

KnowledgePublisher
  Stage(candidate release) -> staged release
  Publish(staged release, expected active release) -> published release

KnowledgeReader
  Search(release, query, constraints, bounds) -> evidence bundle
  Traverse(release, concept, constraints, bounds) -> evidence path
  ReadExact(release, evidence identity) -> exact evidence
```

These are conceptual seams, not implemented Go APIs. Implementation planning
should validate names and method shapes against the selected language and
deployment architecture.

Possible provider adapters include:

| Capability | Local or open option | Managed alternatives |
|---|---|---|
| Source archive | Files, Git, S3-compatible object store | Cloud object storage or enterprise content system |
| Parsing | Native parsers and Docling | Document AI, Textract, Azure Document Intelligence |
| Work queue | Database queue, Redis Streams, NATS, Kafka | Managed queue or event service |
| Lexical retrieval | PostgreSQL full text, OpenSearch | Managed search service |
| Vector retrieval | pgvector, Qdrant, OpenSearch | Managed vector service |
| Small typed graph | In-process projection or relational edge tables | Managed relational database |
| Property graph | Apache AGE, Neo4j, Memgraph, FalkorDB | Managed Neo4j or another managed property graph |
| RDF or managed graph | RDF store with SPARQL and optional SHACL | Amazon Neptune |
| Multi-model graph | Application-selected multi-model database | Spanner Graph |
| Review workflow | Git pull request | Dedicated enterprise review application |

The local default should be chosen for reproducibility and laptop resource use.
Production deployments can select different adapters without changing the
release or retrieval contracts.

Provider selection should also consider license, client maturity, backup and
restore, atomic publication support, multi-tenant isolation, graph-query needs,
index rebuild time, operational skills, and measured performance on the real
corpus. Formal ontology interoperability may favor RDF and SHACL; application-
owned operational relationships may be simpler in a property graph or typed
relational tables.

## 18. Practical V1

V1 should prove evidence accuracy and reproducible publication before adding
every retrieval technique.

### 18.1 V1 includes

- Git or object-store source archive
- Markdown, OKF, JSON, CSV, and one layout-aware document parser adapter
- Provider-neutral Knowledge Document IR
- Exact section and source-span registry
- Deterministic structure-aware chunking
- Lexical search
- Small typed evidence and domain graph
- Candidate and published states
- Deterministic validators
- Git-based expert review
- Immutable release manifest
- Atomic active-release pointer
- Gold parsing and retrieval sets
- Domain Knowledge Gateway with bounded search, traversal, and exact reads

### 18.2 V1 does not require

- A dedicated graph database
- Embeddings
- A reranking model
- Automatic publication of authoritative assertions
- A custom reviewer UI
- A general ontology-management platform
- A framework-level TruvaG3 retrieval abstraction
- Importing every enterprise content system

The first corpus should be synthetic or specifically approved for development.
It should still contain difficult structures and version conflicts so that the
quality gates are meaningful.

## 19. Later Phases

### Phase 2: Retrieval and operational scale

- Enterprise connectors
- Durable distributed work queues
- Incremental projection construction
- Dense retrieval and reranking
- Graph-database adapter
- LLM-assisted extraction at measured quality levels
- Review application
- Cross-source conflict detection
- Downstream impact analysis

### Phase 3: Controlled automation

- Active-learning review prioritization
- Automatic publication of low-risk search metadata
- Multi-tenant isolation and release management
- Larger ontologies and vocabulary governance
- Hierarchical and global retrieval where justified
- Continuous production evaluation
- Enterprise governance and lineage integrations

Automation should expand by artifact class and measured risk. It should not be
enabled globally because one extractor performs well on a narrow test corpus.

## 20. Consumer Contract

A consuming application should receive a bounded evidence bundle similar to:

```json
{
  "release_id": "enterprise-commerce-us-2026-08-18.1",
  "evidence": [
    {
      "document_id": "returns-policy-us",
      "document_version": "2026-07-15",
      "section_id": "4.2",
      "effective_from": "2026-08-01T00:00:00Z",
      "excerpt": "A bounded exact source excerpt...",
      "content_hash": "sha256:...",
      "source_reference": "knowledge://returns-policy-us/2026-07-15/4.2",
      "verification_status": "verified",
      "retrieval_method": "hybrid"
    }
  ],
  "relationship_paths": [],
  "truncated": false,
  "evidence_status": "sufficient"
}
```

The consumer should not need parser, embedding, graph-provider, or index names.
It needs:

- Release identity
- Exact evidence identity and bounded content
- Effective interval and applicability
- Verification, lifecycle, and conflict status
- Integrity identity
- Relationship provenance when applicable
- Structured insufficient, stale, unavailable, or conflicting outcomes

A long-running or human-suspended execution can retain the release and evidence
references it used. The serving system retains old releases according to the
audit and execution-retention policy so exact evidence can be reproduced.

## 21. Failure Handling

| Condition | Required behavior |
|---|---|
| Source cannot be read | Retry within bounds, then dead-letter without changing the active release |
| Parser quality fails | Try an approved fallback or route to review |
| Candidate cites a missing span | Reject the candidate |
| Schema or referential integrity fails | Reject the affected candidate or release according to policy |
| Ambiguous entity match | Retain separate identities and request review |
| Authoritative sources conflict | Apply explicit source precedence or escalate; do not let ranking decide |
| Required evaluation regresses | Block publication |
| One projection fails to build | Do not publish the release |
| Active pointer update conflicts | Re-read current state and rebuild or rebase the candidate release |
| Runtime release is unavailable | Return an explicit unavailable result; do not silently use a different version |
| Evidence is absent | Return insufficient evidence, not proof that no rule exists |

## 22. Research Basis and Decision Trail

This blueprint was researched on 2026-08-18 using primary specifications,
official technical documentation, and original research.

### Representation, trust, and provenance

- [Open Knowledge Format v0.2 specification](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
  for portable knowledge bundles, progressive disclosure, provenance,
  verification, lifecycle, freshness, and attested-computation boundaries
- [Google Cloud's OKF v0.2 trust discussion](https://cloud.google.com/blog/products/data-analytics/okf-v0-2-adds-trust-signals/)
  for explicit trust signals when knowledge is generated at scale
- [W3C PROV-O](https://www.w3.org/TR/prov-o/) for source, activity, agent,
  derivation, generation, and attribution relationships
- [W3C SHACL](https://www.w3.org/TR/shacl/) for RDF graph validation when a
  formal semantic representation is selected

### Parsing and structured documents

- [IBM Docling technical report](https://research.ibm.com/publications/docling-technical-report)
  for an open, extensible document-conversion baseline with layout and table
  models
- [Google Document AI layout parser](https://docs.cloud.google.com/document-ai/docs/layout-parse-chunk)
  for preserving headings, tables, lists, figures, and document relationships
  for retrieval
- [Azure Document Intelligence layout model](https://learn.microsoft.com/en-us/azure/ai-services/document-intelligence/prebuilt/layout)
  for structured text, table, selection-mark, and section extraction
- [AWS Textract documentation](https://docs.aws.amazon.com/textract/) for OCR,
  forms, tables, and layout extraction

### Knowledge and graph construction

- [Microsoft GraphRAG default dataflow](https://github.com/microsoft/graphrag/blob/main/docs/index/default_dataflow.md)
  for source-linked text units, extracted entities and relationships, claims,
  communities, and reports
- [AWS GraphRAG Toolkit lexical graph](https://awslabs.github.io/graphrag-toolkit/lexical-graph/overview/)
  for source-to-chunk-to-statement-to-fact-to-entity hierarchy and separated,
  resumable extraction and build stages
- [Neo4j Knowledge Graph Builder](https://neo4j.com/docs/neo4j-graphrag-python/current/user_guide_kg_builder.html)
  for schema-guided LLM extraction and its need for explicit output pruning and
  validation
- [Comprehensive Survey on Automatic Knowledge Graph Construction](https://arxiv.org/abs/2302.05019)
  for entity, coreference, typing, and relationship-extraction concerns

### Import and publication operations

- [Google Cloud metadata imports](https://docs.cloud.google.com/dataplex/docs/import-metadata)
  for scoped full and incremental imports, timestamps, validation, and stale
  update handling
- [Qdrant collection aliases](https://qdrant.tech/documentation/manage-data/collections/)
  as one implementation example of atomic index replacement

### Storage and serving choices

- [Neo4j documentation](https://neo4j.com/docs/),
  [Memgraph documentation](https://memgraph.com/docs/), and
  [FalkorDB documentation](https://docs.falkordb.com/) for open or
  self-managed property-graph choices
- [Apache AGE](https://age.apache.org/overview/) for a PostgreSQL extension that
  supports graph data and openCypher queries
- [Amazon Neptune documentation](https://docs.aws.amazon.com/neptune/) for a
  managed property-graph and RDF option
- [Spanner Graph documentation](https://cloud.google.com/spanner/docs/graph/overview)
  for a managed multi-model relational, graph, full-text, and vector option

These references establish available implementation classes, not a preferred
provider. License, operational maturity, portability, and corpus-specific
performance still require evaluation.

### Retrieval

- [Microsoft GraphRAG query modes](https://microsoft.github.io/graphrag/query/overview/)
  for local, global, DRIFT, and basic retrieval behavior
- [Anthropic Contextual Retrieval](https://www.anthropic.com/engineering/contextual-retrieval)
  for contextual chunks, lexical and semantic retrieval, and reranking
- [ColBERTv2](https://arxiv.org/abs/2112.01488) for late-interaction retrieval
- [RAPTOR](https://proceedings.iclr.cc/paper_files/paper/2024/hash/8a2acd174940dbca361a6398a4f9df91-Abstract-Conference.html)
  for hierarchical retrieval over long documents
- [Self-RAG](https://proceedings.iclr.cc/paper_files/paper/2024/file/25f7be9694d7b32d5cc670927b8091e1-Paper-Conference.pdf)
  and [Corrective RAG](https://arxiv.org/abs/2401.15884) for adaptive and
  corrective retrieval research
- [Cache-Augmented Generation](https://arxiv.org/abs/2412.15605) for research
  on using cached long context as an alternative for suitable bounded corpora
- [Lost in the Middle](https://aclanthology.org/2024.tacl-1.9/) for limitations
  in how language models use information distributed through long context

### Evaluation

- [ARES](https://aclanthology.org/2024.naacl-long.20/) for context relevance,
  answer faithfulness, and answer relevance evaluation
- [RAGAS](https://aclanthology.org/2024.eacl-demo.16/) for automated,
  reference-light RAG evaluation metrics
- [RAGChecker](https://proceedings.neurips.cc/paper_files/paper/2024/hash/27245589131d17368cccdfa990cbf16e-Abstract-Datasets_and_Benchmarks_Track.html)
  for fine-grained retrieval and generation diagnostics correlated with human
  judgments

These sources inform the design. Their provider APIs, storage products, and
research-specific algorithms are not adopted as TruvaG3 contracts. Versions,
licenses, operational maturity, and performance must be rechecked against the
real corpus before implementation.

## 23. Open Design Decisions

Implementation planning should resolve these decisions using a representative
corpus and measured evaluation results:

- Initial source systems and connector priority
- Knowledge Document IR schema and extension rules
- Parser benchmark corpus and parser-routing thresholds
- Default local source archive and job transport
- Domain vocabulary and ontology ownership
- Evidence-unit and section-boundary rules
- Entity-resolution keys and human-review thresholds
- Source-authority and conflict-precedence policy
- Review roles and approval policy by artifact class
- Local lexical, vector, and graph implementations
- Release-retention period for audit and in-flight execution
- Evaluation thresholds by domain risk
- Multi-tenant data and index isolation
- Runtime freshness and availability targets
- Packaging boundary between the foundry and Domain Knowledge Gateway
- When multiple implementations justify a reusable framework or shared library

These decisions should not be inferred from illustrative names or schemas in
this blueprint.
