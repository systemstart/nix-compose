package composition

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// ValidateResources checks resource configurations and returns warnings.
// It warns if requests exceed limits for CPU or memory.
func ValidateResources(comp *eval.Composition) []string {
	var warnings []string

	names := make([]string, 0, len(comp.Services))
	for name := range comp.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		svc := comp.Services[name]
		warnings = append(warnings, validateServiceResources(name, svc)...)
	}
	return warnings
}

func validateServiceResources(name string, svc eval.Service) []string {
	limits, requests := extractLimitsAndRequests(svc)
	if limits == nil || requests == nil {
		return nil
	}

	var warnings []string
	if exceedsCPU(requests.CPU, limits.CPU) {
		warnings = append(warnings, fmt.Sprintf("service %q: CPU request %q exceeds limit %q", name, requests.CPU, limits.CPU))
	}
	if exceedsMemory(requests.Memory, limits.Memory) {
		warnings = append(warnings, fmt.Sprintf("service %q: memory request %q exceeds limit %q", name, requests.Memory, limits.Memory))
	}
	return warnings
}

func extractLimitsAndRequests(svc eval.Service) (*eval.ResourceSpec, *eval.ResourceSpec) {
	if svc.XNixCompose == nil || svc.XNixCompose.Resources == nil {
		return nil, nil
	}
	return svc.XNixCompose.Resources.Limits, svc.XNixCompose.Resources.Requests
}

// exceedsCPU returns true if the CPU request is numerically greater than the limit.
// Accepts plain numbers ("1.0", "0.25") and millicore strings ("100m", "25m").
func exceedsCPU(request, limit string) bool {
	if request == "" || limit == "" {
		return false
	}
	r, okR := parseCPU(request)
	l, okL := parseCPU(limit)
	if !okR || !okL {
		return false
	}
	return r > l
}

// exceedsMemory returns true if the memory request is numerically greater than the limit.
// Accepts plain numbers and suffixed strings (K, M, G, T, Ki, Mi, Gi, Ti).
func exceedsMemory(request, limit string) bool {
	if request == "" || limit == "" {
		return false
	}
	r, okR := parseMemory(request)
	l, okL := parseMemory(limit)
	if !okR || !okL {
		return false
	}
	return r > l
}

// parseCPU converts a CPU string to millicores.
// "1.0" → 1000, "0.25" → 250, "100m" → 100.
func parseCPU(s string) (float64, bool) {
	if strings.HasSuffix(s, "m") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "m"), 64)
		return v, err == nil
	}
	v, err := strconv.ParseFloat(s, 64)
	return v * 1000, err == nil
}

// parseMemory converts a memory string to bytes.
// Supports: plain bytes, K, M, G, T (powers of 1000) and Ki, Mi, Gi, Ti (powers of 1024).
func parseMemory(s string) (float64, bool) {
	suffixes := []struct {
		suffix     string
		multiplier float64
	}{
		{"Ti", 1024 * 1024 * 1024 * 1024},
		{"Gi", 1024 * 1024 * 1024},
		{"Mi", 1024 * 1024},
		{"Ki", 1024},
		{"T", 1000 * 1000 * 1000 * 1000},
		{"G", 1000 * 1000 * 1000},
		{"M", 1000 * 1000},
		{"K", 1000},
	}
	for _, sf := range suffixes {
		if strings.HasSuffix(s, sf.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(s, sf.suffix), 64)
			return v * sf.multiplier, err == nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}
