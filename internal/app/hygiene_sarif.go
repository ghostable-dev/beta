package app

import "sort"

type sarifLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	ShortDescription sarifText         `json:"shortDescription"`
	Properties       map[string]string `json:"properties,omitempty"`
}

type sarifResult struct {
	RuleID       string             `json:"ruleId"`
	Level        string             `json:"level"`
	Message      sarifText          `json:"message"`
	Locations    []sarifLocation    `json:"locations,omitempty"`
	Suppressions []sarifSuppression `json:"suppressions,omitempty"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifSuppression struct {
	Kind          string `json:"kind"`
	Justification string `json:"justification,omitempty"`
	GUID          string `json:"guid,omitempty"`
}

func hygieneSARIF(report hygieneReport, includeSuppressed bool) sarifLog {
	findings := report.Findings
	if includeSuppressed {
		findings = append(findings, report.SuppressedFindings...)
	}

	ruleMap := map[string]hygieneFinding{}
	for _, finding := range findings {
		ruleMap[finding.Code] = finding
	}
	rules := make([]sarifRule, 0, len(ruleMap))
	for code, finding := range ruleMap {
		rules = append(rules, sarifRule{
			ID:               code,
			Name:             code,
			ShortDescription: sarifText{Text: findingSummary(finding)},
			Properties: map[string]string{
				"severity": finding.Severity,
			},
		})
	}
	sortSARIFRules(rules)

	results := make([]sarifResult, 0, len(findings))
	for _, finding := range findings {
		result := sarifResult{
			RuleID:  finding.Code,
			Level:   sarifLevel(finding.Severity),
			Message: sarifText{Text: finding.Message},
		}
		if finding.Path != "" {
			result.Locations = []sarifLocation{{
				PhysicalLocation: sarifPhysicalLocation{
					ArtifactLocation: sarifArtifactLocation{URI: finding.Path},
				},
			}}
		}
		if finding.Suppressed {
			result.Suppressions = []sarifSuppression{{
				Kind: "inSource",
				GUID: finding.SuppressionID,
			}}
		}
		results = append(results, result)
	}

	return sarifLog{
		Version: "2.1.0",
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs: []sarifRun{{
			Tool: sarifTool{
				Driver: sarifDriver{
					Name:  "Ghostable",
					Rules: rules,
				},
			},
			Results: results,
		}},
	}
}

func findingSummary(finding hygieneFinding) string {
	switch finding.Code {
	case "unused_variable":
		return "Stored variable is not referenced by scanned code"
	case "stale_variable":
		return "Stored variable has not changed within the configured age threshold"
	case "rotation_due":
		return "Stored variable is due for rotation"
	case "environment_key_rotation_due":
		return "Environment encryption key is due for rotation"
	case "invalid_suppression":
		return "Signed hygiene suppression is invalid"
	default:
		return finding.Message
	}
}

func sarifLevel(severity string) string {
	switch severity {
	case "error":
		return "error"
	case "warning":
		return "warning"
	default:
		return "note"
	}
}

func sortSARIFRules(rules []sarifRule) {
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].ID < rules[j].ID
	})
}
