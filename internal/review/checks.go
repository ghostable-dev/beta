package review

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ghostable-dev/beta/internal/dotenv"
)

const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

type checkContext struct {
	Files            []ChangedFile
	References       []Reference
	ReferenceKeys    map[string][]Reference
	Inventories      inventoryByEnvironment
	ChangedVariables []ChangedVariable
	SchemaKeys       map[string]map[string]bool
	ExampleKeys      map[string]bool
	ExampleExists    bool
	Root             string
}

var sensitiveKeyPattern = regexp.MustCompile(`(?i)(^APP_KEY$|API[_-]?KEY|SECRET|TOKEN|PASSWORD|PRIVATE[_-]?KEY|CLIENT[_-]?SECRET|ACCESS[_-]?KEY)`)

func runChecks(report *Report, context checkContext) {
	checkMissingEncryptedValues(report, context)
	checkInvalidValueSignatures(report, context)
	checkMissingSchemaRules(report, context)
	checkMissingDotenvExampleKeys(report, context)
	checkChangedVariablesWithoutCodeReferences(report, context)
	checkPlaintextEnvFiles(report, context)
}

func checkMissingEncryptedValues(report *Report, context checkContext) {
	for key, references := range context.ReferenceKeys {
		location := firstReferenceLocation(references)
		for _, env := range report.Environments {
			if _, ok := context.Inventories[env][key]; ok {
				continue
			}
			report.addFinding(Finding{
				Severity:    SeverityError,
				Code:        "missing_encrypted_value",
				Key:         key,
				Environment: env,
				Path:        location.Path,
				Line:        location.Line,
				Message:     fmt.Sprintf("%s is referenced in %s:%d but missing from %s", key, location.Path, location.Line, env),
			})
		}
	}
}

func checkInvalidValueSignatures(report *Report, context checkContext) {
	for env, variables := range context.Inventories {
		for key, variable := range variables {
			if variable.ValidSignature {
				continue
			}
			message := fmt.Sprintf("%s in %s has an invalid value signature", key, env)
			if variable.SignatureError != "" {
				message += ": " + variable.SignatureError
			}
			report.addFinding(Finding{
				Severity:    SeverityError,
				Code:        "invalid_value_signature",
				Key:         key,
				Environment: env,
				Message:     message,
			})
		}
	}
}

func checkMissingSchemaRules(report *Report, context checkContext) {
	for key, references := range context.ReferenceKeys {
		if schemaRuleExistsForAnyEnvironment(key, context.SchemaKeys, report.Environments) {
			continue
		}
		location := firstReferenceLocation(references)
		report.addFinding(Finding{
			Severity: SeverityWarning,
			Code:     "missing_schema_rule",
			Key:      key,
			Path:     location.Path,
			Line:     location.Line,
			Message:  fmt.Sprintf("%s is referenced in code but has no Ghostable schema rule", key),
		})
	}
}

func checkMissingDotenvExampleKeys(report *Report, context checkContext) {
	if !context.ExampleExists {
		return
	}
	for key, references := range context.ReferenceKeys {
		if context.ExampleKeys[key] {
			continue
		}
		location := firstReferenceLocation(references)
		report.addFinding(Finding{
			Severity: SeverityWarning,
			Code:     "missing_env_example_key",
			Key:      key,
			Path:     location.Path,
			Line:     location.Line,
			Message:  fmt.Sprintf(".env.example does not include %s", key),
		})
	}
}

func checkChangedVariablesWithoutCodeReferences(report *Report, context checkContext) {
	for _, variable := range context.ChangedVariables {
		if _, ok := context.ReferenceKeys[variable.Key]; ok {
			continue
		}
		report.addFinding(Finding{
			Severity:    SeverityWarning,
			Code:        "changed_value_without_reference",
			Key:         variable.Key,
			Environment: variable.Environment,
			Path:        variable.Path,
			Message:     fmt.Sprintf("%s changed in %s but no matching code reference changed", variable.Key, variable.Environment),
		})
	}
}

func checkPlaintextEnvFiles(report *Report, context checkContext) {
	for _, file := range context.Files {
		if file.Category != FileCategoryPlaintextEnv || file.Status == "deleted" {
			continue
		}
		findings := inspectPlaintextEnvFile(context.Root, file.Path)
		for _, finding := range findings {
			report.addFinding(finding)
		}
	}
}

func inspectPlaintextEnvFile(root string, path string) []Finding {
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return []Finding{fileFinding(SeverityError, "plaintext_env_read_failed", path, 0, fmt.Sprintf("could not read plaintext env file: %v", err))}
	}
	defer file.Close()

	parsed, err := dotenv.Parse(file)
	if err != nil {
		return []Finding{fileFinding(SeverityError, "plaintext_env_parse_failed", path, 0, fmt.Sprintf("could not parse plaintext env file: %v", err))}
	}

	findings := []Finding{}
	for _, entry := range parsed.Entries {
		if entry.Disabled || !looksSensitiveKey(entry.Key) || !looksPlaintextSecretValue(entry.Value) {
			continue
		}
		findings = append(findings, Finding{
			Severity: SeverityError,
			Code:     "plaintext_env_secret",
			Key:      entry.Key,
			Path:     path,
			Line:     entry.Line,
			Message:  fmt.Sprintf("%s contains a plaintext-looking secret in %s", entry.Key, path),
		})
	}
	return findings
}

func schemaRuleExistsForAnyEnvironment(key string, schemas map[string]map[string]bool, environments []string) bool {
	for _, env := range environments {
		if schemas[env][key] {
			return true
		}
	}
	return false
}

func firstReferenceLocation(references []Reference) Location {
	if len(references) == 0 {
		return Location{}
	}
	return Location{Path: references[0].Path, Line: references[0].Line}
}

func looksSensitiveKey(key string) bool {
	return sensitiveKeyPattern.MatchString(key)
}

func looksPlaintextSecretValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) < 6 {
		return false
	}
	if strings.HasPrefix(value, "$") || strings.HasPrefix(value, "${") {
		return false
	}
	return !looksPlaceholderValue(value)
}

func looksPlaceholderValue(value string) bool {
	value = strings.Trim(strings.ToLower(strings.TrimSpace(value)), `"'`)
	if value == "" {
		return true
	}
	placeholders := []string{
		"your_", "example", "placeholder", "changeme", "change-me", "todo", "xxx", "dummy", "sample", "not-a-secret",
	}
	for _, placeholder := range placeholders {
		if strings.Contains(value, placeholder) {
			return true
		}
	}
	return false
}

func referenceKeyMap(references []Reference) map[string][]Reference {
	result := map[string][]Reference{}
	for _, reference := range references {
		result[reference.Key] = append(result[reference.Key], reference)
	}
	return result
}
