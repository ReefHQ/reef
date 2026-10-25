# Official multiarch images; versions can be adjusted for NAS engine compatibility.
ARG GO_IMAGE=golang:1.27-alpine
ARG DOCKER_IMAGE=docker:29.9.2-cli
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod VERSION ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /reef ./cmd/reef && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=$(cat VERSION)" -o /reef-agent ./cmd/reef-agent

# Inventory-only agent: no incoming port; persists only its private identity.
FROM ${DOCKER_IMAGE} AS agent
COPY --from=build /reef-agent /usr/local/bin/reef-agent
ENTRYPOINT ["/usr/local/bin/reef-agent"]

# This image includes the Docker client and Compose plugin; no daemon.
FROM ${DOCKER_IMAGE}
COPY --from=build /reef /usr/local/bin/reef
RUN docker --version && docker compose version
WORKDIR /app
EXPOSE 8099
ENTRYPOINT ["/usr/local/bin/reef"]
CMD ["-config", "/etc/reef/config.json"]
