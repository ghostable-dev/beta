package store

import (
	"fmt"
	"os"
	"path/filepath"

	gcrypto "github.com/ghostable-dev/beta/internal/crypto"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/manifest"
	"github.com/ghostable-dev/beta/internal/security"
)

func Setup(root string, options SetupOptions) (Repository, bool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Repository{}, false, err
	}

	manifestPath := filepath.Join(root, ".ghostable", "ghostable.yaml")
	if err := ensureSetupManifestCanBeWritten(manifestPath, options.Force); err != nil {
		return Repository{}, false, err
	}

	options = normalizeSetupOptions(root, options)
	projectID, err := randomID()
	if err != nil {
		return Repository{}, false, err
	}
	project := projectManifestFromSetup(projectID, options)
	if err := validateEnvironmentStoragePathUniqueness(project.Environments); err != nil {
		return Repository{}, false, err
	}

	if err := os.MkdirAll(filepath.Join(root, ".ghostable"), 0o755); err != nil {
		return Repository{}, false, err
	}
	if err := writeManifest(manifestPath, project); err != nil {
		return Repository{}, false, err
	}

	repo, device, err := setupRepository(root, manifestPath, project, options)
	if err != nil {
		return Repository{}, false, err
	}
	if err := repo.writeDevice(device); err != nil {
		return Repository{}, false, err
	}

	policy := newOwnerPolicy(project.ID, repo.DeviceID())
	if err := repo.initializeSetupEnvironments(device, policy); err != nil {
		return Repository{}, false, err
	}
	if err := repo.signAndWritePolicy(policy); err != nil {
		return Repository{}, false, err
	}

	return repo, true, nil
}

func ensureSetupManifestCanBeWritten(manifestPath string, force bool) error {
	if _, err := os.Stat(manifestPath); err == nil && !force {
		return fmt.Errorf("Ghostable is already initialized; pass --force to replace the local manifest")
	}
	return nil
}

func normalizeSetupOptions(root string, options SetupOptions) SetupOptions {
	if options.Name == "" {
		options.Name = filepath.Base(root)
	}
	if options.DeviceName == "" {
		options.DeviceName = domain.DefaultDeviceName
	}
	if options.Platform == "" {
		options.Platform = platformLabel()
	}
	if options.ActivityMode == "" {
		options.ActivityMode = domain.DefaultActivity
	}
	if len(options.Environments) == 0 {
		options.Environments = []domain.Environment{{Name: domain.DefaultEnvName, Type: domain.DefaultEnvType}}
	}
	return options
}

func projectManifestFromSetup(projectID string, options SetupOptions) domain.ProjectManifest {
	project := manifest.New(projectID, options.Name, options.Environments)
	project.Language = options.Language
	project.Framework = options.Framework
	project.PackageManager = options.PackageManager
	project.DeployTarget = options.DeployTarget
	project.ActivityMode = options.ActivityMode
	return project
}

func setupRepository(root string, manifestPath string, project domain.ProjectManifest, options SetupOptions) (Repository, domain.DeviceRecord, error) {
	identityStore, err := security.NewIdentityStore()
	if err != nil {
		return Repository{}, domain.DeviceRecord{}, err
	}
	identity, device, err := security.NewDeviceIdentity(project.ID, options.DeviceName, options.Platform)
	if err != nil {
		return Repository{}, domain.DeviceRecord{}, err
	}
	identity.TrustedPolicySigners = trustedPolicySigners(identity.DeviceID)
	if err := identityStore.Save(identity); err != nil {
		return Repository{}, domain.DeviceRecord{}, err
	}
	if err := identityStore.RegisterProjectIdentity(identity, project.Name, root); err != nil {
		return Repository{}, domain.DeviceRecord{}, err
	}

	return Repository{
		Root:          root,
		ManifestPath:  manifestPath,
		Manifest:      project,
		Identity:      identity,
		identityStore: identityStore,
		identityPath:  identityStore.Path(project.ID),
	}, device, nil
}

func newOwnerPolicy(projectID string, deviceID string) domain.Policy {
	return domain.Policy{
		Schema:       domain.PolicySchema,
		ProjectID:    projectID,
		Version:      1,
		UpdatedAt:    security.Now(),
		Owners:       []string{deviceID},
		Environments: make(map[string]domain.EnvironmentPolicy),
		DeviceID:     deviceID,
	}
}

func emptyEnvironmentPolicy() domain.EnvironmentPolicy {
	return domain.EnvironmentPolicy{
		Readers:  []string{},
		Writers:  []string{},
		Grantors: []string{},
	}
}

func (r Repository) initializeSetupEnvironments(device domain.DeviceRecord, policy domain.Policy) error {
	for _, env := range r.Environments() {
		if err := r.ensureEnvironmentDirs(env.Name); err != nil {
			return err
		}
		if err := r.writeLayout(env.Name, map[string]int{}); err != nil {
			return err
		}
		policy.Environments[env.Name] = emptyEnvironmentPolicy()
		if err := r.createEnvironmentKey(env.Name, device); err != nil {
			return err
		}
		if err := r.recordEvent("environment.created", env.Name, "", map[string]interface{}{"createdBy": "setup"}); err != nil {
			return err
		}
	}
	return nil
}

func Open(start string) (Repository, error) {
	project, err := openProjectManifest(start)
	if err != nil {
		return Repository{}, err
	}

	identityStore, err := security.NewIdentityStore()
	if err != nil {
		return Repository{}, err
	}
	identity, loadedFromToken, err := loadAutomationCredentialIdentity(project.manifest.ID)
	identityPath := automationCredentialEnvironmentVariable
	if err != nil {
		return Repository{}, err
	}
	if !loadedFromToken {
		identity, err = loadLocalIdentity(identityStore, project.manifest.ID)
		if err != nil {
			return Repository{}, err
		}
		identityPath = identityStore.Path(project.manifest.ID)
		// Backfill local metadata for identities created before the registry existed.
		_ = identityStore.RegisterProjectIdentity(identity, project.manifest.Name, project.root)
	}

	return Repository{
		Root:          project.root,
		ManifestPath:  project.manifestPath,
		Manifest:      project.manifest,
		Identity:      identity,
		identityStore: identityStore,
		identityPath:  identityPath,
		legacyKey:     loadLegacyKey(project.manifest.ID),
	}, nil
}

func OpenProject(start string) (Repository, error) {
	project, err := openProjectManifest(start)
	if err != nil {
		return Repository{}, err
	}

	identityStore, err := security.NewIdentityStore()
	if err != nil {
		return Repository{}, err
	}

	return Repository{
		Root:          project.root,
		ManifestPath:  project.manifestPath,
		Manifest:      project.manifest,
		identityStore: identityStore,
	}, nil
}

type openedProjectManifest struct {
	root         string
	manifestPath string
	manifest     domain.ProjectManifest
}

func openProjectManifest(start string) (openedProjectManifest, error) {
	root, manifestPath, err := FindRoot(start)
	if err != nil {
		return openedProjectManifest{}, err
	}
	if err := ensureGhostableStatePath(manifestPath); err != nil {
		return openedProjectManifest{}, err
	}

	file, err := os.Open(manifestPath)
	if err != nil {
		return openedProjectManifest{}, err
	}
	defer file.Close()

	project, err := manifest.Read(file)
	if err != nil {
		return openedProjectManifest{}, err
	}
	if project.ID == "" {
		return openedProjectManifest{}, fmt.Errorf("Ghostable manifest is missing an id")
	}
	if err := validateEnvironmentStoragePathUniqueness(project.Environments); err != nil {
		return openedProjectManifest{}, err
	}

	return openedProjectManifest{
		root:         root,
		manifestPath: manifestPath,
		manifest:     project,
	}, nil
}

func loadLocalIdentity(identityStore security.IdentityStore, projectID string) (domain.LocalIdentityRecord, error) {
	identity, err := identityStore.Load(projectID)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.LocalIdentityRecord{}, fmt.Errorf("this device has no local Ghostable identity for project %s; run `ghostable device join` or `ghostable setup`", projectID)
		}
		return domain.LocalIdentityRecord{}, err
	}
	return identity, nil
}

func loadLegacyKey(projectID string) []byte {
	keyStore, err := gcrypto.NewKeyStore()
	if err != nil {
		return nil
	}

	_, key, err := keyStore.Load(projectID)
	if err != nil {
		return nil
	}
	return key
}
