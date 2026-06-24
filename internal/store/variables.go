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
	inputs := make(map[string]VariablePutInput, len(values))
	for key, value := range values {
		inputs[key] = VariablePutInput{Value: value}
	}
	return r.PutVariablesWithMetadata(env, inputs, options)
}

type VariablePutInput struct {
	Value     string
	Commented *bool
}

func (r Repository) PutVariablesWithMetadata(env string, inputs map[string]VariablePutInput, options PutOptions) (PushResult, error) {
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
	if err := r.writeIncomingVariables(env, inputs, current, options.Reason, &result); err != nil {
		return PushResult{}, err
	}

	if options.Sync {
		if err := r.deleteMissingVariables(env, inputs, current, options.Reason, &result); err != nil {
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

func (r Repository) writeIncomingVariables(env string, incoming map[string]VariablePutInput, current map[string]domain.Variable, reason string, result *PushResult) error {
	for key, input := range incoming {
		if err := validateKey(key); err != nil {
			return err
		}

		existing, exists := current[key]
		commented := false
		if exists {
			commented = existing.Commented
		}
		if input.Commented != nil {
			commented = *input.Commented
		}
		valueChanged := !exists || existing.Value != input.Value
		commentedChanged := exists && existing.Commented != commented
		metadataChanged := commentedChanged

		result.addChangedVariable(key, exists, valueChanged || metadataChanged)
		if exists && !valueChanged && !metadataChanged {
			continue
		}
		if exists && !valueChanged {
			status := domain.KeyStatusActive
			if commented {
				status = domain.KeyStatusCommented
			}
			if err := r.updateKeyMetadata(env, key, func(record *domain.EnvironmentKeyMetadataRecord) error {
				record.Status = status
				return nil
			}); err != nil {
				return err
			}
			continue
		}
		if err := r.writeVariableWithMetadata(writeVariableWithMetadataInput{
			Environment: env,
			Key:         key,
			Value:       input.Value,
			Existing:    exists,
			Reason:      reason,
			Commented:   commented,
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

func (r Repository) deleteMissingVariables(env string, incoming map[string]VariablePutInput, current map[string]domain.Variable, reason string, result *PushResult) error {
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
	keyMetadata, err := r.keyMetadataByKey(env)
	if err != nil {
		return nil, err
	}

	variables := make(map[string]domain.Variable)
	for _, record := range records {
		value, _, err := r.decryptRecord(record)
		if err != nil {
			return nil, err
		}
		metadata, ok := keyMetadata[record.Key]
		if !ok {
			return nil, fmt.Errorf("key metadata for %s was not found in %s", record.Key, env)
		}
		if err := r.verifyKeyMetadata(metadata); err != nil {
			return nil, err
		}
		note, err := r.decryptKeyMetadataNote(metadata)
		if err != nil {
			return nil, err
		}
		variables[record.Key] = domain.Variable{
			Key:         record.Key,
			Value:       value,
			HasValue:    true,
			Sensitive:   record.Schema == domain.ValueSchema || record.Sensitive,
			Commented:   metadata.Status == domain.KeyStatusCommented,
			Note:        note,
			UpdatedAt:   record.UpdatedAt,
			Annotations: cloneKeyAnnotations(metadata.Annotations),
		}
	}

	return variables, nil
}

type VariableMetadata struct {
	Environment       string                `json:"environment"`
	Key               string                `json:"key"`
	Version           int                   `json:"version"`
	UpdatedAt         string                `json:"updatedAt,omitempty"`
	UpdatedByDeviceID string                `json:"updatedByDeviceId,omitempty"`
	Commented         bool                  `json:"commented,omitempty"`
	ValidSignature    bool                  `json:"validSignature"`
	SignatureError    string                `json:"signatureError,omitempty"`
	Annotations       domain.KeyAnnotations `json:"annotations,omitempty"`
}

func (r Repository) ReadVariableMetadata(env string) ([]VariableMetadata, error) {
	if err := r.requireEnvironment(env); err != nil {
		return nil, err
	}

	records, err := r.readEnvironmentValueRecords(env)
	if err != nil {
		return nil, err
	}
	keyMetadata, err := r.keyMetadataByKey(env)
	if err != nil {
		return nil, err
	}

	variables := make([]VariableMetadata, 0, len(records))
	for _, record := range records {
		signatureErr := r.verifyValueRecordMetadata(record)
		keyRecord, ok := keyMetadata[record.Key]
		if !ok && signatureErr == nil {
			signatureErr = fmt.Errorf("key metadata for %s was not found in %s", record.Key, env)
		}
		if ok {
			if err := r.verifyKeyMetadata(keyRecord); err != nil && signatureErr == nil {
				signatureErr = err
			}
		}
		metadata := VariableMetadata{
			Environment:       record.Environment,
			Key:               record.Key,
			Version:           record.Version,
			UpdatedAt:         record.UpdatedAt,
			UpdatedByDeviceID: record.UpdatedByDeviceID,
			Commented:         ok && keyRecord.Status == domain.KeyStatusCommented,
			ValidSignature:    signatureErr == nil,
			Annotations:       cloneKeyAnnotations(keyRecord.Annotations),
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
	Reason    string
	Commented *bool
}

type writeVariableWithMetadataInput struct {
	Environment string
	Key         string
	Value       string
	Existing    bool
	Reason      string
	Commented   bool
}

func (r Repository) writeVariableWithMetadata(input writeVariableWithMetadataInput) error {
	if err := r.writeVariable(writeVariableInput{
		Environment: input.Environment,
		Key:         input.Key,
		Value:       input.Value,
		Existing:    input.Existing,
		Reason:      input.Reason,
	}); err != nil {
		return err
	}

	status := domain.KeyStatusActive
	if input.Commented {
		status = domain.KeyStatusCommented
	}
	return r.updateKeyMetadata(input.Environment, input.Key, func(record *domain.EnvironmentKeyMetadataRecord) error {
		record.Status = status
		return nil
	})
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
	commented := false
	if exists {
		commented = current.Commented
	}
	if options.Commented != nil {
		commented = *options.Commented
	}
	if err := r.writeVariableWithMetadata(writeVariableWithMetadataInput{
		Environment: env,
		Key:         key,
		Value:       value,
		Existing:    exists,
		Reason:      options.Reason,
		Commented:   commented,
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

func (r Repository) SetVariableNote(env string, key string, note string) error {
	_, exists, err := r.GetVariable(env, key)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("variable %q was not found in %s", key, env)
	}
	encryptedNote, err := r.encryptKeyMetadataNote(env, key, note)
	if err != nil {
		return err
	}
	if err := r.updateKeyMetadata(env, key, func(record *domain.EnvironmentKeyMetadataRecord) error {
		record.EncryptedNote = encryptedNote
		return nil
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
	_ = r.removeKeyMetadata(env, key)
	return r.recordEvent("variable.deleted", env, key, map[string]interface{}{"reason": reason})
}

func (r Repository) Pull(env string, options PullOptions) (PullResult, string, error) {
	variables, err := r.ReadVariables(env)
	if err != nil {
		return PullResult{}, "", err
	}

	values := make(map[string]string)
	order, err := r.keyMetadataOrder(env)
	if err != nil {
		return PullResult{}, "", err
	}
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
	storedValues := valuesFromVariables(storedVariables)

	return buildEnvDiff(domain.EnvDiff{
		Environment:       env,
		TargetEnvironment: env,
		File:              filePath,
	}, localValues, storedValues, only, showValues), nil
}

func (r Repository) DiffEnvironments(source string, target string, only []string, showValues bool) (domain.EnvDiff, error) {
	sourceVariables, err := r.ReadVariables(source)
	if err != nil {
		return domain.EnvDiff{}, err
	}
	targetVariables, err := r.ReadVariables(target)
	if err != nil {
		return domain.EnvDiff{}, err
	}

	return buildEnvDiff(domain.EnvDiff{
		Environment:       target,
		SourceEnvironment: source,
		TargetEnvironment: target,
	}, valuesFromVariables(sourceVariables), valuesFromVariables(targetVariables), only, showValues), nil
}

func buildEnvDiff(diff domain.EnvDiff, sourceValues map[string]string, targetValues map[string]string, only []string, showValues bool) domain.EnvDiff {
	onlySet := stringSet(only)
	keys := make(map[string]bool)
	for key := range sourceValues {
		if len(onlySet) == 0 || onlySet[key] {
			keys[key] = true
		}
	}
	for key := range targetValues {
		if len(onlySet) == 0 || onlySet[key] {
			keys[key] = true
		}
	}

	for _, key := range sortedSet(keys) {
		sourceValue, sourceOK := sourceValues[key]
		targetValue, targetOK := targetValues[key]
		switch {
		case sourceOK && !targetOK:
			value := valueForOutput(sourceValue, showValues)
			diff.Added = append(diff.Added, domain.DiffEntry{Key: key, LocalValue: value, SourceValue: value})
		case !sourceOK && targetOK:
			value := valueForOutput(targetValue, showValues)
			diff.Removed = append(diff.Removed, domain.DiffEntry{Key: key, StoredValue: value, TargetValue: value})
		case sourceOK && targetOK && sourceValue != targetValue:
			sourceOutput := valueForOutput(sourceValue, showValues)
			targetOutput := valueForOutput(targetValue, showValues)
			diff.Changed = append(diff.Changed, domain.DiffEntry{
				Key:         key,
				LocalValue:  sourceOutput,
				StoredValue: targetOutput,
				SourceValue: sourceOutput,
				TargetValue: targetOutput,
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
	return diff
}

func valuesFromVariables(variables map[string]domain.Variable) map[string]string {
	values := make(map[string]string, len(variables))
	for key, variable := range variables {
		values[key] = variable.Value
	}
	return values
}
