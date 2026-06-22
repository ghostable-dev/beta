package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/dotenv"
	"github.com/ghostable-dev/beta/internal/store"
	"github.com/ghostable-dev/beta/internal/validation"
)

type ChangedVariable struct {
	Environment string `json:"environment"`
	Key         string `json:"key"`
	Path        string `json:"path"`
	Status      string `json:"status"`
}

type inventoryByEnvironment map[string]map[string]store.VariableMetadata

func resolveReviewEnvironments(repo store.Repository, requested []string) []string {
	if len(requested) > 0 {
		return uniqueStrings(requested)
	}

	available := map[string]bool{}
	for _, env := range repo.Environments() {
		available[env.Name] = true
	}

	envs := []string{}
	for _, env := range repo.Manifest.AuditEnvs {
		if available[env] {
			envs = append(envs, env)
		}
	}
	if len(envs) > 0 {
		return uniqueStrings(envs)
	}

	for env := range available {
		envs = append(envs, env)
	}
	return uniqueSortedStrings(envs)
}

func readInventories(repo store.Repository, environments []string) (inventoryByEnvironment, []Finding) {
	inventories := inventoryByEnvironment{}
	findings := []Finding{}
	for _, env := range environments {
		metadata, err := repo.ReadVariableMetadata(env)
		if err != nil {
			findings = append(findings, Finding{
				Severity:    SeverityError,
				Code:        "inventory_read_failed",
				Message:     fmt.Sprintf("could not read encrypted variable metadata for %s: %v", env, err),
				Environment: env,
			})
			continue
		}
		inventories[env] = map[string]store.VariableMetadata{}
		for _, variable := range metadata {
			inventories[env][variable.Key] = variable
		}
	}
	return inventories, findings
}

func readChangedVariables(root string, files []ChangedFile) ([]ChangedVariable, []Finding) {
	changed := []ChangedVariable{}
	findings := []Finding{}
	for _, file := range files {
		if !isGhostableValuePath(file.Path) || file.Status == "deleted" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			findings = append(findings, fileFinding(SeverityError, "value_metadata_read_failed", file.Path, 0, fmt.Sprintf("could not read changed encrypted value metadata: %v", err)))
			continue
		}
		var record domain.ValueRecord
		if err := json.Unmarshal(content, &record); err != nil {
			findings = append(findings, fileFinding(SeverityError, "value_metadata_parse_failed", file.Path, 0, fmt.Sprintf("could not parse changed encrypted value metadata: %v", err)))
			continue
		}
		if record.Environment == "" || record.Key == "" {
			findings = append(findings, fileFinding(SeverityError, "value_metadata_incomplete", file.Path, 0, "changed encrypted value metadata is missing its environment or key"))
			continue
		}
		changed = append(changed, ChangedVariable{
			Environment: record.Environment,
			Key:         record.Key,
			Path:        file.Path,
			Status:      file.Status,
		})
	}

	sort.Slice(changed, func(i, j int) bool {
		if changed[i].Environment == changed[j].Environment {
			return changed[i].Key < changed[j].Key
		}
		return changed[i].Environment < changed[j].Environment
	})
	return changed, findings
}

func verifyChangedGhostableMetadata(repo store.Repository, files []ChangedFile) []Finding {
	findings := []Finding{}
	for _, file := range files {
		if file.Category != FileCategoryGhostable {
			continue
		}
		if file.Path == ".ghostable/policy.json" {
			if file.Status == "deleted" {
				findings = append(findings, fileFinding(SeverityError, "policy_deleted", file.Path, 0, "Ghostable policy was deleted"))
				continue
			}
			if err := repo.VerifyPolicyMetadata(); err != nil {
				findings = append(findings, fileFinding(SeverityError, "policy_invalid", file.Path, 0, fmt.Sprintf("Ghostable policy does not verify: %v", err)))
			}
			continue
		}
		if isGhostableDevicePath(file.Path) && file.Status != "deleted" {
			if err := repo.VerifyDeviceMetadataFile(file.Path); err != nil {
				findings = append(findings, fileFinding(SeverityError, "device_invalid", file.Path, 0, fmt.Sprintf("Ghostable device record does not verify: %v", err)))
			}
			continue
		}
		if isGhostableAccessGrantPath(file.Path) && file.Status != "deleted" {
			if err := repo.VerifyAccessGrantMetadataFile(file.Path); err != nil {
				findings = append(findings, fileFinding(SeverityError, "access_grant_invalid", file.Path, 0, fmt.Sprintf("Ghostable access grant does not verify: %v", err)))
			}
		}
	}
	return findings
}

func loadSchemaKeys(root string, environments []string) (map[string]map[string]bool, []Finding) {
	schemaKeys := map[string]map[string]bool{}
	findings := []Finding{}
	for _, env := range environments {
		rules, _, err := validation.LoadRules(root, env)
		if err != nil {
			findings = append(findings, Finding{
				Severity:    SeverityError,
				Code:        "schema_read_failed",
				Message:     fmt.Sprintf("could not read schema rules for %s: %v", env, err),
				Environment: env,
			})
			continue
		}
		schemaKeys[env] = map[string]bool{}
		for key := range rules {
			schemaKeys[env][key] = true
		}
	}
	return schemaKeys, findings
}

func readDotenvExampleKeys(root string) (map[string]bool, bool, Finding) {
	path := filepath.Join(root, ".env.example")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, false, Finding{}
		}
		return map[string]bool{}, false, fileFinding(SeverityError, "env_example_read_failed", ".env.example", 0, fmt.Sprintf("could not read .env.example: %v", err))
	}
	defer file.Close()

	parsed, err := dotenv.Parse(file)
	if err != nil {
		return map[string]bool{}, true, fileFinding(SeverityError, "env_example_parse_failed", ".env.example", 0, fmt.Sprintf("could not parse .env.example: %v", err))
	}

	keys := map[string]bool{}
	for key := range parsed.Entries {
		keys[key] = true
	}
	return keys, true, Finding{}
}

func isGhostableValuePath(path string) bool {
	path = filepath.ToSlash(path)
	return strings.HasPrefix(path, ".ghostable/environments/") &&
		strings.Contains(path, "/values/") &&
		strings.HasSuffix(path, ".json")
}

func isGhostableAccessGrantPath(path string) bool {
	path = filepath.ToSlash(path)
	return strings.HasPrefix(path, ".ghostable/environments/") &&
		strings.Contains(path, "/access/") &&
		strings.HasSuffix(path, ".json")
}

func isGhostableDevicePath(path string) bool {
	path = filepath.ToSlash(path)
	return strings.HasPrefix(path, ".ghostable/devices/") && strings.HasSuffix(path, ".json")
}

func uniqueSortedStrings(values []string) []string {
	result := uniqueStrings(values)
	sort.Strings(result)
	return result
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
