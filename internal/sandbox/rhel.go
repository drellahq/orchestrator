package sandbox

import (
	"context"
	"fmt"

	"github.com/drellahq/orchestrator/internal/shellutil"
)

// RHELRegistration holds Red Hat subscription credentials for sandbox provisioning.
type RHELRegistration struct {
	OrgID         string
	ActivationKey string
}

// RegisterScript returns a shell script that installs subscription-manager and registers
// the system with the given org and activation key.
func (r *RHELRegistration) RegisterScript() string {
	org := shellutil.Quote(r.OrgID)
	key := shellutil.Quote(r.ActivationKey)
	return fmt.Sprintf(`set -euo pipefail
sudo dnf install -y subscription-manager
sudo subscription-manager register --org=%s --activationkey=%s
sudo subscription-manager status`, org, key)
}

func registerRHEL(ctx context.Context, ssh func(context.Context, string, ...string) error, name string, rhel *RHELRegistration) error {
	if rhel == nil || rhel.OrgID == "" || rhel.ActivationKey == "" {
		return nil
	}
	return ssh(ctx, name, "bash", "-c", rhel.RegisterScript())
}
