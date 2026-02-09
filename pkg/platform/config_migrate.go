package platform

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateConfig reads YAML from r, migrates it to targetVersion, and writes
// the result to w. If targetVersion is empty, the current version is used.
// Environment variable references (${VAR}) are preserved in the output.
func MigrateConfig(r io.Reader, w io.Writer, targetVersion string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}
	out, err := MigrateConfigBytes(data, targetVersion)
	if err != nil {
		return err //nolint:wrapcheck // MigrateConfigBytes already wraps errors
	}
	if _, err := w.Write(out); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

// MigrateConfigBytes migrates raw YAML config bytes to targetVersion.
// If targetVersion is empty, the current version is used.
// This function does NOT expand environment variables so ${VAR} references
// are preserved in the output.
func MigrateConfigBytes(data []byte, targetVersion string) ([]byte, error) {
	reg := DefaultRegistry()

	if targetVersion == "" {
		targetVersion = reg.Current()
	}

	// Validate target version exists and is not removed
	targetInfo, ok := reg.Get(targetVersion)
	if !ok {
		return nil, fmt.Errorf("unknown target version %q; supported: %s",
			targetVersion, strings.Join(reg.ListSupported(), ", "))
	}
	if targetInfo.Status == VersionRemoved {
		return nil, fmt.Errorf("target version %q has been removed", targetVersion)
	}

	// Peek at the source version (without env expansion)
	sourceVersion := PeekVersion(data)

	// Validate source version
	_, sourceErr := resolveVersion(reg, sourceVersion)
	if sourceErr != nil {
		return nil, fmt.Errorf("source config: %w", sourceErr)
	}

	// Check if apiVersion field is explicitly present in the raw YAML
	hasExplicitVersion := hasAPIVersionField(data)

	// If source == target and apiVersion is present, pass through unchanged
	if sourceVersion == targetVersion && hasExplicitVersion {
		return data, nil
	}

	// If source == target but apiVersion is missing, prepend it
	if sourceVersion == targetVersion && !hasExplicitVersion {
		return prependAPIVersion(data, targetVersion), nil
	}

	// For actual version conversions (future), we would unmarshal via the
	// source converter, transform, and re-marshal. Currently only v1 exists,
	// so this path is unreachable.
	return nil, fmt.Errorf("migration from %s to %s is not yet implemented",
		sourceVersion, targetVersion)
}

// hasAPIVersionField checks if the raw YAML contains an explicit apiVersion field.
func hasAPIVersionField(data []byte) bool {
	var envelope ConfigEnvelope
	if err := yaml.Unmarshal(data, &envelope); err != nil {
		return false
	}
	return envelope.APIVersion != ""
}

// prependAPIVersion adds "apiVersion: <version>\n" before the existing content.
// It preserves any leading comments or blank lines by inserting the version
// line before the first non-comment, non-blank line.
func prependAPIVersion(data []byte, version string) []byte {
	versionLine := fmt.Appendf(nil, "apiVersion: %s\n", version)

	// If the data is empty, just return the version line
	if len(bytes.TrimSpace(data)) == 0 {
		return versionLine
	}

	// Find the insertion point: after leading comments but before content.
	// Leading comments start with '#' and should be preserved above apiVersion.
	lines := bytes.Split(data, []byte("\n"))
	insertIdx := 0
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			insertIdx = i + 1
			continue
		}
		break
	}

	// Build the result: leading comments + apiVersion + rest
	var buf bytes.Buffer
	for i := 0; i < insertIdx; i++ {
		_, _ = buf.Write(lines[i])
		_ = buf.WriteByte('\n')
	}
	_, _ = buf.Write(versionLine)
	for i := insertIdx; i < len(lines); i++ {
		_, _ = buf.Write(lines[i])
		if i < len(lines)-1 {
			_ = buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}
