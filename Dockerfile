FROM --platform=$BUILDPLATFORM golang:1.26.8-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/runui/oci-mirror-proxy/internal/cli.version=$VERSION" \
    -o /out/oci-mirror-proxy ./cmd/oci-mirror-proxy

FROM build AS test
RUN go test -count=1 ./...

FROM scratch
ARG VERSION=dev
LABEL org.opencontainers.image.title="oci-mirror-proxy" \
      org.opencontainers.image.source="https://git.zmide.com/bin/oci-mirror-proxy" \
      org.opencontainers.image.version=$VERSION
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/oci-mirror-proxy /usr/local/bin/oci-mirror-proxy
ENV HOME=/tmp XDG_CONFIG_HOME=/etc XDG_RUNTIME_DIR=/run
USER 65532:65532
EXPOSE 24817
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/oci-mirror-proxy", "status"]
ENTRYPOINT ["/usr/local/bin/oci-mirror-proxy"]
CMD ["serve"]
