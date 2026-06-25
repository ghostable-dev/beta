package validation

import (
	"bufio"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ghostable-dev/beta/internal/domain"
)

type Rule struct {
	Name     string `json:"name"`
	Argument string `json:"argument,omitempty"`
}

type Result struct {
	Environment string            `json:"environment"`
	File        string            `json:"file,omitempty"`
	Passed      bool              `json:"passed"`
	Errors      []ValidationError `json:"errors"`
	Warnings    []string          `json:"warnings,omitempty"`
}

type ValidationError struct {
	Key     string `json:"key"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type VariableReader interface {
	ReadVariables(env string) (map[string]domain.Variable, error)
}

func Validate(root string, reader VariableReader, env string, values map[string]string, file string) (Result, error) {
	rules, warnings, err := LoadRules(root, env)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		Environment: env,
		File:        file,
		Errors:      []ValidationError{},
		Warnings:    warnings,
	}
	for key, keyRules := range rules {
		value, exists := values[key]
		nullable := hasRule(keyRules, "nullable")
		for _, rule := range keyRules {
			if rule.Name == "nullable" {
				continue
			}
			if !exists || value == "" {
				if rule.Name == "required" {
					result.Errors = append(result.Errors, errorFor(key, rule, "is required"))
				}
				if nullable || rule.Name != "required" {
					continue
				}
			}

			if err := evaluateRule(reader, env, key, value, rule); err != nil {
				result.Errors = append(result.Errors, errorFor(key, rule, err.Error()))
			}
		}
	}

	result.Passed = len(result.Errors) == 0
	return result, nil
}

func LoadRules(root string, env string) (map[string][]Rule, []string, error) {
	rules := map[string][]Rule{}
	warnings := []string{}
	globalPath := filepath.Join(root, ".ghostable", "schema.yaml")
	if _, err := os.Stat(globalPath); err == nil {
		loaded, err := ParseFile(globalPath)
		if err != nil {
			return nil, nil, err
		}
		mergeRules(rules, loaded)
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}

	envPath := filepath.Join(root, ".ghostable", "schemas", env+".yaml")
	if _, err := os.Stat(envPath); err == nil {
		loaded, err := ParseFile(envPath)
		if err != nil {
			return nil, nil, err
		}
		mergeRules(rules, loaded)
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}

	if len(rules) == 0 {
		warnings = append(warnings, "No schema rules found.")
	}
	return rules, warnings, nil
}

func ParseFile(path string) (map[string][]Rule, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	rules := map[string][]Rule{}
	scanner := bufio.NewScanner(file)
	currentKey := ""
	for scanner.Scan() {
		raw := scanner.Text()
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingSpaces(line)
		trimmed := strings.TrimSpace(line)

		if indent == 0 && strings.HasSuffix(trimmed, ":") {
			currentKey = strings.TrimSuffix(trimmed, ":")
			currentKey = readScalar(currentKey)
			if currentKey != "" {
				rules[currentKey] = nil
			}
			continue
		}

		if currentKey != "" && indent >= 2 && strings.HasPrefix(trimmed, "- ") {
			rules[currentKey] = append(rules[currentKey], parseRule(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func AppendRule(path string, key string, rule Rule) error {
	rules := map[string][]Rule{}
	if _, err := os.Stat(path); err == nil {
		loaded, err := ParseFile(path)
		if err != nil {
			return err
		}
		rules = loaded
	} else if !os.IsNotExist(err) {
		return err
	}
	rules[key] = append(rules[key], rule)
	return WriteRules(path, rules)
}

func WriteRules(path string, rules map[string][]Rule) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(rules))
	for key := range rules {
		keys = append(keys, key)
	}
	sortStrings(keys)

	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(yamlScalar(key))
		builder.WriteString(":\n")
		for _, rule := range rules[key] {
			builder.WriteString("  - ")
			builder.WriteString(formatRule(rule))
			builder.WriteString("\n")
		}
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

func parseRule(value string) Rule {
	value = readScalar(value)
	name, arg, ok := strings.Cut(value, ":")
	if !ok {
		return Rule{Name: strings.TrimSpace(value)}
	}
	return Rule{Name: strings.TrimSpace(name), Argument: strings.TrimSpace(arg)}
}

func formatRule(rule Rule) string {
	if rule.Argument == "" {
		return yamlScalar(rule.Name)
	}
	return yamlScalar(rule.Name + ":" + rule.Argument)
}

func evaluateRule(reader VariableReader, env string, key string, value string, rule Rule) error {
	switch rule.Name {
	case "required":
		if value == "" {
			return fmt.Errorf("is required")
		}
	case "string":
		return nil
	case "integer":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("must be an integer")
		}
	case "numeric":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("must be numeric")
		}
	case "boolean":
		switch strings.ToLower(value) {
		case "true", "false", "1", "0", "yes", "no":
			return nil
		default:
			return fmt.Errorf("must be boolean")
		}
	case "email":
		if _, err := mail.ParseAddress(value); err != nil {
			return fmt.Errorf("must be a valid email address")
		}
	case "url":
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("must be a valid URL")
		}
	case "starts_with":
		if !strings.HasPrefix(value, rule.Argument) {
			return fmt.Errorf("must start with %q", rule.Argument)
		}
	case "ends_with":
		if !strings.HasSuffix(value, rule.Argument) {
			return fmt.Errorf("must end with %q", rule.Argument)
		}
	case "regex":
		expr, err := regexp.Compile(rule.Argument)
		if err != nil {
			return fmt.Errorf("has invalid regex rule")
		}
		if !expr.MatchString(value) {
			return fmt.Errorf("does not match %q", rule.Argument)
		}
	case "in":
		allowed := splitCSV(rule.Argument)
		for _, item := range allowed {
			if value == item {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s", strings.Join(allowed, ", "))
	case "min":
		return checkMin(value, rule.Argument)
	case "max":
		return checkMax(value, rule.Argument)
	case "different_from":
		if rule.Argument == "" || rule.Argument == env {
			return nil
		}
		variables, err := reader.ReadVariables(rule.Argument)
		if err != nil {
			return fmt.Errorf("could not compare with %s", rule.Argument)
		}
		other, ok := variables[key]
		if ok && other.Value == value {
			return fmt.Errorf("must be different from %s", rule.Argument)
		}
	}
	return nil
}

func checkMin(value string, arg string) error {
	min, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		return fmt.Errorf("has invalid min rule")
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		if number < min {
			return fmt.Errorf("must be at least %s", arg)
		}
		return nil
	}
	if float64(len(value)) < min {
		return fmt.Errorf("must be at least %s characters", arg)
	}
	return nil
}

func checkMax(value string, arg string) error {
	max, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		return fmt.Errorf("has invalid max rule")
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		if number > max {
			return fmt.Errorf("must be at most %s", arg)
		}
		return nil
	}
	if float64(len(value)) > max {
		return fmt.Errorf("must be at most %s characters", arg)
	}
	return nil
}

func hasRule(rules []Rule, name string) bool {
	for _, rule := range rules {
		if rule.Name == name {
			return true
		}
	}
	return false
}

func errorFor(key string, rule Rule, message string) ValidationError {
	return ValidationError{
		Key:     key,
		Rule:    formatRule(rule),
		Message: message,
	}
}

func mergeRules(target map[string][]Rule, source map[string][]Rule) {
	for key, rules := range source {
		target[key] = append(target[key], rules...)
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
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
			if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ') {
				return line[:i]
			}
		}
	}
	return line
}

func readScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		}
		if value[0] == '"' && value[len(value)-1] == '"' {
			return strings.ReplaceAll(value[1:len(value)-1], `\"`, `"`)
		}
	}
	return value
}

func yamlScalar(value string) string {
	if value == "" {
		return `""`
	}
	plain := true
	for _, r := range value {
		if !(r == '-' || r == '_' || r == '.' || r == '/' || r == '*' || r == '@' || r == ':' || r == ' ' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			plain = false
			break
		}
	}
	if plain && !strings.Contains(value, ": ") && !strings.HasPrefix(value, "- ") && !strings.Contains(value, "#") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func leadingSpaces(line string) int {
	count := 0
	for _, r := range line {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}

func sortStrings(values []string) {
	sort.Strings(values)
}
