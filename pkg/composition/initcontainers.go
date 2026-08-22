package composition

import (
	"fmt"
	"sort"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// initServiceName generates the compose service name for an init container.
func initServiceName(serviceName, initName string) string {
	return fmt.Sprintf("%s-init-%s", serviceName, initName)
}

// SynthesizeInitContainers expands init containers into real compose services.
// Each init container becomes a service with restart: "no", chained via depends_on.
// The main service depends on the last init container (service_completed_successfully).
func SynthesizeInitContainers(comp *eval.Composition) *eval.Composition {
	newServices := make(map[string]eval.Service, len(comp.Services))

	// Process services in deterministic order.
	names := make([]string, 0, len(comp.Services))
	for name := range comp.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		svc := comp.Services[name]
		if svc.XNixCompose == nil || len(svc.XNixCompose.InitContainers) == 0 {
			newServices[name] = svc
			continue
		}

		initSvcs, mainDeps := synthesizeForService(name, svc)
		for initName, initSvc := range initSvcs {
			newServices[initName] = initSvc
		}

		// Update main service depends_on to include the last init container.
		if svc.DependsOn.Entries == nil {
			svc.DependsOn.Entries = make(map[string]eval.DependsOnEntry)
		}
		for depName, dep := range mainDeps.Entries {
			svc.DependsOn.Entries[depName] = dep
		}
		newServices[name] = svc
	}

	return &eval.Composition{
		Services: newServices,
		Networks: comp.Networks,
		Volumes:  comp.Volumes,
	}
}

// synthesizeForService creates init container services for a given main service.
// Returns the new services map and the depends_on entries for the main service.
func synthesizeForService(name string, svc eval.Service) (map[string]eval.Service, eval.DependsOnValue) {
	inits := svc.XNixCompose.InitContainers
	services := make(map[string]eval.Service, len(inits))

	var prevInitName string

	for i, ic := range inits {
		initName := initServiceName(name, ic.Name)

		initSvc := eval.Service{
			Image:       ic.Image,
			Command:     ic.Command,
			Environment: ic.Environment,
			Volumes:     ic.Volumes,
			Restart:     "no",
		}

		// Set up depends_on for chaining.
		initDeps := eval.DependsOnValue{
			Entries: make(map[string]eval.DependsOnEntry),
		}

		if i == 0 {
			// First init inherits the main service's existing depends_on.
			for depName, dep := range svc.DependsOn.Entries {
				initDeps.Entries[depName] = dep
			}
		} else {
			// Subsequent inits depend on the previous init.
			initDeps.Entries[prevInitName] = eval.DependsOnEntry{
				Condition: "service_completed_successfully",
			}
		}

		if len(initDeps.Entries) > 0 {
			initSvc.DependsOn = initDeps
		}

		services[initName] = initSvc
		prevInitName = initName
	}

	// Main service depends on the last init container.
	mainDeps := eval.DependsOnValue{
		Entries: map[string]eval.DependsOnEntry{
			prevInitName: {Condition: "service_completed_successfully"},
		},
	}

	return services, mainDeps
}
