package review

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/ghostable-dev/beta/internal/store"
)

type ReviewInput struct {
	Root         string
	BaseRef      string
	HeadRef      string
	Environments []string
	Format       string
	Status       func(message string)
}

type Report struct {
	Root             string            `json:"root"`
	BaseRef          string            `json:"baseRef"`
	HeadRef          string            `json:"headRef,omitempty"`
	Environments     []string          `json:"environments"`
	ChangedFiles     []ChangedFile     `json:"changedFiles"`
	References       []ReferencedKey   `json:"references"`
	ChangedVariables []ChangedVariable `json:"changedVariables,omitempty"`
	Errors           []Finding         `json:"errors"`
	Warnings         []Finding         `json:"warnings"`
	Passed           bool              `json:"passed"`
}

type ReferencedKey struct {
	Key          string                       `json:"key"`
	Locations    []Location                   `json:"locations"`
	Environments []EnvironmentReferenceStatus `json:"environments"`
	ExampleFile  string                       `json:"exampleFile,omitempty"`
	ExampleKey   bool                         `json:"exampleKey"`
}

type EnvironmentReferenceStatus struct {
	Environment    string `json:"environment"`
	State          string `json:"state"`
	SchemaPresent  bool   `json:"schemaPresent"`
	Commented      bool   `json:"commented,omitempty"`
	ValidSignature bool   `json:"validSignature,omitempty"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
}

type Location struct {
	Path string `json:"path,omitempty"`
	Line int    `json:"line,omitempty"`
}

type Finding struct {
	Severity    string `json:"severity"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Path        string `json:"path,omitempty"`
	Line        int    `json:"line,omitempty"`
	Key         string `json:"key,omitempty"`
	Environment string `json:"environment,omitempty"`
}

func Review(ctx context.Context, input ReviewInput) (Report, error) {
	input = normalizeReviewInput(input)

	reportStatus(input, "Opening Ghostable project")
	repo, err := store.OpenProject(input.Root)
	if err != nil {
		return Report{}, err
	}

	input.BaseRef, err = resolveReviewBaseRef(ctx, repo.Root, input.BaseRef)
	if err != nil {
		return Report{}, err
	}

	reportStatus(input, "Reading git changes")
	changes, err := readGitChanges(ctx, repo.Root, input.BaseRef, input.HeadRef)
	if err != nil {
		return Report{}, err
	}

	reportStatus(input, "Scanning changed ENV references")
	environments := resolveReviewEnvironments(repo, input.Environments)
	references := findChangedReferences(changes.AddedLines, changes.Files)
	referenceKeys := referenceKeyMap(references)

	reportStatus(input, "Reading encrypted ENV metadata")
	inventories, inventoryFindings := readInventories(repo, environments)
	changedVariables, changedVariableFindings := readChangedVariables(repo.Root, changes.Files)
	schemaKeys, schemaFindings := loadSchemaKeys(repo.Root, environments)
	exampleKeys, exampleExists, exampleFinding := readDotenvExampleKeys(repo.Root)

	report := Report{
		Root:             repo.Root,
		BaseRef:          input.BaseRef,
		HeadRef:          input.HeadRef,
		Environments:     environments,
		ChangedFiles:     changes.Files,
		References:       buildReferencedKeys(references, environments, inventories, schemaKeys, exampleKeys, exampleExists),
		ChangedVariables: changedVariables,
		Errors:           []Finding{},
		Warnings:         []Finding{},
	}

	report.addFindings(inventoryFindings)
	report.addFindings(changedVariableFindings)
	report.addFindings(schemaFindings)
	report.addFindings(verifyChangedGhostableMetadata(repo, changes.Files))
	if exampleFinding.Message != "" {
		report.addFinding(exampleFinding)
	}

	reportStatus(input, "Checking ENV review rules")
	runChecks(&report, checkContext{
		Files:            changes.Files,
		References:       references,
		ReferenceKeys:    referenceKeys,
		Inventories:      inventories,
		ChangedVariables: changedVariables,
		SchemaKeys:       schemaKeys,
		ExampleKeys:      exampleKeys,
		ExampleExists:    exampleExists,
		Root:             repo.Root,
	})

	sortReport(&report)
	report.Passed = len(report.Errors) == 0
	return report, nil
}

func reportStatus(input ReviewInput, message string) {
	if input.Status != nil {
		input.Status(message)
	}
}

func normalizeReviewInput(input ReviewInput) ReviewInput {
	if strings.TrimSpace(input.Root) == "" {
		input.Root = "."
	}
	input.BaseRef = strings.TrimSpace(input.BaseRef)
	input.HeadRef = strings.TrimSpace(input.HeadRef)
	input.Format = strings.TrimSpace(input.Format)
	return input
}

func buildReferencedKeys(references []Reference, environments []string, inventories inventoryByEnvironment, schemaKeys map[string]map[string]bool, exampleKeys map[string]bool, exampleExists bool) []ReferencedKey {
	byKey := referenceKeyMap(references)
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]ReferencedKey, 0, len(keys))
	for _, key := range keys {
		locations := referenceLocations(byKey[key])
		referenced := ReferencedKey{
			Key:          key,
			Locations:    locations,
			Environments: environmentReferenceStatuses(key, environments, inventories, schemaKeys),
			ExampleKey:   exampleExists && exampleKeys[key],
		}
		if exampleExists {
			referenced.ExampleFile = ".env.example"
		}
		result = append(result, referenced)
	}
	return result
}

func referenceLocations(references []Reference) []Location {
	seen := map[string]bool{}
	locations := []Location{}
	for _, reference := range references {
		id := reference.Path + ":" + strconv.Itoa(reference.Line)
		if seen[id] {
			continue
		}
		seen[id] = true
		locations = append(locations, Location{Path: reference.Path, Line: reference.Line})
	}
	sort.Slice(locations, func(i, j int) bool {
		if locations[i].Path == locations[j].Path {
			return locations[i].Line < locations[j].Line
		}
		return locations[i].Path < locations[j].Path
	})
	return locations
}

func environmentReferenceStatuses(key string, environments []string, inventories inventoryByEnvironment, schemaKeys map[string]map[string]bool) []EnvironmentReferenceStatus {
	statuses := make([]EnvironmentReferenceStatus, 0, len(environments))
	for _, env := range environments {
		status := EnvironmentReferenceStatus{
			Environment:   env,
			State:         "missing",
			SchemaPresent: schemaKeys[env][key],
		}
		if variable, ok := inventories[env][key]; ok {
			status.State = "present"
			if !variable.ValidSignature {
				status.State = "invalid_signature"
			}
			status.Commented = variable.Commented
			status.ValidSignature = variable.ValidSignature
			status.UpdatedAt = variable.UpdatedAt
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (r *Report) addFindings(findings []Finding) {
	for _, finding := range findings {
		r.addFinding(finding)
	}
}

func (r *Report) addFinding(finding Finding) {
	switch finding.Severity {
	case SeverityWarning:
		r.Warnings = append(r.Warnings, finding)
	default:
		finding.Severity = SeverityError
		r.Errors = append(r.Errors, finding)
	}
}

func (r Report) HasErrors() bool {
	return len(r.Errors) > 0
}

func sortReport(report *Report) {
	sortFindings(report.Errors)
	sortFindings(report.Warnings)
	sort.Slice(report.ChangedVariables, func(i, j int) bool {
		if report.ChangedVariables[i].Environment == report.ChangedVariables[j].Environment {
			return report.ChangedVariables[i].Key < report.ChangedVariables[j].Key
		}
		return report.ChangedVariables[i].Environment < report.ChangedVariables[j].Environment
	})
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path == findings[j].Path {
			if findings[i].Line == findings[j].Line {
				return findings[i].Message < findings[j].Message
			}
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Path < findings[j].Path
	})
}

func fileFinding(severity string, code string, path string, line int, message string) Finding {
	return Finding{
		Severity: severity,
		Code:     code,
		Path:     path,
		Line:     line,
		Message:  message,
	}
}
