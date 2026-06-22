package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/dotenv"
)

func (r Repository) PutVariables(env string, values map[string]string, options PutOptions) (PushResult, error) {
	if err := r.requireEnvironment(env); err != nil {
		return PushResult{}, err
	}
	if err := r.ensureEnvironmentDirs(env); err != nil {
		return PushResult{}, err
	}

	current, err := r.ReadVariables(env)
	if err != nil {
		return PushResult{}, err
	}

	result := PushResult{Environment: env}
	if err := r.writeIncomingVariables(env, values, current, &result); err != nil {
		return PushResult{}, err
	}

	if options.Sync {
		if err := r.deleteMissingVariables(env, values, current, options.Reason, &result); err != nil {
			return PushResult{}, err
		}
	}

	sortPushResult(&result)
	if err := r.recordEvent(pushEventAction(options.Sync), env, "", map[string]interface{}{
		"created": len(result.Created),
		"updated": len(result.Updated),
		"deleted": len(result.Deleted),
		"reason":  options.Reason,
	}); err != nil {
		return PushResult{}, err
	}

	return result, nil
}

func (r Repository) writeIncomingVariables(env string, incoming map[string]string, current map[string]domain.Variable, result *PushResult) error {
	for key, value := range incoming {
		if err := validateKey(key); err != nil {
			return err
		}

		existing, exists := current[key]
		result.addChangedVariable(key, exists, existing.Value != value)
		if exists && existing.Value == value {
			continue
		}

		note := ""
		commented := false
		vaporSecret := false
		if exists {
			note = existing.Note
			commented = existing.Commented
			vaporSecret = existing.VaporSecret
		}
		if err := r.writeVariable(writeVariableInput{
			Environment: env,
			Key:         key,
			Value:       value,
			Note:        note,
			Existing:    exists,
			Commented:   commented,
			VaporSecret: vaporSecret,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (result *PushResult) addChangedVariable(key string, exists bool, changed bool) {
	switch {
	case !exists:
		result.Created = append(result.Created, key)
	case changed:
		result.Updated = append(result.Updated, key)
	default:
		result.Unchanged = append(result.Unchanged, key)
	}
}

func (r Repository) deleteMissingVariables(env string, incoming map[string]string, current map[string]domain.Variable, reason string, result *PushResult) error {
	for key := range current {
		if _, ok := incoming[key]; ok {
			continue
		}
		if err := r.DeleteVariable(env, key, reason); err != nil {
			return err
		}
		result.Deleted = append(result.Deleted, key)
	}
	return nil
}

func sortPushResult(result *PushResult) {
	sort.Strings(result.Created)
	sort.Strings(result.Updated)
	sort.Strings(result.Deleted)
	sort.Strings(result.Unchanged)
}

func pushEventAction(sync bool) string {
	if sync {
		return "env.synced"
	}
	return "env.pushed"
}

func (r Repository) ReadVariables(env string) (map[string]domain.Variable, error) {
	if err := r.requireEnvironment(env); err != nil {
		return nil, err
	}

	records, err := r.readEnvironmentValueRecords(env)
	if err != nil {
		return nil, err
	}

	variables := make(map[string]domain.Variable)
	for _, record := range records {
		value, note, err := r.decryptRecord(record)
		if err != nil {
			return nil, err
		}
		variables[record.Key] = domain.Variable{
			Key:         record.Key,
			Value:       value,
			HasValue:    true,
			Sensitive:   record.Schema == domain.ValueSchema || record.Sensitive,
			Commented:   record.Secret.IsCommented,
			VaporSecret: record.Secret.IsVaporSecret != nil && *record.Secret.IsVaporSecret,
			Note:        note,
			UpdatedAt:   record.UpdatedAt,
		}
	}

	return variables, nil
}

type VariableMetadata struct {
	Environment       string `json:"environment"`
	Key               string `json:"key"`
	Version           int    `json:"version"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
	UpdatedByDeviceID string `json:"updatedByDeviceId,omitempty"`
	VaporSecret       bool   `json:"vaporSecret,omitempty"`
	Commented         bool   `json:"commented,omitempty"`
	ValidSignature    bool   `json:"validSignature"`
	SignatureError    string `json:"signatureError,omitempty"`
}

func (r Repository) ReadVariableMetadata(env string) ([]VariableMetadata, error) {
	if err := r.requireEnvironment(env); err != nil {
		return nil, err
	}

	records, err := r.readEnvironmentValueRecords(env)
	if err != nil {
		return nil, err
	}

	variables := make([]VariableMetadata, 0, len(records))
	for _, record := range records {
		signatureErr := r.verifyValueRecordMetadata(record)
		metadata := VariableMetadata{
			Environment:       record.Environment,
			Key:               record.Key,
			Version:           record.Version,
			UpdatedAt:         record.UpdatedAt,
			UpdatedByDeviceID: record.UpdatedByDeviceID,
			VaporSecret:       record.Secret.IsVaporSecret != nil && *record.Secret.IsVaporSecret,
			Commented:         record.Secret.IsCommented,
			ValidSignature:    signatureErr == nil,
		}
		if signatureErr != nil {
			metadata.SignatureError = signatureErr.Error()
		}
		variables = append(variables, metadata)
	}

	sort.Slice(variables, func(i, j int) bool {
		return variables[i].Key < variables[j].Key
	})
	return variables, nil
}

func (r Repository) readEnvironmentValueRecords(env string) ([]domain.ValueRecord, error) {
	valuesDir := r.valuesDir(env)
	entries, err := os.ReadDir(valuesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []domain.ValueRecord{}, nil
		}
		return nil, err
	}

	seen := map[string]string{}
	records := make([]domain.ValueRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		record, err := r.readValueRecord(filepath.Join(valuesDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if existingFile, exists := seen[record.Key]; exists {
			return nil, fmt.Errorf("duplicate value record for %s in %s and %s", record.Key, existingFile, entry.Name())
		}
		seen[record.Key] = entry.Name()
		if record.Environment != env {
			return nil, fmt.Errorf("value %s belongs to environment %s, not %s", record.Key, record.Environment, env)
		}
		expectedName := filepath.Base(r.valuePath(env, record.Key))
		if entry.Name() != expectedName {
			return nil, fmt.Errorf("value %s is stored in %s but expected %s", record.Key, entry.Name(), expectedName)
		}
		records = append(records, record)
	}
	return records, nil
}

func (r Repository) GetVariable(env string, key string) (domain.Variable, bool, error) {
	values, err := r.ReadVariables(env)
	if err != nil {
		return domain.Variable{}, false, err
	}
	value, ok := values[key]
	return value, ok, nil
}

type VariableWriteOptions struct {
	Reason      string
	Commented   *bool
	VaporSecret *bool
}

func (r Repository) SetVariable(env string, key string, value string, reason string) error {
	return r.SetVariableWithOptions(env, key, value, VariableWriteOptions{Reason: reason})
}

func (r Repository) SetVariableWithOptions(env string, key string, value string, options VariableWriteOptions) error {
	if err := r.requireEnvironment(env); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	current, exists, err := r.GetVariable(env, key)
	if err != nil {
		return err
	}
	note := ""
	commented := false
	vaporSecret := false
	if exists {
		note = current.Note
		commented = current.Commented
		vaporSecret = current.VaporSecret
	}
	if options.Commented != nil {
		commented = *options.Commented
	}
	if options.VaporSecret != nil {
		vaporSecret = *options.VaporSecret
	}
	if err := r.writeVariable(writeVariableInput{
		Environment: env,
		Key:         key,
		Value:       value,
		Note:        note,
		Existing:    exists,
		Commented:   commented,
		VaporSecret: vaporSecret,
	}); err != nil {
		return err
	}
	action := "variable.created"
	if exists {
		action = "variable.updated"
	}
	return r.recordEvent(action, env, key, map[string]interface{}{"reason": options.Reason})
}

func (r Repository) SetVariableCommented(env string, key string, value string, commented bool, reason string) error {
	return r.SetVariableWithOptions(env, key, value, VariableWriteOptions{
		Reason:    reason,
		Commented: &commented,
	})
}

func (r Repository) SetVariableVaporSecret(env string, key string, vaporSecret bool, reason string) error {
	current, exists, err := r.GetVariable(env, key)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("variable %q was not found in %s", key, env)
	}
	return r.SetVariableWithOptions(env, key, current.Value, VariableWriteOptions{
		Reason:      reason,
		VaporSecret: &vaporSecret,
	})
}

func (r Repository) SetVariableNote(env string, key string, note string) error {
	current, exists, err := r.GetVariable(env, key)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("variable %q was not found in %s", key, env)
	}
	if err := r.writeVariable(writeVariableInput{
		Environment: env,
		Key:         key,
		Value:       current.Value,
		Note:        note,
		Existing:    true,
		Commented:   current.Commented,
		VaporSecret: current.VaporSecret,
	}); err != nil {
		return err
	}
	return r.recordEvent("variable.context.updated", env, key, nil)
}

func (r Repository) DeleteVariable(env string, key string, reason string) error {
	if err := r.requireEnvironment(env); err != nil {
		return err
	}
	if err := r.requireWrite(env); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	path := r.valuePath(env, key)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("variable %q was not found in %s", key, env)
		}
		return err
	}
	_ = r.removeLayoutKey(env, key)
	return r.recordEvent("variable.deleted", env, key, map[string]interface{}{"reason": reason})
}

func (r Repository) Pull(env string, options PullOptions) (PullResult, string, error) {
	variables, err := r.ReadVariables(env)
	if err != nil {
		return PullResult{}, "", err
	}

	values := make(map[string]string)
	order := r.layoutOrder(env)
	only := stringSet(options.Only)
	for key, variable := range variables {
		if len(only) > 0 && !only[key] {
			continue
		}
		values[key] = variable.Value
	}

	if options.File == "" {
		options.File = ".env." + env
		if env == "local" || env == "default" {
			options.File = ".env"
		}
	}
	path, err := r.resolveProjectOutputPath(options.File)
	if err != nil {
		return PullResult{}, "", err
	}
	existing := ""
	if content, err := os.ReadFile(path); err == nil {
		existing = string(content)
	} else if !os.IsNotExist(err) {
		return PullResult{}, "", err
	}

	next, err := dotenv.Merge(existing, values, order, options.Replace)
	if err != nil {
		return PullResult{}, "", err
	}

	result := PullResult{
		Environment: env,
		File:        options.File,
		DryRun:      options.DryRun,
		Written:     len(values),
	}
	for _, key := range sortedKeys(values) {
		result.Variables = append(result.Variables, domain.Variable{
			Key:       key,
			Value:     valueForOutput(values[key], options.ShowValue),
			HasValue:  options.ShowValue,
			Sensitive: true,
			Commented: variables[key].Commented,
		})
	}

	if options.DryRun {
		return result, next, nil
	}

	if existing != "" && options.Backup {
		backupPath := fmt.Sprintf("%s.ghostable-backup-%s", path, time.Now().UTC().Format("20060102T150405Z"))
		if err := writeFileAtomic(backupPath, []byte(existing), 0o600); err != nil {
			return PullResult{}, "", err
		}
		result.BackupFile = backupPath
	}

	if err := writeFileAtomic(path, []byte(next), 0o600); err != nil {
		return PullResult{}, "", err
	}

	if options.SkipEvent {
		return result, next, nil
	}

	return result, next, r.recordEvent("env.pulled", env, "", map[string]interface{}{"file": options.File})
}

func (r Repository) Diff(env string, filePath string, only []string, showValues bool) (domain.EnvDiff, error) {
	if filePath == "" {
		filePath = ".env." + env
		if env == "local" || env == "default" {
			filePath = ".env"
		}
	}

	localValues := map[string]string{}
	content, err := os.ReadFile(r.resolveProjectPath(filePath))
	if err == nil {
		localValues, err = dotenv.ParseString(string(content))
		if err != nil {
			return domain.EnvDiff{}, err
		}
	} else if !os.IsNotExist(err) {
		return domain.EnvDiff{}, err
	}

	storedVariables, err := r.ReadVariables(env)
	if err != nil {
		return domain.EnvDiff{}, err
	}

	onlySet := stringSet(only)
	keys := make(map[string]bool)
	for key := range localValues {
		if len(onlySet) == 0 || onlySet[key] {
			keys[key] = true
		}
	}
	for key := range storedVariables {
		if len(onlySet) == 0 || onlySet[key] {
			keys[key] = true
		}
	}

	diff := domain.EnvDiff{Environment: env, File: filePath}
	for _, key := range sortedSet(keys) {
		localValue, localOK := localValues[key]
		storedVariable, storedOK := storedVariables[key]
		switch {
		case localOK && !storedOK:
			diff.Added = append(diff.Added, domain.DiffEntry{Key: key, LocalValue: valueForOutput(localValue, showValues)})
		case !localOK && storedOK:
			diff.Removed = append(diff.Removed, domain.DiffEntry{Key: key, StoredValue: valueForOutput(storedVariable.Value, showValues)})
		case localOK && storedOK && localValue != storedVariable.Value:
			diff.Changed = append(diff.Changed, domain.DiffEntry{
				Key:         key,
				LocalValue:  valueForOutput(localValue, showValues),
				StoredValue: valueForOutput(storedVariable.Value, showValues),
			})
		default:
			diff.Unchanged = append(diff.Unchanged, key)
		}
	}

	diff.Summary = domain.DiffSummary{
		Added:     len(diff.Added),
		Changed:   len(diff.Changed),
		Removed:   len(diff.Removed),
		Unchanged: len(diff.Unchanged),
	}
	return diff, nil
}
