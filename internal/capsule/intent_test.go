package capsule

import (
	"testing"
)

func TestDetectInvalidationIntent(t *testing.T) {
	tests := []struct {
		name           string
		text           string
		expectDetected bool
		expectConf     string
	}{
		{
			name:           "high confidence - wrong decision",
			text:           "The previous decision was wrong about using REST",
			expectDetected: true,
			expectConf:     "high",
		},
		{
			name:           "high confidence - should not have",
			text:           "We should not have used Redux for this",
			expectDetected: true,
			expectConf:     "high",
		},
		{
			name:           "high confidence - that was a mistake",
			text:           "That was a mistake, let's fix it",
			expectDetected: true,
			expectConf:     "high",
		},
		{
			name:           "high confidence - turns out wrong",
			text:           "It turns out the approach was wrong",
			expectDetected: true,
			expectConf:     "high",
		},
		{
			name:           "high confidence - invalidate decision",
			text:           "Let's invalidate this decision and try something else",
			expectDetected: true,
			expectConf:     "high",
		},
		{
			name:           "medium confidence - revert",
			text:           "We need to revert to the old implementation",
			expectDetected: true,
			expectConf:     "medium",
		},
		{
			name:           "medium confidence - actually should use",
			text:           "Actually, we should use GraphQL instead",
			expectDetected: true,
			expectConf:     "medium",
		},
		{
			name:           "medium confidence - changing mind",
			text:           "I'm changing my mind about this approach",
			expectDetected: true,
			expectConf:     "medium",
		},
		{
			name:           "medium confidence - didn't work out",
			text:           "That didn't work out as expected",
			expectDetected: true,
			expectConf:     "medium",
		},
		{
			name:           "low confidence - why did we",
			text:           "Why did we choose this library?",
			expectDetected: true,
			expectConf:     "low",
		},
		{
			name:           "low confidence - better approach",
			text:           "There might be a better approach here",
			expectDetected: true,
			expectConf:     "low",
		},
		{
			name:           "no detection - neutral statement",
			text:           "The implementation uses React for the frontend",
			expectDetected: false,
			expectConf:     "",
		},
		{
			name:           "no detection - positive statement",
			text:           "Great decision to use TypeScript",
			expectDetected: false,
			expectConf:     "",
		},
		{
			name:           "no detection - question about feature",
			text:           "How does the authentication work?",
			expectDetected: false,
			expectConf:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := DetectInvalidationIntent(tt.text)
			if intent.Detected != tt.expectDetected {
				t.Errorf("DetectInvalidationIntent(%q).Detected = %v, want %v",
					tt.text, intent.Detected, tt.expectDetected)
			}
			if tt.expectDetected && intent.Confidence != tt.expectConf {
				t.Errorf("DetectInvalidationIntent(%q).Confidence = %q, want %q",
					tt.text, intent.Confidence, tt.expectConf)
			}
		})
	}
}

func TestExtractCapsuleReferences(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected []string
	}{
		{
			name:     "capsule ID pattern",
			text:     "See decision 20260130-add-auth-abc123-investigate-ab12cd34",
			expected: []string{"20260130-add-auth-abc123-investigate-ab12cd34"},
		},
		{
			name:     "decision keyword",
			text:     "That relates to decision auth-choice",
			expected: []string{"auth-choice"},
		},
		{
			name:     "capsule keyword",
			text:     "Check capsule abc123def",
			expected: []string{"abc123def"},
		},
		{
			name:     "no references",
			text:     "Just a normal sentence about code",
			expected: nil,
		},
		{
			name:     "multiple references",
			text:     "Decision foo-bar and decision baz-qux are related",
			expected: []string{"foo-bar", "baz-qux"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := ExtractCapsuleReferences(tt.text)
			if len(refs) != len(tt.expected) {
				t.Errorf("ExtractCapsuleReferences(%q) returned %d refs, want %d",
					tt.text, len(refs), len(tt.expected))
				return
			}
			for i, ref := range refs {
				if ref != tt.expected[i] {
					t.Errorf("ExtractCapsuleReferences(%q)[%d] = %q, want %q",
						tt.text, i, ref, tt.expected[i])
				}
			}
		})
	}
}

func TestInvalidationPrompt(t *testing.T) {
	tests := []struct {
		name       string
		intent     InvalidationIntent
		expectText bool
	}{
		{
			name:       "not detected",
			intent:     InvalidationIntent{Detected: false},
			expectText: false,
		},
		{
			name: "high confidence with ID",
			intent: InvalidationIntent{
				Detected:   true,
				Confidence: "high",
				CapsuleID:  "abc123",
			},
			expectText: true,
		},
		{
			name: "medium confidence",
			intent: InvalidationIntent{
				Detected:   true,
				Confidence: "medium",
			},
			expectText: true,
		},
		{
			name: "low confidence",
			intent: InvalidationIntent{
				Detected:   true,
				Confidence: "low",
			},
			expectText: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := tt.intent.InvalidationPrompt()
			hasText := prompt != ""
			if hasText != tt.expectText {
				t.Errorf("InvalidationPrompt() returned %q, expectText=%v",
					prompt, tt.expectText)
			}
		})
	}
}
