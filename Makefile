IMAGE   ?= pbs-tracer:local
PBS_DIR ?= ../prebid-server
PBS_URL ?= http://localhost:8080
MODULE  := ./modules/test_provider/test_tracer

.PHONY: help test bench cover lint fmt vet tidy install-module docker-build docker-run e2e load load-bench highload perf profile clean

help:                    ## list targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'

test:                    ## module unit, race and in-process integration tests; unit tests of the e2e and load helpers
	go test ./... -race -count=1

bench:                   ## module overhead per traced and untraced auction (ns/op, allocs)
	go test $(MODULE) -run '^$$' -bench . -benchmem -count=1

cover:                   ## statement coverage of the module
	mkdir -p bin && go test $(MODULE) -count=1 -coverprofile=bin/cover.out && go tool cover -func=bin/cover.out | tail -1

lint:                    ## golangci-lint, including the e2e and load build tags
	golangci-lint run ./...

fmt:                     ## gofumpt + golines
	golangci-lint fmt ./...

vet:
	go vet -tags e2e,load ./... && go vet -tags loadbench ./modules/...

tidy:
	go mod tidy

install-module:          ## copy the module into $(PBS_DIR) and regenerate modules/builder.go
	PBS_DIR=$(PBS_DIR) scripts/install-module.sh

docker-build:            ## PBS @ pinned commit with the module compiled in (module tests run in the build)
	docker build -t $(IMAGE) .

docker-run: docker-build ## PBS on :8080; trace packets on stdout, PBS log on stderr
	docker run --rm -p 8080:8080 -p 127.0.0.1:6060:6060 $(IMAGE)

e2e:                     ## end-to-end against pbs-tracer:local and live bidders (builds the image first)
	docker build -t pbs-tracer:local .
	go test -tags e2e -count=1 -v ./test/e2e

load:                    ## load test against a running PBS at $(PBS_URL), live bidders
	go test -tags load -count=1 -v -timeout 0 -run '^TestLoad_' ./test/load -args -pbs-url $(PBS_URL)

load-bench:              ## bench matrix against stub bidders: hooks off/on, active tracing, 3 partners, large payload, stalled stdout
	docker build --build-arg GO_TAGS=loadbench -t pbs-tracer:loadbench .
	go test -tags load -count=1 -v -timeout 0 -run '^TestLoadBench$$' ./test/load $(BENCH_ARGS)

highload:                ## rate ladder to saturation + closed loop, hooks off / on / active tracing, CPU per auction; report → workspace/reports/highload.md
	docker build --build-arg GO_TAGS=loadbench -t pbs-tracer:loadbench .
	mkdir -p workspace/reports
	go test -tags load -count=1 -v -timeout 0 -run '^TestHighload$$' ./test/load -args -hl-report $(CURDIR)/workspace/reports/highload.md $(HL_ARGS)

perf:                    ## load test on the perf profile (tuned config, DNS cache) with CPU profile and metrics
	scripts/perf-docker.sh

profile:                 ## 30 s CPU profile from a running PBS admin port (:6060)
	scripts/profile.sh 30

clean:
	rm -rf bin
