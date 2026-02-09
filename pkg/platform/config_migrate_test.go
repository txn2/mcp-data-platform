package platform

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	migTestV1               = "v1"
	migTestServerNameTest   = "server:\n  name: test\n"
	migTestAPIVersionV1Line = "apiVersion: v1\n"
)

// errReader is a reader that always returns an error.
type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func TestMigrateConfigBytes_Idempotent(t *testing.T) {
	input := []byte(migTestAPIVersionV1Line + migTestServerNameTest)
	out, err := MigrateConfigBytes(input, migTestV1)
	require.NoError(t, err)
	assert.Equal(t, input, out, "should be unchanged when already at target version")
}

func TestMigrateConfigBytes_AddsVersion(t *testing.T) {
	input := []byte(migTestServerNameTest)
	out, err := MigrateConfigBytes(input, migTestV1)
	require.NoError(t, err)
	assert.Contains(t, string(out), migTestAPIVersionV1Line)
	assert.Contains(t, string(out), migTestServerNameTest)
}

func TestMigrateConfigBytes_PreservesComments(t *testing.T) {
	input := []byte("# My config\n# More comments\n" + migTestServerNameTest)
	out, err := MigrateConfigBytes(input, migTestV1)
	require.NoError(t, err)
	result := string(out)
	assert.True(t, strings.HasPrefix(result, "# My config\n# More comments\n"),
		"leading comments should be preserved")
	assert.Contains(t, result, migTestAPIVersionV1Line)
	assert.Contains(t, result, migTestServerNameTest)
}

func TestMigrateConfigBytes_PreservesEnvVars(t *testing.T) {
	input := []byte("server:\n  name: ${MY_VAR}\n")
	out, err := MigrateConfigBytes(input, migTestV1)
	require.NoError(t, err)
	assert.Contains(t, string(out), "${MY_VAR}")
}

func TestMigrateConfigBytes_PreservesContent(t *testing.T) {
	input := []byte(migTestAPIVersionV1Line + "server:\n  name: test\nauth:\n  allow_anonymous: true\n")
	out, err := MigrateConfigBytes(input, migTestV1)
	require.NoError(t, err)
	assert.Equal(t, string(input), string(out))
}

func TestMigrateConfigBytes_EmptyTarget(t *testing.T) {
	input := []byte(migTestServerNameTest)
	out, err := MigrateConfigBytes(input, "")
	require.NoError(t, err)
	assert.Contains(t, string(out), migTestAPIVersionV1Line,
		"empty target should default to current version")
}

func TestMigrateConfigBytes_UnknownSource(t *testing.T) {
	input := []byte("apiVersion: v99\n" + migTestServerNameTest)
	_, err := MigrateConfigBytes(input, migTestV1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported config apiVersion")
}

func TestMigrateConfigBytes_UnknownTarget(t *testing.T) {
	input := []byte(migTestAPIVersionV1Line + migTestServerNameTest)
	_, err := MigrateConfigBytes(input, "v99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown target version")
}

func TestMigrateConfig_ReaderWriter(t *testing.T) {
	r := strings.NewReader(migTestServerNameTest)
	var w bytes.Buffer

	err := MigrateConfig(r, &w, migTestV1)
	require.NoError(t, err)
	assert.Contains(t, w.String(), migTestAPIVersionV1Line)
	assert.Contains(t, w.String(), migTestServerNameTest)
}

func TestMigrateConfigBytes_EmptyInput(t *testing.T) {
	out, err := MigrateConfigBytes([]byte(""), migTestV1)
	require.NoError(t, err)
	assert.Equal(t, migTestAPIVersionV1Line, string(out))
}

func TestMigrateConfigBytes_OnlyComments(t *testing.T) {
	input := []byte("# just a comment\n# another one\n")
	out, err := MigrateConfigBytes(input, migTestV1)
	require.NoError(t, err)
	result := string(out)
	assert.Contains(t, result, "# just a comment\n")
	assert.Contains(t, result, migTestAPIVersionV1Line)
}

func TestHasAPIVersionField(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{"present", "apiVersion: v1\nserver: {}", true},
		{"absent", "server: {}", false},
		{"empty string", "apiVersion: \"\"\nserver: {}", false},
		{"invalid yaml", ":::bad", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasAPIVersionField([]byte(tt.data)))
		})
	}
}

func TestMigrateConfig_ReadError(t *testing.T) {
	var w bytes.Buffer
	err := MigrateConfig(errReader{}, &w, migTestV1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading input")
}

func TestMigrateConfig_WriteError(t *testing.T) {
	r := strings.NewReader(migTestServerNameTest)
	w := &limitedWriter{limit: 0}
	err := MigrateConfig(r, w, migTestV1)
	require.Error(t, err)
}

// limitedWriter is a writer that fails after writing limit bytes.
type limitedWriter struct {
	limit int
	wrote int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.wrote+len(p) > w.limit {
		return 0, io.ErrShortWrite
	}
	w.wrote += len(p)
	return len(p), nil
}

func TestPrependAPIVersion_LeadingBlankLines(t *testing.T) {
	input := []byte("\n\nserver:\n  name: test\n")
	out := prependAPIVersion(input, migTestV1)
	result := string(out)
	// Blank lines should come before apiVersion
	assert.Contains(t, result, "apiVersion: v1\nserver:")
}
