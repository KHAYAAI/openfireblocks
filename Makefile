# OpenFireblocks developer ergonomics. Run `make help` for targets.
.DEFAULT_GOAL := help
GO_SERVICES := services/mpc-signer services/policy-service services/temporal-worker sdks/sdk-go

.PHONY: help
help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: build-go build-node ## Build everything

.PHONY: build-go
build-go: ## Build all Go modules
	@for m in $(GO_SERVICES); do echo "== $$m =="; (cd $$m && go build ./...) || exit 1; done

.PHONY: build-node
build-node: ## Build gateway + JS SDK
	cd services/api-gateway && npm ci && npm run build
	cd sdks/sdk-js && npm ci && npm run build

.PHONY: test
test: test-go test-node test-python ## Run all tests

.PHONY: test-go
test-go: ## Test all Go modules (short)
	@for m in $(GO_SERVICES); do echo "== $$m =="; (cd $$m && go vet ./... && go test -short ./...) || exit 1; done

.PHONY: test-tss
test-tss: ## Run the full threshold-MPC proof (~75s)
	cd services/mpc-signer && go test -tags tss -vet=off ./tss/ -run TestThresholdKeygenAndSign -v

.PHONY: test-node
test-node: ## Test gateway + JS SDK
	cd services/api-gateway && npm test
	cd sdks/sdk-js && npm run build && npm test

.PHONY: test-python
test-python: ## Test Python SDK
	cd sdks/sdk-python && python3 -m unittest discover -s tests

.PHONY: verify
verify: test-go test-node test-python ## Full verification (what CI runs)
	@echo "All modules verified."

.PHONY: up
up: ## Start the local stack
	cd infrastructure && docker compose up -d --build

.PHONY: down
down: ## Stop the local stack
	cd infrastructure && docker compose down

.PHONY: smoke
smoke: ## Run the e2e smoke test against a running stack
	./tests/smoke/smoke.sh
