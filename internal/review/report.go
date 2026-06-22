package review

import (
	"fmt"
	"io"
	"strings"
)

func PrintHuman(out io.Writer, report Report) {
	fmt.Fprintln(out, "Ghostable Review")
	fmt.Fprintln(out)
	printChangedReferences(out, report)
	printFindings(out, "Errors", report.Errors)
	printFindings(out, "Warnings", report.Warnings)
	if len(report.Errors) == 0 && len(report.Warnings) == 0 {
		fmt.Fprintln(out, "No ENV or secret review issues found.")
	}
}

func PrintGitHub(out io.Writer, report Report) {
	for _, finding := range report.Errors {
		printGitHubAnnotation(out, "error", finding)
	}
	for _, finding := range report.Warnings {
		printGitHubAnnotation(out, "warning", finding)
	}
}

func printChangedReferences(out io.Writer, report Report) {
	fmt.Fprintln(out, "Changed code references")
	if len(report.References) == 0 {
		fmt.Fprintln(out, "  None")
		fmt.Fprintln(out)
		return
	}

	for _, reference := range report.References {
		prefix := "[ok]"
		if referenceHasProblem(reference) {
			prefix = "[review]"
		}
		fmt.Fprintf(out, "%s %s\n", prefix, reference.Key)
		for _, location := range reference.Locations {
			fmt.Fprintf(out, "  %s:%d\n", location.Path, location.Line)
		}
		for _, env := range reference.Environments {
			fmt.Fprintf(out, "  %s: %s\n", env.Environment, environmentStatusText(env))
		}
		fmt.Fprintf(out, "  schema: %s\n", schemaStatusText(reference.Environments))
		if reference.ExampleFile != "" {
			fmt.Fprintf(out, "  %s: %s\n", reference.ExampleFile, presentStatus(reference.ExampleKey))
		}
	}
	fmt.Fprintln(out)
}

func printFindings(out io.Writer, heading string, findings []Finding) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintln(out, heading)
	for _, finding := range findings {
		prefix := "!"
		if finding.Severity == SeverityError {
			prefix = "x"
		}
		location := findingLocation(finding)
		if location != "" {
			fmt.Fprintf(out, "%s %s (%s)\n", prefix, finding.Message, location)
			continue
		}
		fmt.Fprintf(out, "%s %s\n", prefix, finding.Message)
	}
	fmt.Fprintln(out)
}

func referenceHasProblem(reference ReferencedKey) bool {
	for _, env := range reference.Environments {
		if env.State != "present" || !env.SchemaPresent {
			return true
		}
	}
	if reference.ExampleFile != "" && !reference.ExampleKey {
		return true
	}
	return false
}

func environmentStatusText(status EnvironmentReferenceStatus) string {
	switch status.State {
	case "present":
		return "encrypted value exists"
	case "invalid_signature":
		return "encrypted value has invalid signature"
	default:
		return "missing encrypted value"
	}
}

func schemaStatusText(statuses []EnvironmentReferenceStatus) string {
	if len(statuses) == 0 {
		return "missing"
	}
	for _, status := range statuses {
		if !status.SchemaPresent {
			return "missing"
		}
	}
	return "present"
}

func presentStatus(present bool) string {
	if present {
		return "present"
	}
	return "missing"
}

func findingLocation(finding Finding) string {
	if finding.Path == "" {
		return ""
	}
	if finding.Line > 0 {
		return fmt.Sprintf("%s:%d", finding.Path, finding.Line)
	}
	return finding.Path
}

func printGitHubAnnotation(out io.Writer, level string, finding Finding) {
	properties := []string{"title=Ghostable review"}
	if finding.Path != "" {
		properties = append(properties, "file="+escapeGitHubProperty(finding.Path))
	}
	if finding.Line > 0 {
		properties = append(properties, fmt.Sprintf("line=%d", finding.Line))
	}
	fmt.Fprintf(out, "::%s %s::%s\n", level, strings.Join(properties, ","), escapeGitHubMessage(finding.Message))
}

func escapeGitHubMessage(value string) string {
	value = strings.ReplaceAll(value, "%", "%25")
	value = strings.ReplaceAll(value, "\r", "%0D")
	value = strings.ReplaceAll(value, "\n", "%0A")
	return value
}

func escapeGitHubProperty(value string) string {
	value = escapeGitHubMessage(value)
	value = strings.ReplaceAll(value, ":", "%3A")
	value = strings.ReplaceAll(value, ",", "%2C")
	return value
}
