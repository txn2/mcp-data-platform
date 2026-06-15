# syntax=docker/dockerfile:1

FROM alpine:3.24@sha256:a2d49ea686c2adfe3c992e47dc3b5e7fa6e6b5055609400dc2acaeb241c829f4 AS certs
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
