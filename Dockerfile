# syntax=docker/dockerfile:1

FROM alpine:3.23@sha256:1882fa4569e0c591ea092d3766c4893e19b8901a8e649de7067188aba3cc0679

# Install ca-certificates for TLS connections
RUN apk add --no-cache ca-certificates

# Copy the binary from goreleaser (multi-arch build context)
ARG TARGETARCH
COPY linux/${TARGETARCH}/{{project-name}} /usr/local/bin/{{project-name}}

# Run as non-root user
RUN adduser -D -u 1000 mcp
USER mcp

ENTRYPOINT ["/usr/local/bin/{{project-name}}"]
