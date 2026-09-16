# syntax=docker/dockerfile:1
#
# Builds Prebid Server (pinned upstream commit) with the test_provider.test_tracer module compiled in,
# and runs it with the assessment's pbs.yaml. Trace packets go to the container's stdout, PBS logs to stderr:
#
#   docker build -t pbs-tracer .
#   docker run --rm -p 8080:8080 pbs-tracer 2>pbs.log | tee trace.ndjson
#
# Build args:
#   PBS_REPO  upstream repository (default github.com/prebid/prebid-server)
#   PBS_REF   commit/tag to build against (default: the commit the module was developed and tested on)
#   RUN_TESTS "true" runs the module's test suite during the build (default true)
ARG GO_IMAGE=golang:1.26-bookworm
ARG BASE_IMAGE=ubuntu:22.04

FROM ${GO_IMAGE} AS build
ARG PBS_REPO=https://github.com/prebid/prebid-server.git
ARG PBS_REF=f660bedc03ef1a51f6af8dcd0dd6ab61e1d4c417
ARG RUN_TESTS=true
ENV CGO_ENABLED=1 GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod

WORKDIR /src/prebid-server
RUN git init -q . \
 && git remote add origin "${PBS_REPO}" \
 && git fetch -q --depth 1 origin "${PBS_REF}" \
 && git checkout -q FETCH_HEAD

# warm the module cache before copying our code so dependency downloads are cached across edits
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY modules/test_provider ./modules/test_provider
RUN --mount=type=cache,target=/go/pkg/mod \
    go generate ./modules/... \
 && grep -q '"test_provider"' modules/builder.go \
 && gofmt -l ./modules/test_provider | { ! grep . ; } \
 && go vet ./modules/test_provider/... \
 && if [ "${RUN_TESTS}" = "true" ]; then go test -count=1 ./modules/test_provider/...; fi \
 && go build -ldflags "-X github.com/prebid/prebid-server/v4/version.Ver=assessment-${PBS_REF} -X github.com/prebid/prebid-server/v4/version.Rev=${PBS_REF}" -o /out/prebid-server .

FROM ${BASE_IMAGE} AS release
LABEL org.opencontainers.image.title="prebid-server + test_provider.test_tracer"
WORKDIR /usr/local/bin/
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates libatomic1 curl \
 && apt-get clean && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/prebid-server .
COPY --from=build /src/prebid-server/static static/
COPY --from=build /src/prebid-server/stored_requests/data stored_requests/data
# the assessment's configuration, unchanged; PBS reads ./pbs.yaml from its working directory
COPY pbs.yaml ./pbs.yaml
RUN chmod a+xr prebid-server && chmod -R a+r static/ stored_requests/data pbs.yaml \
 && addgroup --system --gid 2001 prebidgroup && adduser --system --uid 1001 --ingroup prebidgroup prebid
USER prebid
EXPOSE 8080 6060
HEALTHCHECK --interval=5s --timeout=2s --start-period=5s --retries=12 CMD curl -sf http://localhost:8080/status || exit 1
ENTRYPOINT ["/usr/local/bin/prebid-server"]
CMD ["-stderrthreshold=INFO"]
