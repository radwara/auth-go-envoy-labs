# Go Authorization and Envoy Lab

An interview lab with a Go policy control plane, a two-replica gRPC decision plane, and a concurrent Go job service. Envoy handles HTTP routing, gRPC load balancing, active health checks, bounded retries, circuit breakers, access logs, and Prometheus-format metrics. PostgreSQL stores authorization policies.

- [Architecture, diagrams, and design decisions](docs/architecture.md)
- [TLS and mTLS exercise](docs/tls-lab.md)
- [Issue-to-review workflow graph](docs/agent-workflow.md)
- [GitHub issues](https://github.com/radwara/auth-go-envoy-labs/issues)

## Run locally

Requires Docker Compose v2, `make`, `curl`, and `jq`.

```sh
make validate-envoy
make up
make smoke
make down
```

The smoke test creates a temporary policy, exercises CRUD, calls both decision replicas, runs an asynchronous job, then stops one replica and verifies the survivor serves requests. It starts the stopped replica again during cleanup. `make down` preserves the PostgreSQL volume.

## API

| Entry point | Behavior |
| --- | --- |
| `POST /policies` | Create a rule. Takes subject, action, resource, and effect (allow or deny). |
| `GET /policies?after=0` | List up to 100 policies in ID order. Use the last ID as the next cursor. |
| `GET /policies/{id}` | Read a rule. |
| `PUT /policies/{id}` | Replace a rule. |
| `DELETE /policies/{id}` | Delete a rule. |
| `POST /authorize` | Check subject, action, and resource and return the decision and replica. |
| `POST /jobs` | Queue the same check asynchronously and return a job ID. |
| `GET /jobs/{id}` | Read job status and result. |
| `authz.decision.v1.DecisionService/Check` | Direct gRPC check through Envoy on 9090. |

```sh
base=http://localhost:8080
curl -sS "$base/policies" -H 'Content-Type: application/json' \
  -d '{"subject":"alice","action":"read","resource":"document:7","effect":"allow"}'
curl -sS "$base/authorize" -H 'Content-Type: application/json' \
  -d '{"subject":"alice","action":"read","resource":"document:7"}'
curl -sS "$base/jobs" -H 'Content-Type: application/json' \
  -d '{"subject":"alice","action":"read","resource":"document:7"}'
```

Call the gRPC API with `grpcurl` and the checked-in descriptor source.

```sh
grpcurl -plaintext -import-path . -proto api/decision/v1/decision.proto \
  -d '{"subject":"alice","action":"read","resource":"document:7"}' \
  localhost:9090 authz.decision.v1.DecisionService/Check
```

## Inspect the proxy

```sh
docker compose logs envoy
curl -fsS http://localhost:9901/clusters
curl -fsS http://localhost:9901/stats/prometheus
```

Look for request counts for the decision cluster, healthy membership, and connection failures after the replica stops. Each successful authorization response names its serving replica.

## Develop

Run `make test` with Go 1.25 or later and a C toolchain for the race detector. Regenerate the checked-in protobuf stubs with `protoc`, `protoc-gen-go` and `protoc-gen-go-grpc` on PATH.

```sh
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  api/decision/v1/decision.proto
```
