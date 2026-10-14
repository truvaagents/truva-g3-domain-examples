# Operator-only replay admission

This diagnostic is not a service, autonomous workflow, or credential sanitizer.
It captures requests offline by default. Live mode requires separate approval
of paid calls; a matching hash is an admission check, not authorization to spend.

1. Export the source interactions to a private local file. Review and sanitize
   the complete file, including prompts, system text and unused interactions.
   The four marker checks in the diagnostic are only defense-in-depth.
2. Run an offline capture with a credential-free source execution/export ID:

   ```sh
   # From software-maintenance/, using its published module dependencies.
   GOWORK=off go run ./diagnostics/m1-replay \
     -fixture-source execution-export-id < reviewed-input.json
   ```

3. Review the `fixture_admission` record and request captures. Approval binds the
   exact input bytes **and** source identifier to `admission_sha256`. Even a
   whitespace change requires a new capture/review. Prompt contents are unchanged.
4. Only after explicit paid-run authorization, use the same file and source in
   the approved Kind diagnostic pod, running this diagnostic's compiled binary
   with `-live -fixture-source execution-export-id
   -approved-fixture-sha256 <reviewed-digest>`. The normal agent/tool images do
   not contain that binary; an operator-reviewed diagnostic deployment is a
   separate step, not part of `deploy-all`.
   A missing/mismatching digest or source fails before credential access,
   transport construction or the one-run marker. The existing model, routing,
   zero-retention checks, eight selected samples, disabled retries and maximum
   16 provider attempts remain unchanged. This does not authorize 16 paid calls.

Do not embed credentials in a source identifier, commit private exports, or
remove the one-run marker to retry without permission. Admission checks can be
tested offline; live behavior remains a separate acceptance gate.
