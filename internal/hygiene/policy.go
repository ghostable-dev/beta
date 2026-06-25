package hygiene

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const DefaultPolicyPath = ".ghostable/hygiene.yaml"

type Policy struct {
	Rotation RotationPolicy `json:"rotation,omitempty"`
}

type RotationPolicy struct {
	Keys         map[string]RotationRule              `json:"keys,omitempty"`
	Environments map[string]EnvironmentRotationPolicy `json:"environments,omitempty"`
}

type EnvironmentRotationPolicy struct {
	Keys map[string]RotationRule `json:"keys,omitempty"`
}

type RotationRule struct {
	RotationAfterDays int `json:"rotationAfterDays,omitempty"`
}

type ResolvedRotationRule struct {
	Rule   RotationRule `json:"rule"`
	Source string       `json:"source"`
}

func LoadPolicy(root string) (Policy, error) {
	path := filepath.Join(root, DefaultPolicyPath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Policy{}, nil
	} else if err != nil {
		return Policy{}, err
	}
	return ParseFile(path)
}

func ParseFile(path string) (Policy, error) {
	file, err := os.Open(path)
	if err != nil {
		return Policy{}, err
	}
	defer file.Close()

	return Parse(file)
}

func Parse(reader io.Reader) (Policy, error) {
	policy := Policy{}
	scanner := bufio.NewScanner(reader)
	section := ""
	rotationSection := ""
	currentEnvironment := ""
	currentKey := ""

	for scanner.Scan() {
		raw := scanner.Text()
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}

		indent := leadingSpaces(line)
		trimmed := strings.TrimSpace(line)

		if indent == 0 {
			rotationSection = ""
			currentEnvironment = ""
			currentKey = ""
			if strings.HasPrefix(trimmed, "rotation:") {
				section = "rotation"
			} else {
				section = ""
			}
			continue
		}
		if section != "rotation" {
			continue
		}

		if indent == 2 {
			currentEnvironment = ""
			currentKey = ""
			switch {
			case strings.HasPrefix(trimmed, "keys:"):
				rotationSection = "keys"
				ensureRotationKeys(&policy)
			case strings.HasPrefix(trimmed, "environments:"):
				rotationSection = "environments"
				ensureRotationEnvironments(&policy)
			default:
				rotationSection = ""
			}
			continue
		}

		switch rotationSection {
		case "keys":
			parseProjectRotationLine(&policy, indent, trimmed, &currentKey)
		case "environments":
			parseEnvironmentRotationLine(&policy, indent, trimmed, &currentEnvironment, &currentKey)
		}
	}
	if err := scanner.Err(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func WriteFile(path string, policy Policy) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(Format(policy)), 0o644)
}

func Format(policy Policy) string {
	normalizePolicy(&policy)
	if len(policy.Rotation.Keys) == 0 && len(policy.Rotation.Environments) == 0 {
		return ""
	}

	lines := []string{"rotation:"}
	if len(policy.Rotation.Keys) > 0 {
		lines = append(lines, "  keys:")
		keys := sortedRotationKeys(policy.Rotation.Keys)
		for _, key := range keys {
			rule := policy.Rotation.Keys[key]
			lines = append(lines, fmt.Sprintf("    %s:", yamlScalar(key)))
			lines = append(lines, fmt.Sprintf("      rotationAfterDays: %d", rule.RotationAfterDays))
		}
	}
	if len(policy.Rotation.Environments) > 0 {
		lines = append(lines, "  environments:")
		environments := sortedEnvironmentNames(policy.Rotation.Environments)
		for _, env := range environments {
			envPolicy := policy.Rotation.Environments[env]
			lines = append(lines, fmt.Sprintf("    %s:", yamlScalar(env)))
			lines = append(lines, "      keys:")
			keys := sortedRotationKeys(envPolicy.Keys)
			for _, key := range keys {
				rule := envPolicy.Keys[key]
				lines = append(lines, fmt.Sprintf("        %s:", yamlScalar(key)))
				lines = append(lines, fmt.Sprintf("          rotationAfterDays: %d", rule.RotationAfterDays))
			}
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func SetProjectRotationRule(policy *Policy, key string, rule RotationRule) {
	ensureRotationKeys(policy)
	policy.Rotation.Keys[key] = rule
}

func SetEnvironmentRotationRule(policy *Policy, env string, key string, rule RotationRule) {
	ensureRotationEnvironments(policy)
	envPolicy := policy.Rotation.Environments[env]
	if envPolicy.Keys == nil {
		envPolicy.Keys = map[string]RotationRule{}
	}
	envPolicy.Keys[key] = rule
	policy.Rotation.Environments[env] = envPolicy
}

func RemoveProjectRotationRule(policy *Policy, key string) bool {
	if policy.Rotation.Keys == nil {
		return false
	}
	if _, ok := policy.Rotation.Keys[key]; !ok {
		return false
	}
	delete(policy.Rotation.Keys, key)
	normalizePolicy(policy)
	return true
}

func RemoveEnvironmentRotationRule(policy *Policy, env string, key string) bool {
	envPolicy, ok := policy.Rotation.Environments[env]
	if !ok || envPolicy.Keys == nil {
		return false
	}
	if _, ok := envPolicy.Keys[key]; !ok {
		return false
	}
	delete(envPolicy.Keys, key)
	policy.Rotation.Environments[env] = envPolicy
	normalizePolicy(policy)
	return true
}

func ResolveRotationRule(policy Policy, env string, key string) (ResolvedRotationRule, bool) {
	if rule, ok := policy.Rotation.Keys[key]; ok && rule.RotationAfterDays > 0 {
		resolved := ResolvedRotationRule{Rule: rule, Source: "project"}
		if envPolicy, ok := policy.Rotation.Environments[env]; ok {
			if envRule, ok := envPolicy.Keys[key]; ok && envRule.RotationAfterDays > 0 {
				resolved.Rule = envRule
				resolved.Source = "environment"
			}
		}
		return resolved, true
	}
	if envPolicy, ok := policy.Rotation.Environments[env]; ok {
		if rule, ok := envPolicy.Keys[key]; ok && rule.RotationAfterDays > 0 {
			return ResolvedRotationRule{Rule: rule, Source: "environment"}, true
		}
	}
	return ResolvedRotationRule{}, false
}

func parseProjectRotationLine(policy *Policy, indent int, trimmed string, currentKey *string) {
	if indent == 4 && strings.HasSuffix(trimmed, ":") {
		*currentKey = readScalar(strings.TrimSuffix(trimmed, ":"))
		if *currentKey != "" {
			ensureRotationKeys(policy)
			if _, ok := policy.Rotation.Keys[*currentKey]; !ok {
				policy.Rotation.Keys[*currentKey] = RotationRule{}
			}
		}
		return
	}
	if indent >= 6 && *currentKey != "" {
		rule := policy.Rotation.Keys[*currentKey]
		if updateRotationRule(&rule, trimmed) {
			policy.Rotation.Keys[*currentKey] = rule
		}
	}
}

func parseEnvironmentRotationLine(policy *Policy, indent int, trimmed string, currentEnvironment *string, currentKey *string) {
	if indent == 4 && strings.HasSuffix(trimmed, ":") {
		*currentEnvironment = readScalar(strings.TrimSuffix(trimmed, ":"))
		*currentKey = ""
		if *currentEnvironment != "" {
			ensureRotationEnvironments(policy)
			if _, ok := policy.Rotation.Environments[*currentEnvironment]; !ok {
				policy.Rotation.Environments[*currentEnvironment] = EnvironmentRotationPolicy{}
			}
		}
		return
	}
	if *currentEnvironment == "" {
		return
	}
	if indent == 6 && strings.HasPrefix(trimmed, "keys:") {
		*currentKey = ""
		envPolicy := policy.Rotation.Environments[*currentEnvironment]
		if envPolicy.Keys == nil {
			envPolicy.Keys = map[string]RotationRule{}
		}
		policy.Rotation.Environments[*currentEnvironment] = envPolicy
		return
	}
	if indent == 8 && strings.HasSuffix(trimmed, ":") {
		*currentKey = readScalar(strings.TrimSuffix(trimmed, ":"))
		if *currentKey != "" {
			envPolicy := policy.Rotation.Environments[*currentEnvironment]
			if envPolicy.Keys == nil {
				envPolicy.Keys = map[string]RotationRule{}
			}
			if _, ok := envPolicy.Keys[*currentKey]; !ok {
				envPolicy.Keys[*currentKey] = RotationRule{}
			}
			policy.Rotation.Environments[*currentEnvironment] = envPolicy
		}
		return
	}
	if indent >= 10 && *currentKey != "" {
		envPolicy := policy.Rotation.Environments[*currentEnvironment]
		rule := envPolicy.Keys[*currentKey]
		if updateRotationRule(&rule, trimmed) {
			envPolicy.Keys[*currentKey] = rule
			policy.Rotation.Environments[*currentEnvironment] = envPolicy
		}
	}
}

func updateRotationRule(rule *RotationRule, trimmed string) bool {
	name, value, ok := strings.Cut(trimmed, ":")
	if !ok {
		return false
	}
	switch strings.TrimSpace(name) {
	case "rotationAfterDays", "rotation_after_days":
		days, err := strconv.Atoi(readScalar(value))
		if err != nil || days <= 0 {
			return false
		}
		rule.RotationAfterDays = days
		return true
	default:
		return false
	}
}

func ensureRotationKeys(policy *Policy) {
	if policy.Rotation.Keys == nil {
		policy.Rotation.Keys = map[string]RotationRule{}
	}
}

func ensureRotationEnvironments(policy *Policy) {
	if policy.Rotation.Environments == nil {
		policy.Rotation.Environments = map[string]EnvironmentRotationPolicy{}
	}
}

func normalizePolicy(policy *Policy) {
	for key, rule := range policy.Rotation.Keys {
		if rule.RotationAfterDays <= 0 {
			delete(policy.Rotation.Keys, key)
		}
	}
	for env, envPolicy := range policy.Rotation.Environments {
		for key, rule := range envPolicy.Keys {
			if rule.RotationAfterDays <= 0 {
				delete(envPolicy.Keys, key)
			}
		}
		if len(envPolicy.Keys) == 0 {
			delete(policy.Rotation.Environments, env)
			continue
		}
		policy.Rotation.Environments[env] = envPolicy
	}
}

func sortedRotationKeys(rules map[string]RotationRule) []string {
	keys := make([]string, 0, len(rules))
	for key := range rules {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedEnvironmentNames(environments map[string]EnvironmentRotationPolicy) []string {
	names := make([]string, 0, len(environments))
	for name := range environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func stripComment(line string) string {
	inSingle := false
	inDouble := false
	for i, r := range line {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return line[:i]
			}
		}
	}
	return line
}

func leadingSpaces(value string) int {
	count := 0
	for _, r := range value {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}

func readScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func yamlScalar(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, ":#'\"\n\r\t ") {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return value
}
