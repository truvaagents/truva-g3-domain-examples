# Domain Knowledge Gateway Tool

This domain-neutral tool exposes four stable evidence capabilities while hiding
the persistence engine behind `knowledge.KnowledgeStore`. Its runtime container
uses LadybugDB v0.17.0 as an embedded library and stores the database file on an
`emptyDir`; ordinary unit tests use the in-memory reference adapter.
Every retrieval path enforces release validity and explicit tenant,
jurisdiction, channel, and time scope. Semantic aliases live in each domain pack
rather than in gateway code, and exact reads issue signed receipts consumed by
the deterministic policy boundary.

Use the parent [`setup.sh`](../setup.sh) for every build and deployment action.
This directory was copied from `truva-g3/examples/my-tool`; the template was not modified.
