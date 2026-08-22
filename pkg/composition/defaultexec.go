package composition

import (
	"fmt"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// LookupDefaultExec returns the command `nix-compose exec <service>` runs when
// the caller gives none, taken from `x-nix-compose.serviceInfo.defaultExec`.
//
// A nil result with a nil error means the service declares none, which is not
// an error — the caller falls back to a shell.
func LookupDefaultExec(comp *eval.Composition, service string) ([]string, error) {
	svc, ok := comp.Services[service]
	if !ok {
		return nil, fmt.Errorf("service %q not found", service)
	}
	if svc.XNixCompose == nil || svc.XNixCompose.ServiceInfo == nil {
		return nil, nil
	}
	return svc.XNixCompose.ServiceInfo.DefaultExec, nil
}
