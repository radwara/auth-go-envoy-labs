# Architecture

## High-level view

```mermaid
flowchart LR
    Client["REST or gRPC client"] --> Gateway["Envoy"]
    Gateway -->|REST policies and authorize| Control["Go control plane"]
    Gateway -->|REST jobs| Jobs["Go job service"]
    Control -->|SQL writes and reads| DB[("PostgreSQL policies")]
    Control -->|gRPC Check over HTTP/2| Gateway
    Jobs -->|gRPC Check over HTTP/2| Gateway
    Gateway -->|round robin| A["Go decision replica A"]
    Gateway -->|round robin| B["Go decision replica B"]
    A -->|SQL lookup| DB
    B -->|SQL lookup| DB
```

Envoy exposes three loopback ports on the host. The REST listener on 8080 routes policy and job traffic. The gRPC listener on 9090 routes the typed `Check` call to two identical decision replicas. The admin listener on 9901 exposes cluster and Prometheus-format stats for the lab.

## Low-level request sequence

```mermaid
sequenceDiagram
    participant C as Client
    participant E as Envoy
    participant P as Control
    participant D as Decision A or B
    participant S as PostgreSQL
    C->>E: POST /policies
    E->>P: HTTP route
    P->>S: INSERT subject, action, resource, effect
    S-->>P: Committed rule with ID
    P-->>C: 201 via Envoy
    C->>E: POST /authorize
    E->>P: HTTP route
    P->>E: gRPC Check with 2 s deadline
    E->>D: HTTP/2 using healthy endpoints
    D->>S: Indexed exact-match SELECT with 750 ms deadline
    alt Matching allow or deny rule
        S-->>D: Effect
        D-->>P: Explicit result via Envoy
    else No matching rule
        S-->>D: No rows
        D-->>P: Deny via Envoy
    else Database error
        S-->>D: Error
        D-->>P: gRPC Unavailable via Envoy
    end
    P-->>C: 200 with decision or 503 via Envoy
```

```mermaid
flowchart LR
    Submit["POST /jobs"] --> Lock["Validate, lock, check capacity"]
    Lock -->|queue full| Reject["503 + Retry-After"]
    Lock -->|accepted| Queue["Buffered channel of 16"]
    Lock -->|accepted| Poll["202 + job ID"]
    Queue --> Worker["One of four goroutines"]
    Worker --> Running["Write running under mutex"]
    Running --> Call["Release mutex and call gRPC"]
    Call --> Done["Write succeeded or failed under mutex"]
    Done --> History["Retain up to 100 jobs total"]
    Get["GET /jobs/{id}"] --> History
```

The submitter holds the mutex through admission and insertion into the status map. A worker that receives the job can only publish `running` after that insertion. Workers hold the mutex for state transitions and release it before network I/O. When the history limit is reached, the next accepted job evicts the oldest completed job. Shutdown stops HTTP admission, closes the channel under the same mutex, and waits for workers to drain. A denied decision still represents a successful job because gRPC returned a decision.

## Design decisions

| Decision | Reason and tradeoff |
| --- | --- |
| Exact-match rules, with one row per subject/action/resource | The SQL uniqueness constraint gives the same lookup semantics across replicas and reports conflicts clearly. Wildcards, groups, and policy precedence would change the data model. |
| Default deny with explicit dependency errors | An absent row yields a normal deny. Store outages yield gRPC `Unavailable`, REST `503`, or a failed job so clients can distinguish dependency failure from a policy result. |
| Shared PostgreSQL with per-process connection pools | A committed change is visible on the next decision query. This keeps the learning lab focused on request flow. It couples both planes to database availability and adds a DB round trip to every check. The lab uses the same development DB identity for both planes. |
| Read-only gRPC retry | The gRPC route permits one retry on configured transport failures or an eligible gRPC status. The 850 ms attempt timeout and 1.8 s route timeout fit inside the 2 s client deadline. Envoy retries gRPC statuses in response headers. REST writes and job submission have no configured retry. |
| Active probes and bounded requests | Envoy uses HTTP probes for control and jobs and gRPC health for each decision replica. The decision probe reports process readiness after startup, and individual checks can still fail if PostgreSQL goes down. The decision cluster disables panic routing and bounds connections, pending requests, in-flight requests, and concurrent retries. |
| Bounded process-local job state | Four workers limit concurrency, a 16-entry queue absorbs short bursts, and a 100-job history caps retained state. Job records are transient. Restart clears the queue and job IDs. |
| Local plaintext | Compose uses loopback publishing, local database credentials, and plaintext HTTP/2. The caller supplies the subject for the learning exercise. Client authentication, TLS/mTLS, and independent database roles are production concerns. TLS/mTLS is tracked in [issue #6](https://github.com/radwara/auth-go-envoy-labs/issues/6). |

See the [Envoy router documentation](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/router_filter) for retry conditions and response-header behavior.
