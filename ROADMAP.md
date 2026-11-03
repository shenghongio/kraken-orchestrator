# Roadmap

This roadmap is an engineering plan, not a delivery promise. Every milestone must be marked **Done** only when its exit criteria are satisfied by code, automated tests, deployment evidence, and updated documentation.

## Status vocabulary

- **Not started** — no implementation evidence reviewed.
- **In progress** — implementation exists but acceptance evidence is incomplete.
- **Blocked** — progress depends on a named decision or external prerequisite.
- **Done** — all exit criteria are met and evidence is linked.
- **Deferred** — deliberately excluded from the current release scope.

The initial status of the milestones below is **Not started / not verified** because this documentation task did not inspect the full source tree.

## Principles and sequencing

1. Prove the core task lifecycle before adding platform integrations.
2. Keep PostgreSQL authoritative for task state.
3. Add one real Executor and prove restart/retry behavior before expanding Executor types.
4. Deploy and operate the single-host Compose topology before pursuing multi-host scale-out.
5. Add Kubernetes inventory before Kubernetes-native scheduling or packaging.
6. Add eBPF only for a measured observability question, with a safe degraded mode.
7. Add etcd only when a concrete coordination requirement cannot be met more simply by PostgreSQL or Kubernetes Lease.

## Milestones

### M0 — Repository foundation

**Scope**
- Confirm module path and canonical executable names.
- Create the three command entry points.
- Implement configuration, structured logging, request IDs, graceful shutdown, `/healthz`, and `/readyz`.
- Define domain types, explicit state transitions, error taxonomy, and the Executor interface.
- Establish migrations, test conventions, linting, and CI.

**Exit criteria**
- All three binaries build from a clean checkout.
- Configuration validation fails fast with actionable errors and never logs secrets.
- Shutdown stops accepting new work, cancels active operations, and closes resources.
- Unit tests cover status transitions and input validation.
- CI runs formatting, static analysis, unit tests, and build checks.

### M1 — Reliable task persistence and API

**Scope**
- PostgreSQL schema and migration lifecycle.
- Task submission, querying, listing, cancellation request, and idempotency.
- `tasks`, `task_attempts`, `task_leases`, `agents`, `task_events`, and `outbox_events` as justified by query and lifecycle requirements.
- Explicit API request/response contracts and error codes.

**Exit criteria**
- State-changing operations are transactional.
- Duplicate submission with the same idempotency key behaves deterministically.
- Invalid transitions are rejected.
- API tests cover validation, authorization boundaries, and duplicate requests.
- Backup and restore are documented and tested against a disposable environment.

### M2 — Reliable Agent delivery and recovery

**Scope**
- Agent registration, heartbeat, claim, completion callbacks, cancellation, lease renewal, and lease reaper.
- Attempt numbers and monotonically increasing `lease_epoch` fencing tokens.
- SQLite local checkpoint and callback outbox.
- Reconciliation after Agent restart.

**Exit criteria**
- Concurrent Agents cannot claim the same current task assignment.
- Expired assignments are invalidated before requeue or failure.
- A stale callback cannot overwrite a newer attempt.
- Duplicate callbacks are idempotent.
- Agent restart, Scheduler restart, network interruption, and PostgreSQL interruption are fault-tested.
- No exactly-once claim is made; semantics remain at-least-once plus idempotent effects.

### M3 — Single-host Compose deployment

**Scope**
- Scheduler, Agent, PostgreSQL, and optional Redis services.
- Persistent volumes for PostgreSQL and Agent state.
- Health checks, restart policies, network DNS, secrets, resource limits, and dependency reconnection.
- Host-side `krakenctl` build and use.

**Exit criteria**
- A clean environment can be brought up using checked-in documentation.
- Restarting each service does not silently lose durable task state.
- Redis outage does not corrupt or hide PostgreSQL-backed tasks.
- Agent can reconnect and reconcile after dependency recovery.
- Compose host failure is explicitly documented as a full failure-domain outage.

### M4 — First production-grade Executor: download

**Scope**
- Input validation and SSRF controls.
- HTTP Range support only when the server confirms it.
- Chunk/checkpoint persistence and remote-resource identity (`ETag`, `Last-Modified`, size, checksum where available).
- Task/attempt-specific temporary paths and atomic publication.
- Disk-space admission checks and per-type concurrency limits.

**Exit criteria**
- Resume works after process restart and partial network failure.
- Resource changes are detected; stale partial data is not silently published.
- Final size and checksum are validated when available.
- Credentials and signed URLs are redacted.
- Tests cover disk-full, checksum mismatch, cancellation, and server without Range support.

### M5 — Kubernetes inventory with client-go

**Scope**
- Optional client-go module, kubeconfig and in-cluster configuration.
- Read-only Node and Pod discovery.
- Shared Informers, cache sync, event handlers, and bounded workqueue.
- Agent Pod metadata and Node association.
- Minimal RBAC and Kind integration tests.

**Exit criteria**
- Informer initial cache sync is required before using cached inventory.
- Relist/reconnect and stale cache behavior are tested.
- Pod lifecycle is not treated as a substitute for Agent heartbeat or Scheduler lease state.
- API Server outage degrades inventory only; task persistence and core execution semantics remain intact.
- Feature can be disabled without disabling the base Scheduler.

### M6 — eBPF runtime observer

**Scope**
- Start with one measurable question, such as process-associated network or selected I/O telemetry.
- Use a Go eBPF library and generated bindings where appropriate.
- Capability/kernel feature detection, bounded event transport, dropped-event counters, and safe disablement.
- Correlate Agent task execution with process/cgroup identity where the platform can guarantee the mapping.
- Export bounded-cardinality metrics.

**Exit criteria**
- Supported kernel, architecture, cgroup mode, and privilege requirements are documented and tested.
- Missing BPF capabilities do not prevent Agent task execution.
- Event loss and collector restarts are observable.
- Measurement overhead is recorded under representative load.
- Documentation explicitly states the limitations of per-process and per-task I/O attribution.

### M7 — Optional coordination backend

**Entry condition**

A concrete single-instance control loop or cross-process coordination requirement has been documented and cannot be handled adequately by existing PostgreSQL transactions or, in Kubernetes, Kubernetes Lease.

**Scope**
- `LeaderElector` interface and one implementation at a time.
- etcd Session/Lease, Campaign, leader-loss cancellation, and recovery tests; or Kubernetes Lease when deployed inside Kubernetes.
- TLS, authentication, availability, and operational ownership.

**Exit criteria**
- Leadership is not confused with fencing or exclusive execution.
- A former leader stops protected work promptly after losing its session/lease.
- Critical database writes retain independent transactional and fencing checks.
- Quorum loss and API latency behavior are documented.
- If no concrete requirement exists, this milestone remains Deferred and no etcd dependency is added.

### M8 — Production hardening and scale-out

**Scope**
- Multiple Agent hosts and bounded concurrency.
- SLOs, dashboards, alerts, logs, tracing where useful, runbooks.
- PostgreSQL backup/restore drills and capacity management.
- Load, soak, and fault-injection tests.
- Security review and dependency update policy.

**Exit criteria**
- Measured throughput and latency are reproducible and include environment details.
- Failure recovery targets are defined and met in tests.
- Resource limits and backpressure are validated.
- Operational runbooks are exercised, not merely written.
- Supported scale claims are tied to measured test results.

## Deferred from the first release

- Splitting Scheduler into many microservices.
- Exactly-once execution guarantees.
- Complex conditional DAG workflows.
- Multi-tenancy and a full permission-management system.
- Web UI.
- Kubernetes DaemonSet packaging before the single-host reliability path is proven.
- Multiple Scheduler instances before transaction, lease, and recovery tests are strong.

## Release gate checklist

A release is not ready until the maintainer has reviewed:

- [ ] Scope and compatibility notes.
- [ ] Migrations and rollback/forward-fix strategy.
- [ ] Unit, integration, race, and relevant fault-injection tests.
- [ ] Security and secret-redaction impact.
- [ ] Operational metrics, logs, and alerts.
- [ ] Deployment and recovery documentation.
- [ ] Changelog entry and tagged commit.
- [ ] Known limitations and unverified claims.
