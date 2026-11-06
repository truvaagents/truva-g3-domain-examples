# Maintenance workspace tool — implementation reference

Implemented with published TruvaG3 v0.6.1 dependencies. See the
[README](../../README.md) for deployment and sandbox requirements.

## Responsibility and capabilities

The workspace tool owns immutable source workspaces, staged candidates,
regression artifacts, Kubernetes test Jobs and trusted verification receipts.
It is passive: callers propose bounded changes and choose their investigation.

[tool.go](tool.go) declares:

- `create_workspace`: retrieve and verify an authorized source snapshot; return
  profile-approved command IDs and numeric staging/artifact limits.
- `stage_candidate`: check base-file digests and stage exact candidate/test bytes.
- `run_experiment`: submit an owner-allowed command for an immutable target.
- `run_verification`: submit the protected reproduction and candidate suite.
- `get_job`: reconcile actual Job state; optional bounded waiting observes
  the existing Job and leaves its state unchanged when the wait expires.
- `read_job_artifact`: read bounded exact byte ranges; `format=text` supplies
  readable UTF-8 when possible, otherwise base64. Omitted format retains base64.
- `get_review_bundle`: review-identity-only candidate previews and receipts.
- `get_publication_bundle`: internal publication-identity-only eligible bytes
  and verification evidence.

Verification checks reproduction and protected-suite results, log completeness
and exact candidate scope. A submitted Job requires reconciliation before its
outcome is established. Concurrent updates can return
`WORKSPACE_EVIDENCE_CHANGED`; re-read the same reference for a consistent record.

## Sandbox and policy

The profile fixes image, commands, namespace/storage and resource limits.
Callers cannot supply arbitrary pod specifications or credentials. Sandbox Jobs
have no service-account token, secrets or host mounts. The workspace tool alone
has the required Kubernetes Job permissions; it has no GitHub credential.

The current profile has `verification_ready: true`, a digest-pinned Go 1.27.2
runner and a 300-second Job deadline. Jobs run as nonroot UID 10002 with a
read-only root filesystem, dropped capabilities and RuntimeDefault seccomp.
Runner changes require propagating the dependent profile fingerprints.

Independent environment gates require verified network-isolation attestation
or explicitly authorized development-only functional acceptance. Kind
NetworkPolicy enforcement depends on the installed CNI/version. The verified
local functional environment allows pinned module downloads; it does not
establish production-grade isolation.

## Discovery contracts

Authorized workspace creation projects `allowed_command_ids` and numeric
`limits` outside the immutable stored receipt. Command arguments, runner
configuration and caller lists remain private. Discovery hints describe target
and candidate rules, command selection, idempotency keys, staging bounds and
artifact byte ranges using those authorized response values.

The accompanying capability updates support composition across the example:

- Source tree discovery advertises optional `next_after` and `path_prefix`;
  local pagination and upstream coverage remain separate.
- Publication discovery exposes profile-derived title/body byte limits and
  receipt provenance fields used by callers.
- Repair/review summaries mark `failed_operations` optional; repair also
  advertises confidence, limitations and its profile hash.
- Objective, source-query, byte-offset and observation-window hints state the
  existing validators' constraints. Repair rejection feedback reports the
  selected profile's objective byte limit.

These additions preserve authorization, receipt identities, verification gates,
agent-owned skills and autonomous planning. Focused contract tests compare
registered metadata with serialized responses and exercise the framework's
HTTP catalog enrichment and planner rendering. Coverage includes pagination,
UTF-8 byte limits, authorized command/limit disclosure and strict receipt reads.

## Verification

The latest local Kind acceptance on 2026-11-05 exercised this implementation with
published v0.6.1 modules and Go 1.27.2, without local framework replacements:

- Module verification, full Go tests, vet and build passed. Shell syntax,
  all 47 offline setup checks and configuration validation passed.
- All eight components were built/deployed through `setup.sh`; running image
  and binary identities matched the builds. All 12 live discovery checks passed,
  and actual provider planning prompts contained the new capability metadata.
- Ordinary catalog-product traffic triggered scheduled collection and a complete
  repair/review/publication run: 62 successful tool steps and 44 successful
  OpenRouter model calls. The independent review approved the candidate.
- Both baseline reproductions failed only the intended regression; candidate
  tests recorded 39 passing test events and vet exited zero. All four complete
  artifacts matched trusted receipt hashes and actual Kubernetes logs.
- [Retail Order Service draft PR #29](https://github.com/truvaagents/retail-order-service/pull/29)
  contained exactly the staged source/test bytes. Two concurrent-evidence read
  conflicts recovered through identical-reference retries. No provider retry
  occurred, so provider retry recovery was not exercised.

A separate existing-PR investigation created no duplicate, but its application
result was inconclusive because historical assessment receipts had expired.
Accepted existing-remediation linkage was not established by that run.

Collection was disabled after both terminal runs, with matching original
profile bytes and all eight deployments ready. Registry Viewer executions,
prompts, skills, logs, traces, assessment and coordinator memory records were
inspected. Existing-cluster functional acceptance does not cover a cold
bootstrap or an external fork.

The protected suite currently has no formatting gate. Approval and publication
are separate components' responsibilities. Operate through the solution setup
script on container port 8401. Review retained files before cleanup: deleting
the workspace PVC can remove candidates and artifacts. No generic framework
sandbox module or crash-resumable orchestration is claimed.
