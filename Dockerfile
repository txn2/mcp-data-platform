# syntax=docker/dockerfile:1

FROM alpine:3.23@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659 AS certs
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
