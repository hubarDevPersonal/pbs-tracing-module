IMAGE   ?= pbs-tracer:local
PBS_DIR ?= $(HOME)/Dev/prebid-server

.PHONY: help build test bench lint fmt vet tidy docker-build docker-run docker-e2e e2e perf profile install-module clean

help:                    ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'

build:                   ## build the CLIs (tracecheck, loadgen) into bin/
	go build -o bin/tracecheck ./cmd/tracecheck && go build -o bin/loadgen ./cmd/loadgen

test:                    ## unit + integration tests of this repository (PBS is a pinned dependency)
	go test ./... -race -count=1

bench:                   ## module hook benchmarks (ns/op, allocs per traced and untraced auction)
	go test ./internal/testtracer -run '^$$' -bench . -benchmem -count=1

cover:                   ## coverage report for the module package
	go test ./internal/testtracer -count=1 -coverprofile=bin/cover.out && go tool cover -func=bin/cover.out | tail -1

lint:                    ## golangci-lint (config: .golangci.yml)
	golangci-lint run ./...

fmt:                     ## gofumpt + golines via golangci-lint
	golangci-lint fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

install-module: ## copy the module into $(PBS_DIR)/modules/test_provider/test_tracer and regenerate builder.go
	PBS_DIR=$(PBS_DIR) scripts/install-module.sh

docker-build:            ## build PBS @ pinned commit + module image (module tests run inside the build)
	docker build -t $(IMAGE) .

docker-run: docker-build ## run PBS on :8080; trace packets on stdout, PBS logs on stderr
	docker run --rm -p 8080:8080 -p 6060:6060 $(IMAGE)

docker-e2e:              ## build image, run container, fire 02-send-bid-request.sh, verify the trace
	IMAGE=$(IMAGE) scripts/e2e-docker.sh

e2e:                     ## live e2e against a local PBS checkout in $(PBS_DIR)
	PBS_DIR=$(PBS_DIR) scripts/e2e-live.sh

perf:                    ## perf profile (tuned config + DNS sidecar): load run, pprof, metrics deltas → perf-report.md
	scripts/perf-docker.sh

profile:                 ## 30 s CPU profile from a running PBS admin port (:6060)
	scripts/profile.sh 30

clean:
	rm -rf bin
