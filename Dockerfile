FROM --platform=$BUILDPLATFORM golang:1.27@sha256:65b6f280bf050ec5af12716857e8ea8439d694dbba8f31ceeb7630670071f2bb AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /plugin ./main.go

# pg_doorman for in-place upgrade delivery: both architectures, because a
# pooler pod may run on a different arch than the plugin pod serving it.
FROM --platform=linux/amd64 ghcr.io/ozontech/pg_doorman:v3.11.0@sha256:b4c32c60e4267bbfe5ddd138c53a16e9f2e143fc64d7b3fae879b711d0db8578 AS doorman-amd64
FROM --platform=linux/arm64 ghcr.io/ozontech/pg_doorman:v3.11.0@sha256:b4c32c60e4267bbfe5ddd138c53a16e9f2e143fc64d7b3fae879b711d0db8578 AS doorman-arm64

FROM --platform=$BUILDPLATFORM golang:1.27@sha256:65b6f280bf050ec5af12716857e8ea8439d694dbba8f31ceeb7630670071f2bb AS binaries
# E2E_BINARY_MARKER appends bytes to produce a content-distinct binary of the
# same version: e2e exercises the delivery/upgrade machinery without needing
# a second upstream release with a compatible config schema.
ARG E2E_BINARY_MARKER=""
COPY --from=doorman-amd64 /usr/bin/pg_doorman /binaries/amd64/pg_doorman
COPY --from=doorman-arm64 /usr/bin/pg_doorman /binaries/arm64/pg_doorman
RUN if [ -n "$E2E_BINARY_MARKER" ]; then \
      printf '%s' "$E2E_BINARY_MARKER" >> /binaries/amd64/pg_doorman && \
      printf '%s' "$E2E_BINARY_MARKER" >> /binaries/arm64/pg_doorman; \
    fi

FROM gcr.io/distroless/static:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7
COPY --from=builder /plugin /plugin
COPY --from=binaries /binaries /binaries
USER 10001:10001
ENTRYPOINT ["/plugin"]
