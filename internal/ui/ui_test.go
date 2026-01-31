package ui

import (
	"strings"
	"testing"
)

func TestBold_ContainsText(t *testing.T) {
	Init(false)
	result := Bold("hello")
	if !strings.Contains(result, "hello") {
		t.Errorf("Bold output should contain 'hello', got %q", result)
	}
}

func TestColorDisabled_PlainText(t *testing.T) {
	Init(true) // no color
	defer Init(false)

	if Bold("hello") != "hello" {
		t.Errorf("expected plain text when color disabled, got %q", Bold("hello"))
	}
	if Red("error") != "error" {
		t.Errorf("expected plain text, got %q", Red("error"))
	}
	if Green("ok") != "ok" {
		t.Errorf("expected plain text, got %q", Green("ok"))
	}
	if Yellow("warn") != "warn" {
		t.Errorf("expected plain text, got %q", Yellow("warn"))
	}
	if Dim("dim") != "dim" {
		t.Errorf("expected plain text, got %q", Dim("dim"))
	}
}

func TestLoggerInitialized(t *testing.T) {
	Init(false)
	if Logger == nil {
		t.Error("Logger should be initialized after Init()")
	}
}

func TestLogo_NoErrors(t *testing.T) {
	Init(false)
	// Logo writes to stderr; just verify no panic
	Logo()
	LogoWithTagline("test tagline")
}

func TestRandomWisdom_ReturnsQuote(t *testing.T) {
	Init(false)
	quote := RandomWisdom()
	if quote == "" {
		t.Error("RandomWisdom should return a non-empty string")
	}
	// Should not contain [engineer] placeholder
	if strings.Contains(quote, "[engineer]") {
		t.Errorf("RandomWisdom should replace [engineer] placeholder, got %q", quote)
	}
}

func TestRandomWisdom_Variety(t *testing.T) {
	Init(false)
	// Run 50 times and verify we get at least 2 different quotes
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		seen[RandomWisdom()] = true
	}
	if len(seen) < 2 {
		t.Errorf("Expected variety in quotes, but only got %d unique", len(seen))
	}
}
