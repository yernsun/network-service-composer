GO ?= go
NPM ?= npm
PROTOC ?= protoc
VERSION ?= $(shell sed -n '1p' VERSION)
LDFLAGS := -s -w -X main.version=v$(VERSION)

.PHONY: all build test test-race test-fuzz test-store test-e2e test-e2e-compose test-e2e-management test-e2e-ui test-e2e-acme test-e2e-ha test-e2e-swarm bench-large lint vuln tidy generate verify-generated docker-build docker-build-controller docker-build-gateway docker-build-verification-toolchain compose-config run-controller run-agent

all: test build

build: frontend-build
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/nsc-controller ./cmd/nsc-controller
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/nsc-agent ./cmd/nsc-agent

test: frontend-build
	$(GO) test ./...

test-race: frontend-build
	$(GO) test -race -count=1 -timeout=30m ./...

.PHONY: test-coverage
test-coverage: frontend-build
	GO="$(GO)" NPM="$(NPM)" bash ./scripts/check-coverage.sh

.PHONY: test-coverage-backend
test-coverage-backend: frontend-build
	GO="$(GO)" NODE="$(NODE)" bash ./scripts/check-coverage.sh

test-fuzz:
	GOMAXPROCS=2 $(GO) test -run '^$$' -fuzz '^FuzzNormalizeHost$$' -fuzztime=5s ./internal/core
	GOMAXPROCS=2 $(GO) test -run '^$$' -fuzz '^FuzzNormalizePath$$' -fuzztime=5s ./internal/core
	GOMAXPROCS=2 $(GO) test -run '^$$' -fuzz '^FuzzParseLabels$$' -fuzztime=5s ./internal/runtime
	GOMAXPROCS=2 $(GO) test -run '^$$' -fuzz '^FuzzDecodeSnapshot$$' -fuzztime=5s ./internal/gateway
	GOMAXPROCS=2 $(GO) test -run '^$$' -fuzz '^FuzzNGINXPathValidation$$' -fuzztime=5s ./internal/nginx

lint: frontend-build
	$(GO) vet ./...

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

test-store:
	$(GO) test -count=1 -run TestExternalStoreConformance ./internal/storage

test-e2e: build docker-build
	./scripts/e2e-compose.sh
	./scripts/e2e-management.sh
	./scripts/e2e-ui.sh
	./scripts/e2e-acme.sh
	./scripts/e2e-ha.sh postgres
	./scripts/e2e-ha.sh mysql
	./scripts/e2e-swarm.sh

test-e2e-compose: build docker-build
	./scripts/e2e-compose.sh

test-e2e-management: build docker-build
	./scripts/e2e-management.sh

.PHONY: test-cluster-swarm
test-cluster-swarm:
	./scripts/test-cluster-swarm.sh

.PHONY: test-gateway-swarm-rolling
test-gateway-swarm-rolling:
	./scripts/test-gateway-swarm-rolling.sh

.PHONY: test-swarm-rollout test-swarm-rollout-unit
test-swarm-rollout-unit:
	python3 -m unittest discover -s scripts -p 'test_swarm_rollout*.py' -v

test-swarm-rollout:
	python3 scripts/swarm_rollout_acceptance.py --controller-image "$(NSC_CONTROLLER_IMAGE)" --gateway-image "$(NSC_GATEWAY_IMAGE)" --output coverage/swarm-rollout/current/summary.json

test-e2e-ui: build docker-build
	./scripts/e2e-ui.sh

test-e2e-acme: build docker-build
	./scripts/e2e-acme.sh

test-e2e-ha: build docker-build-controller
	./scripts/e2e-ha.sh postgres
	./scripts/e2e-ha.sh mysql

test-e2e-swarm: build docker-build
	./scripts/e2e-swarm.sh

bench-large:
	$(GO) test -run '^$$' -bench '^BenchmarkEncodeLargeSnapshot$$' -benchtime=3x ./internal/gateway
	$(GO) test -run '^$$' -bench '^BenchmarkApplyLargeRuntimeSignal$$' -benchtime=10x ./internal/storage

generate:
	@protoc_bin=$$(command -v "$(PROTOC)") || { echo "protoc not found: $(PROTOC)" >&2; exit 1; }; \
	protoc_dir=$$(cd "$$(dirname "$$protoc_bin")" && pwd) || exit 1; \
	protoc_bin="$$protoc_dir/$$(basename "$$protoc_bin")"; \
	cd api/proto && PATH="$$protoc_dir:$$PATH:$$($(GO) env GOPATH)/bin" "$$protoc_bin" -I . --go_out=../gen --go_opt=paths=source_relative --go-grpc_out=../gen --go-grpc_opt=paths=source_relative nsc/gateway/v2/gateway.proto

verify-generated:
	@root=$$(pwd); tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	protoc_bin=$$(command -v "$(PROTOC)") || { echo "protoc not found: $(PROTOC)" >&2; exit 1; }; \
	protoc_dir=$$(cd "$$(dirname "$$protoc_bin")" && pwd) || exit 1; \
	protoc_bin="$$protoc_dir/$$(basename "$$protoc_bin")"; \
	(cd "$$root/api/proto" && PATH="$$protoc_dir:$$PATH:$$($(GO) env GOPATH)/bin" "$$protoc_bin" -I . --go_out="$$tmp" --go_opt=paths=source_relative --go-grpc_out="$$tmp" --go-grpc_opt=paths=source_relative nsc/gateway/v2/gateway.proto) || exit 1; \
	diff -ru "$$root/api/gen" "$$tmp"

docker-build: docker-build-controller docker-build-gateway

docker-build-controller:
	@source_revision=$$(git rev-parse HEAD) && source_fingerprint=$$($(NODE) scripts/readiness/gate.mjs fingerprint) && \
	docker build --build-arg SOURCE_REVISION="$$source_revision" --build-arg SOURCE_FINGERPRINT="$$source_fingerprint" --build-arg VERSION=v$(VERSION) -f build/Dockerfile.controller -t nsc-controller:v$(VERSION) .

docker-build-gateway:
	@source_revision=$$(git rev-parse HEAD) && source_fingerprint=$$($(NODE) scripts/readiness/gate.mjs fingerprint) && \
	docker build --build-arg SOURCE_REVISION="$$source_revision" --build-arg SOURCE_FINGERPRINT="$$source_fingerprint" --build-arg VERSION=v$(VERSION) -f build/Dockerfile.gateway -t nsc-gateway:v$(VERSION) .

VERIFY_TOOLCHAIN_IMAGE ?= nsc-verification-toolchain:go1.27.3-protoc33
docker-build-verification-toolchain:
	docker build -f build/Dockerfile.verification -t $(VERIFY_TOOLCHAIN_IMAGE) .

compose-config:
	docker compose config --quiet

tidy:
	$(GO) mod tidy

run-controller: frontend-build
	$(GO) run ./cmd/nsc-controller serve --config ./config/controller.yaml

run-agent:
	$(GO) run ./cmd/nsc-agent --config ./config/agent.yaml

.PHONY: frontend-install frontend-build frontend-check frontend-dev
frontend-install:
	$(NPM) ci --prefix frontend

frontend-build: frontend-install
	$(NPM) run build --prefix frontend

frontend-check: frontend-install
	$(NPM) run lint --prefix frontend
	$(NPM) run typecheck --prefix frontend
	$(NPM) run test:coverage --prefix frontend
	$(NPM) run build --prefix frontend

frontend-dev:
	$(NPM) run dev --prefix frontend

.PHONY: test-e2e-delivery test-e2e-nginx
test-e2e-delivery: build docker-build
	NSC_CONTROLLER_IMAGE=nsc-controller:v$(VERSION) NSC_GATEWAY_IMAGE=nsc-gateway:v$(VERSION) NSC_DELIVERY_PRIVATE=true python3 scripts/e2e-delivery.py
	NSC_DELIVERY_SWARM=true NSC_CONTROLLER_IMAGE=nsc-controller:v$(VERSION) NSC_GATEWAY_IMAGE=nsc-gateway:v$(VERSION) python3 scripts/e2e-delivery.py

test-e2e-nginx: build docker-build
	NSC_E2E_NGINX=true ./scripts/e2e-compose.sh

# Shared local/CI verification; prerequisites are installed explicitly.
NODE ?= node
.PHONY: privacy-check install-git-hooks
privacy-check:
	$(NODE) scripts/check-repository-privacy.mjs --worktree
	$(NODE) --test scripts/check-repository-privacy.test.mjs

install-git-hooks:
	sh scripts/install-git-hooks.sh

.PHONY: harness-check harness
harness-check:
	GO="$(GO)" NPM="$(NPM)" NODE="$(NODE)" PROTOC="$(PROTOC)" $(NODE) scripts/harness/run.mjs check

harness:
	GO="$(GO)" NPM="$(NPM)" NODE="$(NODE)" PROTOC="$(PROTOC)" $(NODE) scripts/harness/run.mjs full

.PHONY: test-e2e-market-swarm
test-e2e-market-swarm: build docker-build
	NSC_CONTROLLER_IMAGE=nsc-controller:v$(VERSION) NSC_GATEWAY_IMAGE=nsc-gateway:v$(VERSION) python3 scripts/e2e-market-swarm.py

.PHONY: test-ha-alerts test-readiness-gate
test-ha-alerts:
	@image=$$($(NODE) -e 'console.log(JSON.parse(require("fs").readFileSync("build/image-lock.json")).images.prometheus_verification)'); \
	docker run --rm --network none --read-only --tmpfs /tmp:rw,nosuid,nodev,mode=1777 --entrypoint /bin/promtool \
	  --mount type=bind,source="$(CURDIR)/deploy",target=/rules,readonly \
	  "$$image" test rules /rules/ha-alerts.test.yaml

test-readiness-gate:
	$(NODE) --test scripts/readiness/*.test.mjs

FIXTURE_PROFILE ?= p0
.PHONY: prepare-fixture-images
prepare-fixture-images:
	python3 scripts/prepare-fixture-images.py --profile $(FIXTURE_PROFILE)
