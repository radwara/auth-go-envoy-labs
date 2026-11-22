#!/usr/bin/env bash
set -euo pipefail
base="http://127.0.0.1:8080"
body=$(mktemp)
stopped=''
id=''
cleanup() {
  if [[ -n "$stopped" ]]; then docker compose start "$stopped" >/dev/null || true; fi
  if [[ -n "$id" ]]; then curl -fsS --max-time 5 -X DELETE "$base/policies/$id" >/dev/null || true; fi
  rm -f "$body"
}
trap cleanup EXIT

expect() {
  local expected=$1
  shift
  local actual
  actual=$(curl -sS --max-time 5 -o "$body" -w '%{http_code}' "$@")
  if [[ "$actual" != "$expected" ]]; then
    printf 'Expected %s, got %s\n' "$expected" "$actual" >&2
    cat "$body" >&2
    return 1
  fi
}

json() {
  jq -e "$@" "$body" >/dev/null
}

tries=0
until curl -fs --max-time 3 "$base/healthz" >/dev/null; do
  tries=$((tries + 1))
  if (( tries >= 40 )); then echo 'Control plane did not become ready' >&2; exit 1; fi
  sleep 1
done

input=$(jq -nc --arg subject "smoke-$(date +%s)-$$" '{subject:$subject,action:"read",resource:"document:7"}')
rule=$(jq -c '. + {effect:"allow"}' <<<"$input")
deny=$(jq -c '. + {effect:"deny"}' <<<"$input")
header='Content-Type: application/json'

expect 201 -H "$header" -d "$rule" "$base/policies"
id=$(jq -er '.id' "$body")
expect 409 -H "$header" -d "$rule" "$base/policies"
expect 200 "$base/policies/$id"
json '.effect == "allow"'
expect 200 "$base/policies?after=$((id - 1))"
# shellcheck disable=SC2016
json --argjson id "$id" 'any(.[]; .id == $id)'
expect 200 -X PUT -H "$header" -d "$deny" "$base/policies/$id"
json '.effect == "deny"'

replicas=''
for _ in {1..16}; do
  if expect 200 -H "$header" -d "$input" "$base/authorize"; then
    json '.allowed == false and .reason == "explicit_deny"'
    replicas="$replicas $(jq -er '.replica' "$body")"
    if [[ "$replicas" == *decision-a* && "$replicas" == *decision-b* ]]; then break; fi
  fi
  sleep 1
done
[[ "$replicas" == *decision-a* && "$replicas" == *decision-b* ]]
echo "Observed both decision replicas"

expect 200 -X PUT -H "$header" -d "$rule" "$base/policies/$id"
expect 200 -H "$header" -d "$input" "$base/authorize"
json '.allowed == true and .reason == "explicit_allow"'
expect 202 -H "$header" -d "$input" "$base/jobs"
job=$(jq -er '.id' "$body")
for _ in {1..20}; do
  expect 200 "$base/jobs/$job"
  if [[ $(jq -r '.status' "$body") == succeeded ]]; then break; fi
  sleep 0.25
done
json '.status == "succeeded" and .result.allowed == true'

stopped=decision-a
docker compose stop "$stopped"
sleep 5
for _ in {1..4}; do
  expect 200 -H "$header" -d "$input" "$base/authorize"
  json '.allowed == true and .replica == "decision-b"'
done
curl -fsS --max-time 5 http://127.0.0.1:9901/stats/prometheus >"$body"
grep -q '^envoy_cluster_upstream_rq_total.*decision' "$body"

expect 204 -X DELETE "$base/policies/$id"
expect 404 "$base/policies/$id"
expect 200 -H "$header" -d "$input" "$base/authorize"
json '.allowed == false and .reason == "no_matching_policy"'
id=''
echo 'Passed policy CRUD, gRPC, jobs, replica failover, and proxy metrics checks'
