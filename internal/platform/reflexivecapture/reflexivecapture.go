// Package reflexivecapture wires reflexive knowledge activation (#635) into the
// platform: it observes Trino query errors and mints a "misconception + fix"
// correction memory when a later related query succeeds in the same session.
// It lives in its own package so the platform facade stays within its size
// budget and the wiring is cohesive and independently testable.
package reflexivecapture

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

// entryTTL bounds how long a query failure stays eligible to pair with a later
// fix; sessionTimeout bounds how long an idle session's failure state is kept.
// Sized for interactive analyst sessions where an error and its correction land
// within a few minutes.
const (
	entryTTL          = 15 * time.Minute
	sessionTimeout    = 30 * time.Minute
	memoryCaptureTool = "memory_capture"
)

// Config is the reflexive-capture YAML config block.
type Config struct {
	Enabled *bool `yaml:"enabled"`
}

// IsEnabled reports whether reflexive capture is enabled, defaulting to true
// when not explicitly set.
func (c Config) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// PersonaToolCheck reports whether a caller with the given roles is authorized
// the named tool. Reflexive capture uses it to respect the memory_capture grant.
type PersonaToolCheck func(ctx context.Context, roles []string, tool string) bool

// Deps carries the platform primitives Wire needs, kept as plain values and
// closures so this package never imports the platform package (which would
// cycle).
type Deps struct {
	Enabled bool
	Server  *mcp.Server
	Toolkit *memorykit.Toolkit
	// BuildURN entity-keys a correction to the dataset it concerns. The
	// platform owns the one builder (connection -> platform and catalog
	// mapping -> URN); nil leaves corrections unkeyed.
	BuildURN middleware.URNBuilder
	// PersonaAllowsTool gates capture on the memory_capture grant. Nil means no
	// persona gating is configured (allow), matching the tools/list visibility
	// middleware when no authorizer is wired.
	PersonaAllowsTool PersonaToolCheck
}

// Wire registers the reflexive-capture middleware when enabled and the memory
// subsystem is available, returning the session error tracker so the caller can
// Stop it on shutdown (nil when not wired).
func Wire(d Deps) *middleware.SessionErrorTracker {
	if !d.Enabled || d.Toolkit == nil || d.Server == nil {
		return nil
	}

	tracker := middleware.NewSessionErrorTracker(entryTTL, sessionTimeout)
	tracker.StartCleanup(time.Minute)

	cfg := middleware.ReflexiveCaptureConfig{
		Captor:     &captor{toolkit: d.Toolkit},
		Tracker:    tracker,
		URNBuilder: d.BuildURN,
	}
	if d.PersonaAllowsTool != nil {
		cfg.CapturePermitted = func(ctx context.Context, pc *middleware.PlatformContext) bool {
			return d.PersonaAllowsTool(ctx, pc.Roles, memoryCaptureTool)
		}
	}
	d.Server.AddReceivingMiddleware(middleware.MCPReflexiveCaptureMiddleware(cfg))
	slog.Info("reflexive knowledge capture enabled (auto-captures query-error corrections into review)")
	return tracker
}

// captor adapts the memory toolkit's AutoCapture to the middleware's
// ReflexiveCaptor interface.
type captor struct {
	toolkit *memorykit.Toolkit
}

// CaptureCorrection persists a reflexive correction as an automation-sourced
// memory capture.
func (c *captor) CaptureCorrection(ctx context.Context, cc middleware.CorrectionCapture) error {
	_, err := c.toolkit.AutoCapture(ctx, memorykit.AutoCaptureInput{
		SinkClass:  cc.SinkClass,
		Content:    cc.Content,
		Category:   cc.Category,
		Source:     memory.SourceAutomation,
		EntityURNs: cc.EntityURNs,
		Metadata:   cc.Metadata,
		CreatedBy:  cc.CreatedBy,
		Persona:    cc.Persona,
		UserID:     cc.UserID,
		SessionID:  cc.SessionID,
	})
	if err != nil {
		return fmt.Errorf("reflexive auto-capture: %w", err)
	}
	return nil
}
