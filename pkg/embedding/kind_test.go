package embedding

import "testing"

// TestKind verifies that each provider returns its declared kind. The
// platform wiring and toolkit write paths use this signal to decide
// whether to persist vectors; a misreported kind would silently
// reintroduce the #429 defect.
func TestKind(t *testing.T) {
	if got := NewNoopProvider(768).Kind(); got != KindNoop {
		t.Errorf("noop.Kind() = %q; want %q", got, KindNoop)
	}
	if got := NewOllamaProvider(OllamaConfig{}).Kind(); got != KindOllama {
		t.Errorf("ollama.Kind() = %q; want %q", got, KindOllama)
	}
}

// TestIsConfigured exercises the helper callers use as a one-liner
// guard. nil and the noop placeholder are NOT configured; a real
// provider is.
func TestIsConfigured(t *testing.T) {
	if IsConfigured(nil) {
		t.Errorf("IsConfigured(nil) = true; want false")
	}
	if IsConfigured(NewNoopProvider(768)) {
		t.Errorf("IsConfigured(noop) = true; want false")
	}
	if !IsConfigured(NewOllamaProvider(OllamaConfig{})) {
		t.Errorf("IsConfigured(ollama) = false; want true")
	}
}

// TestIsZeroVector covers the shared hybrid-vs-lexical fallback signal:
// an all-zero vector (the noop provider's output) is "zero"; any non-zero
// component, including a single one in any position, is not.
func TestIsZeroVector(t *testing.T) {
	if !IsZeroVector(nil) {
		t.Errorf("IsZeroVector(nil) = false; want true")
	}
	if !IsZeroVector([]float32{0, 0, 0}) {
		t.Errorf("IsZeroVector(all-zero) = false; want true")
	}
	if IsZeroVector([]float32{0, 0, 0.0001}) {
		t.Errorf("IsZeroVector(trailing non-zero) = true; want false")
	}
	if IsZeroVector([]float32{-1, 0, 0}) {
		t.Errorf("IsZeroVector(leading non-zero) = true; want false")
	}
}

// TestModelName returns the provider's model when exposed, else "". The
// memory write path and the indexjobs memory Sink both read the model
// this way, so they must agree.
func TestModelName(t *testing.T) {
	// Ollama exposes Model().
	if got := ModelName(NewOllamaProvider(OllamaConfig{Model: "nomic-embed-text"})); got != "nomic-embed-text" {
		t.Errorf("ModelName(ollama) = %q; want %q", got, "nomic-embed-text")
	}
	// Noop does not implement Model(); ModelName returns "".
	if got := ModelName(NewNoopProvider(768)); got != "" {
		t.Errorf("ModelName(noop) = %q; want \"\"", got)
	}
}
