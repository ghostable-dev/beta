package userpresence

import (
	"fmt"
	"io"
	"runtime"
	"strings"
)

// Request describes a local user-presence check for a protected operation.
type Request struct {
	Environment string
	Operation   string
	Interactive bool
	In          io.Reader
	Out         io.Writer
	ErrOut      io.Writer
}

// Verify requires an interactive local operating-system confirmation.
func Verify(request Request) error {
	if !request.Interactive {
		return fmt.Errorf("protected environment %q requires local user confirmation for %s; run from an interactive terminal or use a scoped GHOSTABLE_CI_TOKEN automation credential", request.Environment, request.Operation)
	}

	if err := verifyPlatformUserPresence(request); err != nil {
		return fmt.Errorf("local user confirmation failed for %s on %s: %w", request.Operation, runtime.GOOS, err)
	}
	return nil
}

func confirmationMessage(request Request) string {
	environment := strings.TrimSpace(request.Environment)
	if environment == "" {
		environment = "protected environment"
	}
	return fmt.Sprintf("access Ghostable %s secrets for %s", environment, operationLabel(request.Operation))
}

func operationLabel(operation string) string {
	switch operation {
	case "env.pull":
		return "env pull"
	case "env.run":
		return "env run"
	case "var.pull":
		return "var pull"
	default:
		return strings.TrimSpace(operation)
	}
}
