# Official multiarch images; versions can be adjusted for NAS engine compatibility.
ARG GO_IMAGE=golang:1.27-alpine
ARG DOCKER_IMAGE=docker:29.9.2-cli
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG APP_REVISION=unknown
WORKDIR /src
COPY go.mod VERSION ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X reef/internal/buildinfo.Version=$(cat VERSION) -X reef/internal/buildinfo.Revision=${APP_REVISION}" -o /reef ./cmd/reef && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X reef/internal/buildinfo.Version=$(cat VERSION) -X reef/internal/buildinfo.Revision=${APP_REVISION}" -o /reef-agent ./cmd/reef-agent

# Inventory-only agent: no incoming port; persists only its private identity.
FROM ${DOCKER_IMAGE} AS agent
COPY --from=build /reef-agent /usr/local/bin/reef-agent
ENTRYPOINT ["/usr/local/bin/reef-agent"]

# This image includes the Docker client and Compose plugin; no daemon.
FROM ${DOCKER_IMAGE} AS runtime
ARG APP_REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/ReefHQ/reef" \
      org.opencontainers.image.revision="${APP_REVISION}"
COPY --from=build /reef /usr/local/bin/reef
WORKDIR /app
EXPOSE 8099
ENTRYPOINT ["/usr/local/bin/reef"]
CMD ["-config", "/etc/reef/config.json"]
