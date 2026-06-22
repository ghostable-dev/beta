package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunReviewHelp(t *testing.T) {
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "review", "--help"}, strings.NewReader(""), &output, &output)

	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "Usage: ghostable review --base <ref>") ||
		!strings.Contains(text, "--format <FORMAT>") {
		t.Fatalf("expected review help output, got:\n%s", text)
	}
}
