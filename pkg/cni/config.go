package cni

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
)

// DefaultConfDir is the standard directory for CNI conflist files.
const DefaultConfDir = "/etc/cni/net.d"

// DefaultPluginDirs lists standard paths where CNI plugin binaries are found.
var DefaultPluginDirs = []string{"/opt/cni/bin", "/usr/lib/cni", "/usr/libexec/cni"}

// RequiredPlugins lists the CNI plugins needed for bridge networking with DNS.
var RequiredPlugins = []string{"bridge", "host-local", "portmap", "firewall", "dnsname"}

// Store manages CNI conflist files on disk.
type Store struct {
	ConfDir    string
	PluginDirs []string
}

// NewStore creates a Store using the default directories.
// If CNI_PATH is set, its colon-separated entries are prepended to the
// search list so that Nix-provided plugins are discovered automatically.
func NewStore() *Store {
	dirs := append([]string{}, DefaultPluginDirs...)
	if env := os.Getenv("CNI_PATH"); env != "" {
		dirs = append(splitPath(env), dirs...)
	}
	return &Store{
		ConfDir:    DefaultConfDir,
		PluginDirs: dirs,
	}
}

// splitPath splits a colon-separated path string into individual entries.
func splitPath(s string) []string {
	var out []string
	for _, p := range filepath.SplitList(s) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ConflistName returns the conflist filename for a project.
func ConflistName(project string) string {
	return fmt.Sprintf("nix-compose-%s.conflist", project)
}

// ConflistPath returns the full path to a project's conflist file.
func (s *Store) ConflistPath(project string) string {
	return filepath.Join(s.ConfDir, ConflistName(project))
}

// BridgeName returns the Linux bridge name for a project.
// Bridge names are limited to 15 characters (IFNAMSIZ-1).
func BridgeName(project string) string {
	prefix := "nc-"
	name := prefix + project
	if len(name) <= 15 {
		return name
	}
	// Truncate and add a hash suffix for uniqueness.
	h := fnv.New32a()
	_, _ = h.Write([]byte(project))
	suffix := fmt.Sprintf("%08x", h.Sum32())
	// "nc-" (3) + truncated + "-" + 8 hex = 15 max → truncated part = 3 chars
	return name[:3] + "-" + suffix
}

// SubnetForProject returns a deterministic /24 subnet for a project.
// Uses FNV-32a hash to derive two octets: 10.X.Y.0/24.
func SubnetForProject(project string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(project))
	sum := h.Sum32()
	// Use two bytes from the hash for the second and third octets.
	// Avoid 0 and 255 for the second octet to stay in valid range.
	x := (sum>>8)%254 + 1
	y := sum % 256
	return fmt.Sprintf("10.%d.%d.0/24", x, y)
}

// BuildConflist generates the CNI conflist JSON for a project.
func BuildConflist(project string) ([]byte, error) {
	bridge := BridgeName(project)
	subnet := SubnetForProject(project)

	conflist := map[string]any{
		"cniVersion": "0.4.0",
		"name":       bridge,
		"plugins": []map[string]any{
			{
				"type":        "bridge",
				"bridge":      bridge,
				"isGateway":   true,
				"ipMasq":      true,
				"hairpinMode": true,
				"ipam": map[string]any{
					"type":   "host-local",
					"ranges": [][]map[string]string{{{"subnet": subnet}}},
				},
			},
			{
				"type":         "portmap",
				"capabilities": map[string]bool{"portMappings": true},
			},
			{
				"type": "firewall",
			},
			{
				"type":         "dnsname",
				"domainName":   "nix-compose.local",
				"capabilities": map[string]bool{"aliases": true},
			},
		},
	}

	data, err := json.MarshalIndent(conflist, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshalling conflist: %w", err)
	}
	return data, nil
}

// Write generates and writes the conflist file for a project.
func (s *Store) Write(project string) error {
	data, err := BuildConflist(project)
	if err != nil {
		return fmt.Errorf("building conflist: %w", err)
	}

	if err := os.MkdirAll(s.ConfDir, 0o755); err != nil {
		return fmt.Errorf("creating confdir %s: %w", s.ConfDir, err)
	}

	path := s.ConflistPath(project)
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // CNI config needs standard read perms
		return fmt.Errorf("writing conflist %s: %w", path, err)
	}
	return nil
}

// Remove deletes the conflist file for a project. No-op if absent.
func (s *Store) Remove(project string) error {
	path := s.ConflistPath(project)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing conflist %s: %w", path, err)
	}
	return nil
}

// CheckPlugins returns the names of required CNI plugins that are missing.
func (s *Store) CheckPlugins() []string {
	var missing []string
	for _, plugin := range RequiredPlugins {
		if !s.pluginExists(plugin) {
			missing = append(missing, plugin)
		}
	}
	return missing
}

func (s *Store) pluginExists(name string) bool {
	for _, dir := range s.PluginDirs {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}
