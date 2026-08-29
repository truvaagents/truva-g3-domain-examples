(function () {
  "use strict";

  const workflow = [
    {
      actor: "Caller → Agent webhook",
      title: "Submit an order-risk event",
      explanation: "A caller sends a small JSON event. It includes a unique event ID, order ID, risk signal, requested option, and decision time.",
      input: "Order-risk JSON",
      output: "HTTP 202, task ID, and status URL",
      why: "The caller does not need to keep a connection open while the investigation runs."
    },
    {
      actor: "Agent webhook → Redis → Worker",
      title: "Deduplicate and queue the task",
      explanation: "The agent checks the event ID. A new event becomes a durable task in Redis. A duplicate event returns the original task instead of starting the work again.",
      input: "Validated event and idempotency key",
      output: "One queued task",
      why: "A retry from the caller must not create a second refund or replacement."
    },
    {
      actor: "Agent → Skills API",
      title: "Load the operating procedure",
      explanation: "The framework activates version 8 of the commerce skill. The skill tells the agent which safe procedure to follow. It does not contain the large policy collection.",
      input: "Task objective and skill name",
      output: "Procedure, constraints, and capability guidance",
      why: "The operating method can be changed and audited separately from the agent code and the policy corpus."
    },
    {
      actor: "Agent → Commerce Core",
      title: "Read current business state",
      explanation: "The agent asks for the order context and the bounded fulfillment options. Commerce Core returns structured facts, including the order version.",
      input: "Order ID",
      output: "Order facts and permitted option shapes",
      why: "The decision must use current operational facts, not facts invented or remembered by the language model."
    },
    {
      actor: "Agent → Knowledge Gateway",
      title: "Search the pinned policy release",
      explanation: "The agent pins one immutable domain-pack release, searches with metadata filters, and may follow graph relationships such as overrides or requirements.",
      input: "Release ID, query, market, carrier, product, and date",
      output: "Ranked candidate sections with source metadata",
      why: "Search narrows a large corpus. It does not make the final authority decision."
    },
    {
      actor: "Agent → Knowledge Gateway",
      title: "Read the exact authoritative sections",
      explanation: "For each important candidate, the agent requests the exact section. The gateway returns the text plus a short-lived, signed evidence receipt.",
      input: "Pinned release and exact section ID",
      output: "Verbatim section, provenance, content hash, and receipt",
      why: "A ranked search snippet is not enough to authorize a real business action."
    },
    {
      actor: "Agent → Commerce Core",
      title: "Ask deterministic code for the decision",
      explanation: "The agent sends an order ID, proposed option, pinned release, exact section IDs, and evidence receipts. Commerce Core reads the current order itself, verifies the proof, and applies fixed Go rules.",
      input: "Order ID, option, release hash, section IDs, receipts, and decision time",
      output: "allowed, approval_required, denied, insufficient_evidence, or conflicting_evidence",
      why: "The language model can assemble the case, but it cannot grant itself authority."
    },
    {
      actor: "Framework HITL checkpoint",
      title: "Pause if human approval is required",
      explanation: "A low-risk allowed action continues. An approval-class action pauses immediately before the command. Redis stores a checkpoint and the worker becomes free.",
      input: "Policy outcome and approval requirement",
      output: "Continue automatically or wait for a human decision",
      why: "A long approval wait does not occupy a worker or bypass business authority."
    },
    {
      actor: "Agent → Commerce Core",
      title: "Revalidate and execute once",
      explanation: "Commerce Core checks the order version, policy release, receipts, decision token, approval proof, and idempotency key again. Only then can it mutate the synthetic order.",
      input: "Eligible decision plus current-state and approval proofs",
      output: "Completed command or a safe conflict/error",
      why: "The world may change after the decision. Execution therefore never trusts an old decision blindly."
    },
    {
      actor: "Agent → Commerce Core",
      title: "Read the order again and verify",
      explanation: "The agent asks Commerce Core for the post-command state. Verification confirms that the expected order version and fulfillment status are present.",
      input: "Order ID and expected outcome",
      output: "Verified final state",
      why: "An accepted command is not the same as a completed business outcome."
    },
    {
      actor: "Agent, Registry, Jaeger, and notification capability",
      title: "Record the result and communicate",
      explanation: "After verification, the demo records a synthetic notification. Task state, capability calls, LLM calls, and distributed trace spans provide different views of the same execution.",
      input: "Verified outcome and execution context",
      output: "Terminal task result, audit records, trace, and notification record",
      why: "Operators need both a business result and evidence showing how the system reached it."
    }
  ];

  const scenarios = {
    normal: {
      level: "Start here",
      mutation: "No agent mutation",
      mutationClass: "no",
      title: "A healthy order stays on the normal path",
      story: "ORD-1000 is confirmed, paid, and already has reserved inventory.",
      behaviour: "The setup driver calls a deterministic normal-order capability directly. The agent and language model are not invoked.",
      result: "<code>normal_order_confirmed</code>",
      learning: "AI should not be added to a business path that already has a clear, fixed answer.",
      evidence: "Direct result for ORD-1000: <code>agent_invoked=false</code> and <code>llm_invoked=false</code>."
    },
    automatic: {
      level: "Core success path",
      mutation: "Order changed once",
      mutationClass: "yes",
      title: "A low-value replacement is recovered automatically",
      story: "ORD-1003 is missing in transit, costs 69 USD, is not restricted, and has replacement inventory.",
      behaviour: "The agent gathers exact evidence. Deterministic policy code returns allowed. Commerce Core revalidates, creates one replacement, verifies version 2, and records a notification.",
      result: "<code>recovered</code> · <code>allowed</code> · <code>verified=true</code>",
      learning: "The agent coordinates the investigation. Fixed code owns authority and mutation.",
      evidence: "Execution <code>order-operations-1787446133236523689</code>; trace <code>f3952cb932773e34bedaff89cd2df4b5</code>."
    },
    approval: {
      level: "Human-in-the-loop",
      mutation: "Only after approval",
      mutationClass: "controlled",
      title: "A high-value action waits for a person",
      story: "The policy permits the option, but the order value is above the automatic-action threshold.",
      behaviour: "The framework stores a checkpoint before execution. Approval resumes the workflow, which revalidates all state and evidence before calling the command.",
      result: "<code>approval_required</code> → approved → executed → verified",
      learning: "The approval window may be long, but evidence and order state must still be current at execution time.",
      evidence: "Initial <code>order-operations-1787431818524531555</code>; checkpoint <code>cp-171eded2-66d8-42</code>; resume <code>order-operations-1787431933621096526</code>."
    },
    denied: {
      level: "Deterministic stop",
      mutation: "No mutation",
      mutationClass: "no",
      title: "A forbidden option is denied",
      story: "The requested recovery option violates an applicable restriction or a controlling policy rule.",
      behaviour: "Commerce Core returns denied. The agent removes the execution chain and finishes without calling the command capability.",
      result: "<code>denied</code>",
      learning: "A denial is a successful safety outcome, not a system failure.",
      evidence: "Execution <code>order-operations-1787432041838325142</code>; task <code>order-event-manual-denied-20260822-d1</code>."
    },
    insufficient: {
      level: "Safe abstention",
      mutation: "No mutation",
      mutationClass: "no",
      title: "Required authority is missing",
      story: "The retrieved evidence does not include every section required to authorize the requested action.",
      behaviour: "The deterministic evaluator returns insufficient_evidence. The workflow reports which authority is missing and stops.",
      result: "<code>insufficient_evidence</code>",
      learning: "The system must say “I do not have enough evidence” instead of asking the language model to guess.",
      evidence: "Execution <code>order-operations-1787439551802679081</code>."
    },
    conflict: {
      level: "Safe abstention",
      mutation: "No mutation",
      mutationClass: "no",
      title: "Two current policy sections disagree",
      story: "Applicable evidence supports both permission and prohibition, and the declared precedence rules do not resolve the conflict.",
      behaviour: "Commerce Core returns conflicting_evidence. No eligible execution chain is created and the order is unchanged.",
      result: "<code>conflicting_evidence</code>",
      learning: "More retrieved text does not always create certainty. Unresolved conflict must stop action.",
      evidence: "Execution <code>order-operations-1787439074847452187</code>."
    },
    duplicate: {
      level: "Retry protection",
      mutation: "At most one mutation",
      mutationClass: "controlled",
      title: "The same event arrives twice",
      story: "A caller retries because it did not receive or trust the first HTTP response.",
      behaviour: "The webhook recognises the same event ID and returns the original task. The second delivery does not start another investigation or command.",
      result: "<code>DUPLICATE_EVENT</code> with the original task identity",
      learning: "Network retries are normal. Business effects still need exactly-once protection.",
      evidence: "Execution <code>order-operations-1787432364863440680</code>; trace <code>86950528276054615c029fda63d0dd2f</code>."
    },
    "state-change": {
      level: "Race-condition guard",
      mutation: "Recovery blocked",
      mutationClass: "no",
      title: "The order changes while approval is waiting",
      story: "A person approves, but another process changed the order after the original decision.",
      behaviour: "On resume, Commerce Core compares the current order version with the approved basis. It returns HTTP 409 and aborts the checkpoint.",
      result: "<code>state_changed_during_hitl</code> · replan required",
      learning: "Human approval does not make stale order data safe.",
      evidence: "Resume execution <code>order-operations-1787455319493659830</code>; trace <code>c05ae0bfcdf2b907eb7b565b63561ce7</code>."
    },
    "policy-change": {
      level: "Race-condition guard",
      mutation: "Recovery blocked",
      mutationClass: "no",
      title: "The policy release changes while approval is waiting",
      story: "A person approves the old basis, but a new policy release becomes active before execution.",
      behaviour: "On resume, Commerce Core detects that the release is no longer current. It returns HTTP 409 and aborts without a recovery command.",
      result: "<code>policy_changed_during_hitl</code> · replan required",
      learning: "Approval is tied to a specific evidence basis, not only to a human name and timestamp.",
      evidence: "Resume execution <code>order-operations-1787455389634165126</code>; trace <code>80f5d8d9cede1f8ae010d0aba248e871</code>."
    },
    reapproval: {
      level: "Fresh-basis recovery",
      mutation: "Fresh approval only",
      mutationClass: "controlled",
      title: "A new investigation and approval use the new policy",
      story: "The old approval became stale. The workflow investigates again, obtains a fresh decision, and asks for a new approval.",
      behaviour: "The old decision remains unusable. A new release, new evidence receipts, new decision token, and new checkpoint form a fresh execution basis.",
      result: "Old basis blocked → fresh basis approved → recovered",
      learning: "Replanning means rebuilding authority. It is not a retry of the rejected old command.",
      evidence: "Old <code>order-operations-1787445946381889583</code>; fresh decision <code>order-operations-1787446024682060648</code>; resume <code>order-operations-1787446065452372818</code>."
    },
    "other-domain": {
      level: "Portability proof",
      mutation: "Read-only proof",
      mutationClass: "controlled",
      title: "The gateway switches from commerce to IT policy",
      story: "The same gateway process activates the bundled it-ops-mini pack and answers an emergency production-access question.",
      behaviour: "The public gateway contract does not change. Release pinning, filtered search, relationship traversal, and exact-section reading operate on IT evidence instead of commerce evidence.",
      result: "Commerce release → IT release → commerce release restored",
      learning: "Retrieval infrastructure is reusable. Each domain still needs its own documents, schema, procedure skill, transaction tool, and deterministic rules.",
      evidence: "Traces <code>5eaa22e189a98203821891b6d8648a79</code>, <code>d1bb00a6b64dee8e15833dd4ed9a574e</code>, and <code>8436859d5cfc213db601f98aefe722f4</code>."
    }
  };

  function initialiseThemeToggle() {
    const toggle = document.getElementById("theme-toggle");
    const label = document.getElementById("theme-toggle-label");
    if (!toggle || !label) return;

    const root = document.documentElement;

    function applyTheme(theme, savePreference) {
      const dark = theme === "dark";
      root.setAttribute("data-theme", dark ? "dark" : "light");
      toggle.setAttribute("aria-pressed", dark ? "true" : "false");
      toggle.setAttribute("aria-label", dark ? "Switch to light mode" : "Switch to night mode");
      toggle.title = dark ? "Switch to light mode" : "Switch to night mode";
      label.textContent = dark ? "Light mode" : "Night mode";
      if (savePreference) {
        try {
          localStorage.setItem("agentic-commerce-tutorial-theme", dark ? "dark" : "light");
        } catch (error) {
          // The selected theme still works when browser storage is unavailable.
        }
      }
    }

    applyTheme(root.getAttribute("data-theme") === "dark" ? "dark" : "light", false);
    toggle.addEventListener("click", () => {
      applyTheme(root.getAttribute("data-theme") === "dark" ? "light" : "dark", true);
    });
  }

  function initialiseWorkflow() {
    const buttons = Array.from(document.querySelectorAll("[data-workflow-step]"));
    const previous = document.getElementById("workflow-previous");
    const next = document.getElementById("workflow-next");
    if (!buttons.length || !previous || !next) return;

    let selected = 0;
    const actor = document.getElementById("workflow-actor");
    const count = document.getElementById("workflow-count");
    const title = document.getElementById("workflow-title");
    const explanation = document.getElementById("workflow-explanation");
    const input = document.getElementById("workflow-input");
    const output = document.getElementById("workflow-output");
    const why = document.getElementById("workflow-why");

    function selectStep(index) {
      selected = Math.max(0, Math.min(workflow.length - 1, index));
      const step = workflow[selected];
      actor.textContent = step.actor;
      count.textContent = `Step ${selected + 1} of ${workflow.length}`;
      title.textContent = step.title;
      explanation.textContent = step.explanation;
      input.textContent = step.input;
      output.textContent = step.output;
      why.textContent = step.why;
      buttons.forEach((button, buttonIndex) => {
        button.setAttribute("aria-pressed", buttonIndex === selected ? "true" : "false");
      });
      previous.disabled = selected === 0;
      next.disabled = selected === workflow.length - 1;
      buttons[selected].scrollIntoView({ block: "nearest", inline: "nearest" });
    }

    buttons.forEach((button) => {
      button.addEventListener("click", () => selectStep(Number(button.dataset.workflowStep)));
    });
    previous.addEventListener("click", () => selectStep(selected - 1));
    next.addEventListener("click", () => selectStep(selected + 1));
    selectStep(0);
  }

  function initialiseScenarios() {
    const buttons = Array.from(document.querySelectorAll("[data-scenario]"));
    if (!buttons.length) return;

    const level = document.getElementById("scenario-level");
    const mutation = document.getElementById("scenario-mutation");
    const title = document.getElementById("scenario-title");
    const story = document.getElementById("scenario-story");
    const behaviour = document.getElementById("scenario-behaviour");
    const result = document.getElementById("scenario-result");
    const learning = document.getElementById("scenario-learning");
    const evidence = document.getElementById("scenario-evidence");

    function selectScenario(key) {
      const scenario = scenarios[key];
      if (!scenario) return;
      level.textContent = scenario.level;
      mutation.textContent = scenario.mutation;
      mutation.className = `mutation-badge ${scenario.mutationClass}`;
      title.textContent = scenario.title;
      story.textContent = scenario.story;
      behaviour.textContent = scenario.behaviour;
      result.innerHTML = scenario.result;
      learning.textContent = scenario.learning;
      evidence.innerHTML = scenario.evidence;
      buttons.forEach((button) => {
        button.setAttribute("aria-pressed", button.dataset.scenario === key ? "true" : "false");
      });
    }

    buttons.forEach((button) => {
      button.addEventListener("click", () => selectScenario(button.dataset.scenario));
    });
    selectScenario("normal");
  }

  function initialiseScaleCalculator() {
    const pods = document.getElementById("pod-count");
    const workers = document.getElementById("worker-count");
    const events = document.getElementById("event-count");
    if (!pods || !workers || !events) return;

    const podsValue = document.getElementById("pod-count-value");
    const workersValue = document.getElementById("worker-count-value");
    const eventsValue = document.getElementById("event-count-value");
    const active = document.getElementById("active-tasks");
    const queued = document.getElementById("queued-tasks");
    const formula = document.getElementById("scale-formula");

    function update() {
      const podCount = Number(pods.value);
      const workerCount = Number(workers.value);
      const eventCount = Number(events.value);
      const slots = podCount * workerCount;
      const activeCount = Math.min(eventCount, slots);
      const queuedCount = Math.max(0, eventCount - activeCount);
      podsValue.textContent = String(podCount);
      workersValue.textContent = String(workerCount);
      eventsValue.textContent = String(eventCount);
      active.textContent = String(activeCount);
      queued.textContent = String(queuedCount);
      formula.textContent = `${podCount} ${podCount === 1 ? "pod" : "pods"} × ${workerCount} ${workerCount === 1 ? "worker" : "workers"} = ${slots} task ${slots === 1 ? "slot" : "slots"}`;
    }

    [pods, workers, events].forEach((input) => input.addEventListener("input", update));
    update();
  }

  document.addEventListener("DOMContentLoaded", () => {
    initialiseThemeToggle();
    initialiseWorkflow();
    initialiseScenarios();
    initialiseScaleCalculator();
  });
})();
