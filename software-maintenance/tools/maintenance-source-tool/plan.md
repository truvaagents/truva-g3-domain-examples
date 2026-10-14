# Maintenance source tool — implementation reference

Current as of the v0.6.0 acceptance run on 2026-10-13. Setup and profile
preparation are in the [README](../../README.md) and
[profile guide](../../PROFILE_SETUP_GUIDE.md).

## Responsibility and capabilities

This passive tool supplies bounded, read-only source evidence for a server-owned
repository profile. It does not diagnose, execute repository code or publish.

[tool.go](tool.go) declares the current schemas:

- `resolve_source_revision`: resolve an allowed named ref or exact commit to
  immutable commit/tree identities, readiness and numeric read limits.
- `list_source_tree`: sorted bounded tree page with coverage information.
- `read_source_file`: bounded UTF-8 bytes and full-file digest.
- `search_source`: bounded literal search, with explicit scan coverage.
- `create_source_snapshot`: deterministic sanitized archive of immutable source.

The internal `get_source_snapshot` capability is hidden from planner discovery
and restricted to the workspace-tool identity. The workspace reconstructs the
archive and verifies its digest. Models inspect selected files; a full permitted
snapshot is transferred between tools, not loaded wholesale into the prompt.

## Controls

Server-owned profiles fix repository identity, allowed refs, path exclusions and
byte/file/search/snapshot limits. Requests cannot select arbitrary upstream URLs.
Operations after resolution require exact commit SHAs. Denied credential,
workflow and policy paths remain excluded. Binary or omitted content produces
explicit coverage limitations.

The tool uses read-scoped GitHub credentials, traced HTTP, Redis topology/keyspace
APIs and the framework tool lifecycle. Snapshot metadata/archive storage is
atomic with matching expiry. It has no GitHub write authority.

## Readiness and verification

The supplied source profile now has `assessment_ready: true`; the original
uncommitted-source limitation is no longer the shipped configuration. Readiness
still depends on the operator's repository, contract and deployed build agreeing.

All maintenance modules pin v0.6.0. The live existing-cluster run resolved the
target's immutable source, inspected files and supplied the repair snapshot.
It does not prove access to a new user's fork. The target-service repository
provenance portability issue remains an explicit [release gate](../../README.md#public-release-readiness).
Use `setup.sh` from the solution root; the tool runs on container port 8399.
