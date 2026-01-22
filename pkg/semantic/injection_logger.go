// Package semantic provides semantic layer abstractions.
package semantic

import (
	"log"
	"sync"
)

// InjectionLogger logs detected prompt injection attempts.
type InjectionLogger struct {
	mu       sync.RWMutex
	logFunc  func(format string, args ...any)
	disabled bool
}

// DefaultInjectionLogger is the default logger for injection attempts.
var DefaultInjectionLogger = &InjectionLogger{
	logFunc: log.Printf,
}

// SetLogFunc sets the logging function for injection attempts.
func (l *InjectionLogger) SetLogFunc(f func(format string, args ...any)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logFunc = f
}

// Disable disables injection logging.
func (l *InjectionLogger) Disable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.disabled = true
}

// Enable enables injection logging.
func (l *InjectionLogger) Enable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.disabled = false
}

// LogInjectionAttempt logs a detected injection attempt.
func (l *InjectionLogger) LogInjectionAttempt(source, field string, patterns []string) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.disabled || l.logFunc == nil {
		return
	}

	l.logFunc("WARNING: Prompt injection patterns detected in %s.%s: %v", source, field, patterns)
}

// DetectAndLog detects injection patterns in the input and logs if found.
// Returns true if injection was detected.
func (l *InjectionLogger) DetectAndLog(sanitizer *Sanitizer, source, field, input string) bool {
	if input == "" {
		return false
	}

	detected, patterns := sanitizer.DetectInjection(input)
	if detected {
		l.LogInjectionAttempt(source, field, patterns)
	}
	return detected
}
