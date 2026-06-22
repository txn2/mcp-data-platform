# syntax=docker/dockerfile:1

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS certs
RUN apk add --no-cache ca-certificates

FROM scratch

# TLS root certificates for HTTPS connections (OIDC, DataHub, Trino, S3)
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Copy the binary from goreleaser (multi-arch build context)
ARG TARGETARCH
COPY linux/${TARGETARCH}/mcp-data-platform /usr/local/bin/mcp-data-platform

# Run as non-root user (numeric UID — scratch has no adduser/passwd)
USER 1000:1000

ENTRYPOINT ["/usr/local/bin/mcp-data-platform"]
