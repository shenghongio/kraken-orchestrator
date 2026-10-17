# AGENTS.md --- kraken-orchestrator

## 1. Project identity and goals

-   **Repository/project name:** `kraken-orchestrator`
-   **Scheduler binary:** `kraken-scheduler`
-   **Worker binary:** `kraken-agent`
-   **CLI binary:** `krakenctl`
-   **Current environment:** one machine with Docker available.
-   **Initial deployment:** Docker Compose runs Scheduler, one Agent,
    PostgreSQL, and Redis on the same machine.
-   **Target scale:** approximately 10--50 Agents in a later phase.
-   **Workload:** download, rclone, log collection, AI analysis, and
    other extensible task types. Tasks may run for hours, days, or
    weeks.
-   **Primary quality goal:** reliable execution and recovery.

Build a modular Go repository with three executable entry points. Keep
Scheduler and Agent as separate processes, but do not split the project
into many microservices prematurely. The architecture must work on one
Docker host now and allow Agents to be deployed on other machines later.

## 2. Naming conventions

Use these names consistently in source code, documentation, logs,
container configuration, and examples:

  -----------------------------------------------------------------------
  Item                                Name
  ----------------------------------- -----------------------------------
  Repository/project                  `kraken-orchestrator`

  Scheduler executable                `kraken-scheduler`

  Agent executable                    `kraken-agent`

  CLI executable                      `krakenctl`

  Suggested Compose service names     `scheduler`, `agent`, `postgres`,
                                      `redis`

  Suggested image names               `kraken-scheduler`, `kraken-agent`
  -----------------------------------------------------------------------

Go command entry points should be:

-   `cmd/kraken-scheduler/main.go`
-   `cmd/kraken-agent/main.go`
-   `cmd/krakenctl/main.go`

Do not use the old generic names `scheduler`, `agent`, or `cli` as
binary names. Those words may still be used for package names, Compose
service names, or domain concepts where appropriate.

The Go module path should match the repository's actual remote URL once
it is known. Do not invent a Git hosting domain. Keep internal package
imports consistent with the `module` directive in `go.mod`.

## 3. Architecture

### `kraken-scheduler`

A long-running service responsible for:

-   HTTP API for task submission, querying, cancellation, Agent
    registration, task claiming, heartbeat, and callbacks.
-   Task state transitions and scheduling.
-   Atomic task claiming and lease management.
-   Retry policy and recovery of expired leases.
-   PostgreSQL persistence and transactional outbox processing.
-   Metrics, structured logs, health and readiness endpoints.

### `kraken-agent`

A long-running worker responsible for:

-   Registering with Scheduler and reporting heartbeats.
-   Claiming tasks that match its capabilities and available
    concurrency.
-   Executing tasks through a pluggable Executor interface.
-   Persisting local execution checkpoints and pending callbacks in
    SQLite.
-   Reporting progress, success, failure, cancellation, and recoverable
    errors.
-   Reconciling unfinished work with Scheduler after restart.
-   Stopping or abandoning work when its assignment is no longer valid.

### `krakenctl`

A command-line client that calls Scheduler's HTTP API. It should support
commands such as:

``` bash
krakenctl task submit --file task.yaml
krakenctl task get <task-id>
krakenctl task list
krakenctl task cancel <task-id>
krakenctl agent list
krakenctl health
```

The exact command syntax can evolve, but keep it consistent and document
it. `krakenctl` is not the scheduler and must not be responsible for
running long-lived tasks. It normally runs on demand and exits after
completing a command.

## 4. Single-machine deployment first

Initially use Docker Compose to run:

-   `scheduler`: the `kraken-scheduler` process.
-   `agent`: one instance of `kraken-agent`.
-   `postgres`: authoritative task-state database.
-   `redis`: optional notification/cache/wakeup layer.

Run `krakenctl` on the host as a compiled binary by default. It may also
be run from a container when useful.

Deployment requirements:

-   Persist PostgreSQL data with a Docker volume.
-   Persist the Agent's SQLite database, checkpoints, and working data
    with a volume or explicit bind mount.
-   Make Scheduler and Agent reconnect safely when PostgreSQL, Redis, or
    the network is temporarily unavailable.
-   Use configuration/environment variables for service URLs,
    credentials, concurrency, paths, and timeouts.
-   Do not hard-code `localhost` for communication between containers;
    use Compose service DNS names on the Compose network.
-   Do not bake secrets into container images, source control, or logs.
-   Treat a single Docker host as a single failure domain. Compose
    restart policies help with process restarts but do not provide
    machine-level high availability.
-   Design Agent configuration so a future Agent on another machine can
    connect to Scheduler using a configurable URL and authenticated
    identity.

## 5. Suggested repository layout

``` text
kraken-orchestrator/
├── cmd/
│   ├── kraken-scheduler/
│   │   └── main.go
│   ├── kraken-agent/
│   │   └── main.go
│   └── krakenctl/
│       └── main.go
├── internal/
│   ├── config/
│   ├── domain/                 # Task, status, lease, attempt, errors
│   ├── scheduler/              # Submission, claiming, callback, cancellation, recovery
│   ├── agent/                  # Registration, heartbeat, runtime, runner, recovery
│   ├── executor/               # Executor interface and implementations
│   │   ├── download/
│   │   ├── rclone/
│   │   ├── log/
│   │   └── ai/
│   ├── lease/
│   ├── retry/
│   ├── dispatch/
│   ├── persistence/
│   │   ├── postgres/
│   │   └── sqlite/
│   ├── redis/
│   ├── transport/
│   │   └── http/
│   └── observability/
├── migrations/
├── configs/
├── deployments/
│   └── docker-compose.yml
├── scripts/
├── tests/
├── Dockerfile
├── go.mod
├── README.md
└── AGENTS.md
```

This is a suggested structure, not a requirement to create empty
packages immediately. Add packages when there is real implementation to
put in them. Keep reusable business logic in `internal/`; keep
`cmd/*/main.go` focused on configuration, dependency wiring, startup,
and shutdown.

## 6. Task persistence and state machine

PostgreSQL is the source of truth for task state. Redis must never be
the only place where a task or state transition exists.

Recommended initial tables:

1.  `tasks`: task payload, type, priority, status, attempt counters,
    retry time, idempotency key, result/error, current Agent, lease
    epoch, timestamps, cancellation request, optimistic version.
2.  `task_attempts`: one row per execution attempt, including Agent,
    lease epoch, status, progress, result/error, and start/end times.
3.  `task_leases`: current lease owner, attempt, epoch, expiry, and
    renewal time.
4.  `agents`: identity, status, version, capabilities, concurrency, last
    heartbeat.
5.  `task_dependencies`: reserved for simple task dependencies when
    implemented.
6.  `task_events`: append-only lifecycle/audit events.
7.  `outbox_events`: transactional events waiting to be published or
    processed.

Initial task statuses:

-   `pending`
-   `running`
-   `success`
-   `failed`
-   `cancelled`

Expected transitions:

-   `pending -> running` when claimed.
-   `pending -> cancelled` when cancelled before execution.
-   `running -> success` on accepted successful completion.
-   `running -> pending` when a retry is allowed after failure or lease
    expiry.
-   `running -> failed` when the error is non-retryable or retries are
    exhausted.
-   `running -> cancelled` after cancellation is requested and execution
    has actually stopped or has been reconciled.

Reject invalid transitions. A stale Agent callback must never overwrite
a newer attempt's result. Define `max_retries` as retries after the
initial attempt; maximum attempts therefore equal `1 + max_retries`.

## 7. Claim, lease, and fencing protocol

Use PostgreSQL transactions for claiming and state changes.

1.  Agent authenticates and reports its identity, supported task
    types/capabilities, and available slots.
2.  Scheduler selects eligible pending tasks, respecting `next_run_at`,
    dependencies, and concurrency policy.
3.  Claim rows with `FOR UPDATE SKIP LOCKED` so multiple Scheduler
    instances can coordinate safely if introduced later.
4.  In the same transaction, increment attempt and `lease_epoch`, assign
    the Agent, create the lease and attempt rows, record an event, and
    create any outbox event.
5.  Commit before returning the assignment to the Agent.
6.  Agent renews its lease periodically. Every renewal must match task
    ID, Agent ID, attempt, and `lease_epoch`.
7.  A normal heartbeat must not revive an already expired lease. A
    reaper invalidates the old assignment and either requeues the task
    or marks it failed according to retry policy.
8.  Each new assignment receives a higher `lease_epoch` fencing token.
9.  Completion callbacks must include task ID, Agent ID, attempt, and
    `lease_epoch`. Scheduler validates all of them and applies callback
    state changes transactionally.
10. Duplicate callbacks should be idempotent.

Suggested starting values: heartbeat every 15 seconds, lease TTL 60
seconds, lease reaper every 5 seconds. Make these configurable and test
behavior under delays and clock/network problems.

A fencing token protects Scheduler's state, but does not magically
prevent an old process from modifying external files or systems.
Executors must use task/epoch-specific temporary paths, idempotency
keys, checksums, atomic publication, or external locking where
appropriate.

## 8. Long-running task reliability

Do not apply one short global timeout to the whole task. Separate task
lifetime from request timeouts, lease renewals, and per-operation
timeouts.

For long tasks:

-   Persist progress and resumable checkpoints.
-   Store callback results in the Agent's SQLite outbox before sending
    them.
-   Retry unsent callbacks with backoff and mark them delivered only
    after Scheduler acknowledges them.
-   On Agent restart, reconcile local unfinished work with Scheduler
    before resuming execution.
-   Never let local SQLite state override authoritative Scheduler state.
-   On lease loss, stop or quarantine the old execution and prevent it
    from publishing stale output.
-   Use exponential backoff with jitter for retryable errors.
-   Distinguish retryable and non-retryable errors.
-   Ensure cancellation propagates to child processes and executors
    where possible.
-   Do not claim exactly-once execution; design for at-least-once
    delivery and idempotent effects.

For downloads:

-   Use HTTP Range only when the server confirms range support.
-   Start with a configurable chunk size, for example 64 MiB.
-   Persist completed chunks and resource identity such as ETag,
    Last-Modified, size, revision, or checksum when available.
-   Validate `Content-Range`, final size, and checksum where possible.
-   Download into task/epoch-specific temporary paths and publish
    atomically only after validation.
-   Check disk space before starting and account for concurrent tasks
    where feasible.
-   Redact credentials and signed URLs from logs.

## 9. Executor interface

Use a small, explicit interface similar to:

``` go
type Executor interface {
    Type() string
    Validate(task *Task) error
    Execute(ctx context.Context, task *Task) (*Result, error)
}
```

Adapt the exact types to the domain model. Executors must:

-   Validate payloads before expensive work begins.
-   Honor context cancellation where practical.
-   Return structured errors with retryability information where
    appropriate.
-   Emit structured progress instead of writing unbounded output to
    memory.
-   Avoid unsafe shell interpolation; use process arguments directly.
-   Validate filesystem paths and restrict access to configured work
    directories.
-   Avoid logging credentials, tokens, or sensitive task payload fields.

Do not implement every planned Executor before the core task lifecycle
is reliable. Add download first, then rclone/log/AI according to actual
requirements.

## 10. API boundaries

Initial API candidates:

-   `POST /tasks`
-   `GET /tasks/{id}`
-   `GET /tasks`
-   `POST /tasks/{id}/cancel`
-   `POST /agent/register`
-   `POST /agent/tasks/claim`
-   `POST /agent/heartbeat`
-   `POST /callback`
-   `GET /healthz`
-   `GET /readyz`
-   Metrics endpoint, such as `/metrics`

Use explicit request/response types, payload versions, input validation,
request IDs, authentication for Agent endpoints, and consistent error
responses. Do not expose internal database models directly as public API
contracts.

## 11. PostgreSQL, Redis, and outbox behavior

-   PostgreSQL transactions govern task correctness.
-   Use migrations for schema changes; do not rely on manual production
    schema edits.
-   Use a transactional outbox whenever a database state change must
    trigger Redis notification or another asynchronous action.
-   If Redis is unavailable, tasks must remain durable and discoverable
    from PostgreSQL. Redis failure may reduce responsiveness, but must
    not corrupt task state.
-   Use bounded database connection pools and explicit query timeouts.
-   Add indexes for claim eligibility, task status, Agent heartbeat,
    lease expiry, and idempotency keys based on actual query patterns.
-   Define backup and restore procedures before production use.

## 12. Observability and security

Use structured JSON logs with fields such as timestamp, level,
component, request ID, task ID, attempt, lease epoch, Agent ID, event,
and error code. Redact secrets.

Useful metrics include:

-   Task totals by type/status.
-   Task duration by type.
-   Pending queue length.
-   Active/busy Agent slots.
-   Lease expirations.
-   Claim failures.
-   Callback retry counts.
-   Agent heartbeat failures.
-   Executor failures and retry counts.

Avoid task IDs, user-provided strings, or other unbounded values as
metric labels.

Security requirements:

-   Authenticate Agents and authorize administrative operations.
-   Use TLS when traffic crosses untrusted networks.
-   Keep credentials outside source control and images.
-   Validate payload size, type, version, and fields.
-   Prevent path traversal and restrict filesystem operations.
-   Apply SSRF protections to user-supplied URLs.
-   Use least-privilege database credentials.
-   Never execute untrusted task strings through a shell.

## 13. Testing requirements

For meaningful changes, add tests appropriate to the behavior:

-   Unit tests for task state transitions, retry policy, validation, and
    lease rules.
-   PostgreSQL integration tests for claim concurrency, transaction
    boundaries, and stale callbacks.
-   Agent tests for restart reconciliation, callback outbox retries,
    cancellation, and lease loss.
-   Executor tests for checkpoint recovery, checksum failures, and
    partial downloads.
-   API tests for validation, authentication, idempotency, and error
    responses.
-   Fault-injection tests for database/Redis interruptions, lost HTTP
    responses, Agent restart, and expired leases.

Do not make tests depend on public network services unless the test is
explicitly an opt-in integration test.

## 14. Implementation phases

### Phase 1 --- Foundation

-   Initialize Go module and the three command entry points.
-   Add configuration loading, structured logging, graceful shutdown,
    and health endpoints.
-   Define domain types, status transitions, error model, and Executor
    interface.
-   Add PostgreSQL migrations and repositories.
-   Implement task submission, query, cancellation, and idempotency.
-   Add unit and repository integration tests.

### Phase 2 --- Reliable task delivery

-   Implement Agent registration and heartbeat.
-   Implement atomic claiming, attempts, leases, fencing tokens, and
    lease reaper.
-   Implement callback validation and idempotent processing.
-   Add transactional outbox and Redis notifications.
-   Add Agent SQLite checkpoint/outbox storage and restart
    reconciliation.

### Phase 3 --- Single-host Compose deployment

-   Build images for `kraken-scheduler` and `kraken-agent`.
-   Add Compose services for Scheduler, Agent, PostgreSQL, and Redis.
-   Configure persistent volumes, health checks, restart policies, and
    environment-based configuration.
-   Verify that tasks survive container restarts and temporary
    dependency outages.
-   Document how to build and run `krakenctl`.

### Phase 4 --- First real Executors

-   Implement a reliable download Executor with checkpoint/resume and
    atomic publication.
-   Add disk-space checks and safe handling of credentials.
-   Add rclone, log collection, and AI Executors incrementally.
-   Apply per-type concurrency limits.

### Phase 5 --- Production hardening and scale-out

-   Add dashboards, alerts, operational runbooks, backup/restore drills,
    and fault-injection tests.
-   Test multiple Agents competing for work.
-   Support Agents on separate machines through configurable Scheduler
    URL and authenticated identity.
-   Consider multiple Scheduler instances only after transaction, lease,
    and recovery behavior is well tested.
-   Add more advanced workflow features only when required.

## 15. Out of scope for the first release

Unless a concrete requirement appears, defer:

-   Splitting Scheduler into many microservices.
-   Exactly-once execution guarantees.
-   Complex conditional DAG workflows.
-   Multi-tenancy and a full permission-management system.
-   A Web UI.
-   Kubernetes deployment and DaemonSet packaging.
-   Premature optimization for large fleets before the single-host
    reliability path is proven.

## 16. Working rules for coding agents

When modifying this repository:

1.  Read this file and the relevant code before making changes.
2.  Preserve the naming convention: `kraken-orchestrator`,
    `kraken-scheduler`, `kraken-agent`, `krakenctl`.
3.  Prefer small, focused changes with tests.
4.  Do not introduce new infrastructure or dependencies without
    explaining why they are needed.
5.  Do not silently change public API contracts, task state semantics,
    database migrations, or configuration names.
6.  Never remove persistence, lease validation, callback idempotency, or
    recovery logic merely to simplify a test.
7.  Keep `cmd/*/main.go` thin; put business logic in `internal/`.
8.  Update README and configuration examples when commands, APIs,
    environment variables, or deployment behavior changes.
9.  Run relevant tests and report any tests that could not be run.
10. Never claim a task is durable, resumable, or exactly-once unless the
    implementation and tests actually support that claim.
11. All code must be written to production standards: handle every error
    explicitly (never ignore returned errors), set context timeouts on all
    I/O and database operations, use bounded queues and goroutine pools,
    redact secrets from logs and error messages, validate all external
    inputs, persist state before acknowledging side effects, and ensure
    graceful shutdown releases all resources. No "TODO for later"
    shortcuts in error handling, logging, or resource cleanup.