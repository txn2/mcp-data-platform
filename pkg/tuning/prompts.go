// Package tuning provides AI tuning capabilities for the platform.
package tuning

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// PromptManager manages prompt resources.
type PromptManager struct {
	promptsDir string
	prompts    map[string]string
}

// PromptConfig configures prompt management.
type PromptConfig struct {
	PromptsDir string
}

// NewPromptManager creates a new prompt manager.
func NewPromptManager(cfg PromptConfig) *PromptManager {
	return &PromptManager{
		promptsDir: cfg.PromptsDir,
		prompts:    make(map[string]string),
	}
}

// LoadPrompts loads prompts from the configured directory.
func (m *PromptManager) LoadPrompts() error {
	if m.promptsDir == "" {
		return nil
	}

	entries, err := os.ReadDir(m.promptsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading prompts directory: %w", err)
	}

	for _, entry := range entries {
		m.loadPromptFile(entry)
	}

	return nil
}

// loadPromptFile loads a single prompt file.
func (m *PromptManager) loadPromptFile(entry os.DirEntry) {
	if entry.IsDir() {
		return
	}

	name := entry.Name()
	if !isPromptFile(name) {
		return
	}

	// #nosec G304 -- path is constructed from directory listing, not user input
	content, err := os.ReadFile(filepath.Join(m.promptsDir, name))
	if err != nil {
		return
	}

	key := strings.TrimSuffix(name, filepath.Ext(name))
	m.prompts[key] = string(content)
}

// isPromptFile checks if a filename is a valid prompt file.
func isPromptFile(name string) bool {
	if strings.ContainsAny(name, "/\\") || name == ".." {
		return false
	}
	return strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".md")
}

// Get retrieves a prompt by name.
func (m *PromptManager) Get(name string) (string, bool) {
	prompt, ok := m.prompts[name]
	return prompt, ok
}

// Set sets a prompt.
func (m *PromptManager) Set(name, content string) {
	m.prompts[name] = content
}

// All returns all prompts.
func (m *PromptManager) All() map[string]string {
	result := make(map[string]string)
	maps.Copy(result, m.prompts)
	return result
}

// BuildSystemPrompt builds a system prompt for a persona.
func BuildSystemPrompt(prefix, instructions, suffix string) string {
	var parts []string

	if prefix != "" {
		parts = append(parts, prefix)
	}

	if instructions != "" {
		parts = append(parts, instructions)
	}

	if suffix != "" {
		parts = append(parts, suffix)
	}

	return strings.Join(parts, "\n\n")
}
