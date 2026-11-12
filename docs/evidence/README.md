# Validation evidence

Recorded on 2026-11-11 in a managed Linux development workspace. All telemetry is synthetic. Test output and JSON artifacts are retained here so claims can be inspected.

| Check | Result | Artifact |
| --- | --- | --- |
| GitHub CI | All three jobs passed (Go, web, full Docker pipeline) | [Verified run](https://github.com/atbakhru/beacon/actions/runs/43123691025) |
| Go unit/integration tests, race detector | 10 tests passed, including concurrent writes, tenant-filtered vector search, correlation windows, and live WebSocket reconnect | [go-tests.txt](go-tests.txt) |
| Public GitHub Pages demo | HTTP 200 for HTML, JavaScript, and CSS; local browser journeys passed; remote Chromium trust-store limit documented | [public-demo.json](public-demo.json) |
| Browser demo | 3 Playwright journeys passed: lifecycle/search/1,000-alert burst/deduplication/persistence; URL filters; mobile layout | [browser-tests.txt](browser-tests.txt) |
| Real backend browser journey | Real API, WebSocket, burst count, and related vector results | [live-browser.json](live-browser.json) |
| Full Kafka pipeline, lexical vector baseline | Passed | [pipeline-smoke.json](pipeline-smoke.json) |
| Full Kafka pipeline, learned MiniLM vectors | Passed | [semantic-smoke.json](semantic-smoke.json) |
| Deterministic seed | 1,200 alerts; 20 incidents; 5 active, 15 resolved; zero pending | [seed.json](seed.json) |
| Terraform | `init -backend=false` and `validate` passed; no plan/apply | [terraform-validation.txt](terraform-validation.txt) |
| TypeScript + Vite production build | Passed | [web-build.txt](web-build.txt) |

## HTTP measurements

[Raw benchmark](http-benchmark.json): 300 warm `GET /v1/incidents?limit=50` requests at each thread-pool setting, approximately 21 incidents/1,201 alerts in PostgreSQL. Python client and Go API share the same Linux host; database, Redis and broker run in Docker. Worker activity and shared host scheduling can affect results. These are short read bursts, not sustained production capacity tests or semantic-search measurements.

| Maximum client workers | p50 | p95 | Errors |
| --- | --- | --- | --- |
| 10 | 5.56 ms | 12.78 ms | 0 / 300 |
| 50 | 9.84 ms | 35.32 ms | 0 / 300 |
| 100 | 6.13 ms | 14.26 ms | 0 / 300 |

Non-monotonic latency is expected in such a short shared-host run. Use `make benchmark` to repeat; do not extrapolate these numbers to larger data or internet traffic.

## Semantic sanity check

[MiniLM paraphrase check](semantic-evaluation.json) uses four hand-written queries against four candidate titles. Three of four retrieve the intended title first. The payment paraphrase instead retrieves notification delay: this shows why similarity is assistance rather than proof of root cause. This tiny fixture is not a representative retrieval benchmark. The learned model returned 384-dimensional normalized vectors and both indexing and queries used `sentence-transformers/all-MiniLM-L6-v2`.

## Environment and verification limits

- Go 1.27.4; Node 24.22.1; Python 3.12.14; PostgreSQL 16 + pgvector; Redis 7.4; Redpanda 25.2.2; system Chromium.
- The managed host uses Docker's space-intensive `vfs` driver. For local validation, the PostgreSQL image was flattened without changing its filesystem; Go and Python ran as host processes against the real containerized dependencies. The checked-in Compose deployment uses the standard upstream image and container builds. [GitHub CI run 43123691025](https://github.com/atbakhru/beacon/actions/runs/43123691025) passed the exact container build, seed, pipeline smoke, and live WebSocket test, alongside the Go and browser jobs.
- AWS credentials were not configured. Terraform validation checks provider schemas and configuration, not resource availability, costs, successful provisioning, or production behavior.
- No six-service deployment, production SLO, sub-10ms semantic search, or exhaustive recovery/backpressure coverage is claimed.

## Screenshots

- [Overview](dashboard.png)
- [Incident detail](incident-detail.png)
- [Mobile overview](mobile.png)
- [Real backend and semantic results](live-backend.png)
