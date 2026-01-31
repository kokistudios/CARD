package claude

import (
	"strings"
	"testing"
)

func TestAvailable_ErrorMessage(t *testing.T) {
	// We can't guarantee claude is or isn't on PATH, but we can test
	// that Available() returns a meaningful error when it fails.
	err := Available()
	if err != nil {
		if !strings.Contains(err.Error(), "claude CLI not found") {
			t.Errorf("unexpected error message: %v", err)
		}
	}
	// If claude IS available, no error is expected — both cases are valid
}
