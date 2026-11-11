# Architecture decisions

## 1. Start with one Go deployable

The guide's six services are responsibility boundaries: ingestion, correlation, incident lifecycle, search, streaming, and catalog. They share a Go process and PostgreSQL schema for this version. Kafka consumers and the outbox publisher run as cancellable background routines. Python remains a separate process because embedding libraries are native to that ecosystem. Split services only when independent scaling or ownership justifies distributed operational overhead.

## 2. PostgreSQL is authoritative

Ingestion inserts the validated alert and a Kafka outbox record in one transaction. A publisher locks pending rows with `FOR UPDATE SKIP LOCKED`, waits for broker acknowledgements, then marks them delivered. A crash after publish but before commit produces a duplicate, never a silently dropped committed alert. Consumers commit Kafka offsets after their work succeeds. Correlation locks the alert, then takes an advisory lock on the tenant/service/signature key. This prevents both double processing and the simultaneous creation of two incidents for one key.

The initial schema has nine implementation tables (organizations, services, alerts, incidents, incident_alerts, incident_events, incident_embeddings, outbox_events, rules). The blueprint's user and source tables are deferred because this release has service-account authentication and source event IDs. Index counts are not used as a quality metric.

## 3. Time, severity, lifecycle

A default 900-second correlation window can be replaced by an enabled service-specific or organization-wide rule. Event times may arrive out of order; grouping bounds both sides of the window and updates extrema. Critical beats high, medium, and low. Resolved incidents never absorb new alerts. Acknowledge only applies to open incidents, resolve to open/acknowledged incidents, and reopen to resolved incidents. Repeating the current state is idempotent. Source IDs are unique per tenant for the lifetime of retained alerts.

## 4. Streaming is a hint; state is durable

Redis pub/sub delivers invalidations. A browser reconnect always fetches fresh incidents, services, alerts, and metrics. This repairs gaps without treating Redis as a log. `/v1/events?cursor=N` and per-incident timelines provide ordered, durable history; their numeric cursor advances only through records returned by PostgreSQL. The WebSocket announces `resync` and has a 256-message channel. A full channel closes with code 1013; the browser backs off and reloads state. Large clients should paginate events until the cursor stops advancing. Browser snapshots currently load up to 500 incidents; this is a deliberate demo-scale bound.

## 5. Search and model integrity

The Python worker subscribes to incident events and also scans for missing, stale, or model-incompatible embeddings. Queries and indexing share the same encoder. Each stored vector carries a model name and incident version. The default feature-hash model uses SHA-256 signed unigram/bigram features; it is lexical similarity, not learned semantics. The optional FastEmbed MiniLM model produces learned 384-dimensional vectors.

Search filters by authenticated tenant and model version before returning candidates. Exact cosine ranking is appropriate for this fixture size and avoids filtered ANN recall pitfalls. An HNSW index exists for future measured scale work. Similarity scores are cosine similarities, not calibrated confidence or causal explanations. The UI labels browser matching separately.

## 6. Authorization and deployment

Every business route resolves its tenant from server-configured API keys, or from the explicit synthetic single-tenant demo mode. Looking up a foreign record yields 404. CORS and WebSocket origins use an exact allowlist. Production browsers need session or short-lived ticket authentication before demo mode can be disabled for the existing UI; API clients can send bearer headers. OIDC is not implemented. The static public demo has no credentials or backend connection.

## Known limits

- One migration is implemented; migration history/upgrade orchestration is needed when adding future migrations.
- Outbox delivery and Kafka consumption retry transient failures; no automatic DLQ/quarantine or bounded poison-event retry policy yet.
- Kafka topics use three partitions and replication factor one for local development. AWS provisioning is optional and not applied.
- No archival retention policy, user-level RBAC, production rate limiting, or cloud operational SLO is claimed.
- Local Compose uses Redis only for fan-out. Search caching is intentionally omitted to avoid stale data.
- Exactly-once delivery is not claimed; correctness relies on transactional state and idempotent processing.
