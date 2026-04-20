# syntax=docker/dockerfile:1

FROM alpine:3.23@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11 AS certs
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
