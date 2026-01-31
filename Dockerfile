# syntax=docker/dockerfile:1

FROM alpine:3.23@sha256:865b95f46d98cf867a156fe4a135ad3fe50d2056aa3f25ed31662dff6da4eb62

# Install ca-certificates for TLS connections
RUN apk add --no-cache ca-certificates

# Copy the binary from goreleaser (multi-arch build context)
ARG TARGETARCH
COPY linux/${TARGETARCH}/mcp-data-platform /usr/local/bin/mcp-data-platform

# Copy bundled MCP Apps (default apps shipped with the image)
# Users can override by mounting their own apps to /etc/mcp-apps/
COPY apps/ /usr/share/mcp-data-platform/apps/

# Run as non-root user
RUN adduser -D -u 1000 mcp
USER mcp

ENTRYPOINT ["/usr/local/bin/mcp-data-platform"]
