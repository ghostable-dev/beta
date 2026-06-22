package store

import (
	"fmt"
	"os"
	"sort"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/security"
)

func (r Repository) Environments() []domain.Environment {
	envs := make([]domain.Environment, 0, len(r.Manifest.Environments))
	for name, env := range r.Manifest.Environments {
		env.Name = name
		if env.Type == "" {
			env.Type = environmentType(name)
		}
		envs = append(envs, env)
	}
	sort.Slice(envs, func(i, j int) bool {
		return envs[i].Name < envs[j].Name
	})
	return envs
}

func (r Repository) SaveManifest() error {
	return writeManifest(r.ManifestPath, r.Manifest)
}

func (r Repository) GenerateLayout(env string, keys []string) error {
	if err := r.requireEnvironment(env); err != nil {
		return err
	}
	ranks := make(map[string]int, len(keys))
	for index, key := range keys {
		if key == "" {
			continue
		}
		ranks[key] = index + 1
	}
	return r.writeLayout(env, ranks)
}

func (r Repository) AddLayoutKey(env string, key string) error {
	if err := r.requireEnvironment(env); err != nil {
		return err
	}
	if err := r.requireWrite(env); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	return r.addLayoutKey(env, key)
}

func (r Repository) CreateEnvironment(name string, envType string) (EnvironmentResult, error) {
	if err := r.requireOwner(); err != nil {
		return EnvironmentResult{}, err
	}
	if err := validateEnvironmentName(name); err != nil {
		return EnvironmentResult{}, err
	}
	if envType == "" {
		envType = environmentType(name)
	}
	if _, ok := r.Manifest.Environments[name]; ok {
		return EnvironmentResult{Environment: r.Manifest.Environments[name], Created: false}, nil
	}
	if err := r.requireEnvironmentStoragePathAvailable(name, ""); err != nil {
		return EnvironmentResult{}, err
	}

	env := domain.Environment{Name: name, Type: envType}
	r.Manifest.Environments[name] = env
	if err := writeManifest(r.ManifestPath, r.Manifest); err != nil {
		return EnvironmentResult{}, err
	}
	if err := r.ensureEnvironmentDirs(name); err != nil {
		return EnvironmentResult{}, err
	}
	if err := r.writeLayout(name, map[string]int{}); err != nil {
		return EnvironmentResult{}, err
	}
	policy, err := r.readPolicy()
	if err == nil {
		policy.Environments[name] = emptyEnvironmentPolicy()
		policy.UpdatedAt = security.Now()
		_ = r.signAndWritePolicy(policy)
	}
	device, err := r.localDeviceRecord()
	if err != nil {
		return EnvironmentResult{}, err
	}
	if err := r.createEnvironmentKey(name, device); err != nil {
		return EnvironmentResult{}, err
	}
	if err := r.recordEvent("environment.created", name, "", map[string]interface{}{"type": envType}); err != nil {
		return EnvironmentResult{}, err
	}

	return EnvironmentResult{Environment: env, Created: true}, nil
}

func (r Repository) DeleteEnvironment(name string) error {
	if err := r.requireOwner(); err != nil {
		return err
	}
	if err := r.requireEnvironment(name); err != nil {
		return err
	}
	if len(r.Manifest.Environments) <= 1 {
		return fmt.Errorf("cannot delete the last environment")
	}
	if err := ensureGhostableStatePath(r.environmentDir(name)); err != nil {
		return err
	}

	delete(r.Manifest.Environments, name)
	if err := writeManifest(r.ManifestPath, r.Manifest); err != nil {
		return err
	}
	if err := os.RemoveAll(r.environmentDir(name)); err != nil {
		return err
	}
	policy, err := r.readPolicy()
	if err == nil {
		delete(policy.Environments, name)
		policy.UpdatedAt = security.Now()
		_ = r.signAndWritePolicy(policy)
	}
	return r.recordEvent("environment.deleted", name, "", nil)
}

func (r Repository) RenameEnvironment(source string, target string, reason string) error {
	if err := r.requireOwner(); err != nil {
		return err
	}
	if err := r.requireEnvironment(source); err != nil {
		return err
	}
	if err := validateEnvironmentName(target); err != nil {
		return err
	}
	if _, exists := r.Manifest.Environments[target]; exists {
		return fmt.Errorf("environment %q already exists", target)
	}
	if err := r.requireEnvironmentStoragePathAvailable(target, source); err != nil {
		return err
	}
	if err := ensureGhostableStatePath(r.environmentDir(source)); err != nil {
		return err
	}
	if err := ensureGhostableStatePath(r.environmentDir(target)); err != nil {
		return err
	}

	env := r.Manifest.Environments[source]
	delete(r.Manifest.Environments, source)
	env.Name = target
	r.Manifest.Environments[target] = env

	if err := os.Rename(r.environmentDir(source), r.environmentDir(target)); err != nil {
		return err
	}

	values, err := r.ReadVariables(target)
	if err == nil {
		for key, variable := range values {
			if err := r.writeVariable(writeVariableInput{
				Environment: target,
				Key:         key,
				Value:       variable.Value,
				Note:        variable.Note,
				Existing:    true,
				Commented:   variable.Commented,
			}); err != nil {
				return err
			}
		}
	}

	if err := writeManifest(r.ManifestPath, r.Manifest); err != nil {
		return err
	}

	return r.recordEvent("environment.renamed", target, "", map[string]interface{}{
		"from":   source,
		"to":     target,
		"reason": reason,
	})
}

func (r Repository) requireEnvironmentStoragePathAvailable(name string, except string) error {
	targetPath := environmentPathSegment(name)
	names := sortedEnvironmentNames(r.Manifest.Environments)
	for _, existing := range names {
		if existing == except {
			continue
		}
		if environmentPathSegment(existing) == targetPath {
			return fmt.Errorf("environment %q conflicts with existing environment %q after storage path normalization", name, existing)
		}
	}
	return nil
}

func validateEnvironmentStoragePathUniqueness(environments map[string]domain.Environment) error {
	seen := map[string]string{}
	for _, name := range sortedEnvironmentNames(environments) {
		if err := validateEnvironmentPathSegment(name); err != nil {
			return err
		}
		path := environmentPathSegment(name)
		if existing, ok := seen[path]; ok {
			return fmt.Errorf("environment %q conflicts with existing environment %q after storage path normalization", name, existing)
		}
		seen[path] = name
	}
	return nil
}

func sortedEnvironmentNames(environments map[string]domain.Environment) []string {
	names := make([]string, 0, len(environments))
	for name := range environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
