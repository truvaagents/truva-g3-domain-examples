# Maintenance workspace tool — implementation reference

Current as of the v0.6.0 acceptance run on 2026-10-13. See the
[README](../../README.md) for deployment and sandbox requirements.

## Responsibility and capabilities

The workspace tool owns immutable source workspaces, staged candidates,
regression artifacts, Kubernetes test Jobs and trusted verification receipts.
It is passive: callers propose bounded changes; the tool does not diagnose or
choose a repair plan.

[tool.go](tool.go) declares:

- `create_workspace`: retrieve and verify an authorized source snapshot.
- `stage_candidate`: check base-file digests and stage exact candidate/test bytes.
- `run_experiment`: submit an owner-allowed command for an immutable target.
- `run_verification`: submit the protected reproduction and candidate suite.
- `get_job`: reconcile actual Job state; optional bounded waiting observes
  the existing Job and does not cancel it when the wait expires.
- `read_job_artifact`: bounded exact byte ranges; `format=text` supplies
  readable UTF-8 when possible, otherwise base64. Omitted format retains base64.
- `get_review_bundle`: review-identity-only candidate previews and receipts.
- `get_publication_bundle`: internal publication-identity-only eligible bytes
  and verification evidence.

A submitted Job is not proof of success. Verification checks reproduction and
protected-suite results, log completeness and exact candidate scope. Concurrent
updates can return `WORKSPACE_EVIDENCE_CHANGED`; re-read the same reference to
obtain a consistent record rather than bypassing the gate.

## Sandbox and policy

The profile fixes image, commands, namespace/storage and resource limits.
Callers cannot supply arbitrary pod specifications or credentials. Sandbox Jobs
have no service-account token, secrets or host mounts. The workspace tool alone
has the required Kubernetes Job permissions; it has no GitHub credential.

The current profile has `verification_ready: true`, a digest-pinned runner and
a 300-second Job deadline. Independent environment gates require verified
network-isolation attestation or explicitly authorized development-only
functional acceptance. Kind NetworkPolicy behavior depends on the installed
CNI/version; infer neither enforcement nor its absence from the cluster name.
The local functional run allowed pinned module downloads and is not a production
isolation certification.

## Verification

The v0.6.0 run exercised real Kubernetes Jobs: the regression failed on baseline,
candidate tests and vet passed, and exact receipts supported review/publication.
The current protected suite does not include a formatting gate. Approval and
publication are separate components' responsibilities.

Use the solution setup script on container port 8401. Review retained files
before cleanup: deleting the workspace PVC can remove candidates and artifacts.
No generic framework sandbox module or crash-resumable orchestration is claimed.
