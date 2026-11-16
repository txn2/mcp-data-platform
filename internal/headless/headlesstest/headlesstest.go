//go:build integration

// Package headlesstest starts the renderer the platform documents, pinned by
// digest, for an integration test that draws a page in a real headless
// Chrome: one copy of the pin and the start, which the tile tests share.
package headlesstest

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Image is the renderer the platform documents, pinned by digest.
const Image = "chromedp/headless-shell@sha256:2d349b544a1ea6b5b5fd7c0fe99215ff662339c57407ee2e8c0a11af93516b04"

// Start runs the renderer for the test and returns its DevTools address.
func Start(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        Image,
			ExposedPorts: []string{"9222/tcp"},
			WaitingFor:   wait.ForHTTP("/json/version").WithPort("9222/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting the renderer: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	endpoint, err := c.PortEndpoint(ctx, "9222/tcp", "http")
	if err != nil {
		t.Fatalf("renderer endpoint: %v", err)
	}
	return endpoint
}
