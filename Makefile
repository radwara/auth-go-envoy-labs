.PHONY: test up down validate-envoy smoke
test:
	go test ./...
	go test -race ./...
	go vet ./...

up:
	docker compose up --build -d

down:
	docker compose down

validate-envoy:
	docker compose run --rm --no-deps envoy --mode validate -c /etc/envoy/envoy.yaml

smoke:
	bash scripts/smoke.sh

.PHONY: smoke-tls
smoke-tls:
	bash scripts/smoke-tls.sh
