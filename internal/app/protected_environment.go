package app

import (
	"github.com/ghostable-dev/beta/internal/store"
	"github.com/ghostable-dev/beta/internal/userpresence"
)

const (
	protectedOperationDeploy  = "deploy"
	protectedOperationEnvPull = "env.pull"
	protectedOperationEnvRun  = "env.run"
	protectedOperationVarPull = "var.pull"
)

var verifyProtectedEnvironmentUserPresence = userpresence.Verify

func (r *Runner) requireProtectedEnvironmentAccess(repo store.Repository, env string, operation string) error {
	if !isProductionLikeEnvironment(repo, env) {
		return nil
	}
	if repo.UsesAutomationCredential() {
		return nil
	}
	return verifyProtectedEnvironmentUserPresence(userpresence.Request{
		Environment: env,
		Operation:   operation,
		Interactive: r.interactive,
		In:          r.in,
		Out:         r.out,
		ErrOut:      r.errOut,
	})
}
