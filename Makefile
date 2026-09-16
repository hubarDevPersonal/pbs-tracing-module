IMAGE ?= pbs-tracer:local
PBS_DIR ?= $(HOME)/Dev/prebid-server

.PHONY: docker-build docker-run docker-e2e e2e test fmt

docker-build:            ## build PBS + module image (runs the module tests inside the build)
	docker build -t $(IMAGE) .

docker-run: docker-build ## run PBS on :8080; trace packets on stdout, PBS logs on stderr
	docker run --rm -p 8080:8080 -p 6060:6060 $(IMAGE)

docker-e2e:              ## build image, run container, send 02-send-bid-request.sh, assert the trace
	IMAGE=$(IMAGE) e2e/run-docker.sh

e2e:                     ## live e2e against a local PBS checkout in $(PBS_DIR)
	PBS_DIR=$(PBS_DIR) e2e/run.sh

test:                    ## run the module tests inside $(PBS_DIR)
	rm -rf $(PBS_DIR)/modules/test_provider && cp -R modules/test_provider $(PBS_DIR)/modules/ \
	&& cd $(PBS_DIR) && go generate ./modules/... && go vet ./modules/test_provider/... \
	&& go test -count=1 -race -cover ./modules/test_provider/...

fmt:
	gofmt -l -w modules
