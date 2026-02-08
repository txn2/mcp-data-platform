package tuning

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	testInstructions = "Instructions"
	testPrefix       = "Prefix"
	testSuffix       = "Suffix"
)

func TestNewPromptManager(t *testing.T) {
	pm := NewPromptManager(PromptConfig{
		PromptsDir: "/some/dir",
	})

	if pm == nil {
		t.Fatal("NewPromptManager() returned nil")
	}
	if pm.promptsDir != "/some/dir" {
		t.Errorf("promptsDir = %q, want %q", pm.promptsDir, "/some/dir")
	}
	if pm.prompts == nil {
		t.Error("prompts map is nil")
	}
}

func TestPromptManagerLoadPrompts_EmptyDir(t *testing.T) {
	pm := NewPromptManager(PromptConfig{
		PromptsDir: "",
	})

	err := pm.LoadPrompts()
	if err != nil {
		t.Errorf("LoadPrompts() error = %v", err)
	}
}

func TestPromptManagerLoadPrompts_NonexistentDir(t *testing.T) {
	pm := NewPromptManager(PromptConfig{
		PromptsDir: "/nonexistent/path/that/does/not/exist",
	})

	err := pm.LoadPrompts()
	if err != nil {
		t.Errorf("LoadPrompts() error = %v (should return nil for nonexistent dir)", err)
	}
}

func TestPromptManagerLoadPrompts_FileNotDir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not_a_dir")
	if err := os.WriteFile(filePath, []byte("file content"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	pm := NewPromptManager(PromptConfig{
		PromptsDir: filePath,
	})

	err := pm.LoadPrompts()
	if err == nil {
		t.Error("LoadPrompts() expected error when path is a file, not directory")
	}
}

func TestPromptManagerLoadPrompts_TxtFiles(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "greeting.txt"), []byte("Hello, {{name}}!"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "farewell.md"), []byte("Goodbye!"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	pm := NewPromptManager(PromptConfig{
		PromptsDir: dir,
	})

	if err := pm.LoadPrompts(); err != nil {
		t.Fatalf("LoadPrompts() error = %v", err)
	}

	assertPromptContent(t, pm, "greeting", "Hello, {{name}}!")
	assertPromptContent(t, pm, "farewell", "Goodbye!")
	assertPromptNotLoaded(t, pm, "ignored")
}

func TestPromptManagerLoadPrompts_SkipsDirs(t *testing.T) {
	dir := t.TempDir()

	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o750); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(subdir, "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	pm := NewPromptManager(PromptConfig{
		PromptsDir: dir,
	})

	if err := pm.LoadPrompts(); err != nil {
		t.Fatalf("LoadPrompts() error = %v", err)
	}

	assertPromptNotLoaded(t, pm, "nested")
}

// assertPromptContent checks that a prompt exists with the expected content.
func assertPromptContent(t *testing.T, pm *PromptManager, name, expectedContent string) {
	t.Helper()
	content, ok := pm.Get(name)
	if !ok {
		t.Errorf("%s prompt not loaded", name)
		return
	}
	if content != expectedContent {
		t.Errorf("%s content = %q, want %q", name, content, expectedContent)
	}
}

// assertPromptNotLoaded checks that a prompt was not loaded.
func assertPromptNotLoaded(t *testing.T, pm *PromptManager, name string) {
	t.Helper()
	_, ok := pm.Get(name)
	if ok {
		t.Errorf("%s should not have been loaded", name)
	}
}

func TestIsPromptFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{"txt file", "prompt.txt", true},
		{"md file", "prompt.md", true},
		{"json file", "prompt.json", false},
		{"yaml file", "prompt.yaml", false},
		{"no extension", "prompt", false},
		{"dot dot", "..", false},
		{"path with slash", "dir/file.txt", false},
		{"path with backslash", "dir\\file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPromptFile(tt.filename)
			if result != tt.expected {
				t.Errorf("isPromptFile(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestPromptManagerGetSet(t *testing.T) {
	pm := NewPromptManager(PromptConfig{})

	t.Run("Get missing", func(t *testing.T) {
		_, ok := pm.Get("nonexistent")
		if ok {
			t.Error("Get() returned true for nonexistent prompt")
		}
	})

	t.Run("Set and Get", func(t *testing.T) {
		pm.Set("test", "test content")

		content, ok := pm.Get("test")
		if !ok {
			t.Error("Get() returned false after Set()")
		}
		if content != "test content" {
			t.Errorf("content = %q", content)
		}
	})

	t.Run("Overwrite", func(t *testing.T) {
		pm.Set("test", "first")
		pm.Set("test", "second")

		content, _ := pm.Get("test")
		if content != "second" {
			t.Errorf("content = %q, want 'second'", content)
		}
	})
}

func TestPromptManagerAll(t *testing.T) {
	pm := NewPromptManager(PromptConfig{})
	pm.Set("prompt1", "content1")
	pm.Set("prompt2", "content2")

	all := pm.All()

	if len(all) != 2 {
		t.Errorf("All() returned %d prompts, want 2", len(all))
	}
	if all["prompt1"] != "content1" {
		t.Errorf("all[prompt1] = %q", all["prompt1"])
	}
	if all["prompt2"] != "content2" {
		t.Errorf("all[prompt2] = %q", all["prompt2"])
	}

	// Verify it returns a copy
	all["prompt3"] = "content3"
	_, ok := pm.Get("prompt3")
	if ok {
		t.Error("modifying returned map affected internal state")
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name         string
		prefix       string
		instructions string
		suffix       string
		expected     string
	}{
		{"all parts", testPrefix, testInstructions, testSuffix, "Prefix\n\nInstructions\n\nSuffix"},
		{"no prefix", "", testInstructions, testSuffix, "Instructions\n\nSuffix"},
		{"no suffix", testPrefix, testInstructions, "", "Prefix\n\nInstructions"},
		{"only instructions", "", testInstructions, "", testInstructions},
		{"only prefix", testPrefix, "", "", testPrefix},
		{"only suffix", "", "", testSuffix, testSuffix},
		{"empty", "", "", "", ""},
		{"prefix and suffix", testPrefix, "", testSuffix, "Prefix\n\nSuffix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSystemPrompt(tt.prefix, tt.instructions, tt.suffix)
			if result != tt.expected {
				t.Errorf("BuildSystemPrompt(%q, %q, %q) = %q, want %q",
					tt.prefix, tt.instructions, tt.suffix, result, tt.expected)
			}
		})
	}
}
