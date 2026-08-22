package cni

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBridgeName_Short(t *testing.T) {
	name := BridgeName("app")
	if name != "nc-app" {
		t.Errorf("BridgeName(app) = %q, want nc-app", name)
	}
}

func TestBridgeName_Exact15(t *testing.T) {
	// "nc-" (3) + 12 = 15
	name := BridgeName("123456789012")
	if name != "nc-123456789012" {
		t.Errorf("BridgeName = %q, want nc-123456789012", name)
	}
	if len(name) != 15 {
		t.Errorf("len = %d, want 15", len(name))
	}
}

func TestBridgeName_Truncated(t *testing.T) {
	name := BridgeName("very-long-project-name-that-exceeds-limit")
	if len(name) > 15 {
		t.Errorf("len = %d, want <= 15", len(name))
	}
	if !strings.HasPrefix(name, "nc-") {
		t.Errorf("should start with nc-, got %q", name)
	}
}

func TestBridgeName_MaxLen(t *testing.T) {
	// Even the longest project name should produce a bridge name <= 15 chars.
	name := BridgeName(strings.Repeat("x", 200))
	if len(name) > 15 {
		t.Errorf("len = %d, want <= 15", len(name))
	}
}

func TestSubnetForProject_Deterministic(t *testing.T) {
	s1 := SubnetForProject("myproject")
	s2 := SubnetForProject("myproject")
	if s1 != s2 {
		t.Errorf("not deterministic: %q != %q", s1, s2)
	}
}

func TestSubnetForProject_DifferentProjects(t *testing.T) {
	s1 := SubnetForProject("alpha")
	s2 := SubnetForProject("beta")
	if s1 == s2 {
		t.Errorf("different projects should (usually) get different subnets: both %q", s1)
	}
}

func TestSubnetForProject_Format(t *testing.T) {
	s := SubnetForProject("test")
	if !strings.HasPrefix(s, "10.") {
		t.Errorf("should start with 10., got %q", s)
	}
	if !strings.HasSuffix(s, ".0/24") {
		t.Errorf("should end with .0/24, got %q", s)
	}
}

func TestBuildConflist_Structure(t *testing.T) {
	data, err := BuildConflist("myproj")
	if err != nil {
		t.Fatalf("BuildConflist: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed["cniVersion"] != "0.4.0" {
		t.Errorf("cniVersion = %v, want 0.4.0", parsed["cniVersion"])
	}
	if parsed["name"] != BridgeName("myproj") {
		t.Errorf("name = %v, want %s", parsed["name"], BridgeName("myproj"))
	}
}

func TestBuildConflist_PluginCount(t *testing.T) {
	data, err := BuildConflist("test")
	if err != nil {
		t.Fatalf("BuildConflist: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	plugins, ok := parsed["plugins"].([]any)
	if !ok {
		t.Fatal("plugins should be an array")
	}
	if len(plugins) != 4 {
		t.Errorf("expected 4 plugins, got %d", len(plugins))
	}
}

func TestBuildConflist_BridgeName(t *testing.T) {
	data, err := BuildConflist("myproj")
	if err != nil {
		t.Fatalf("BuildConflist: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	plugins := parsed["plugins"].([]any)
	bridgePlugin := plugins[0].(map[string]any)
	if bridgePlugin["type"] != "bridge" {
		t.Errorf("first plugin type = %v, want bridge", bridgePlugin["type"])
	}
	if bridgePlugin["bridge"] != BridgeName("myproj") {
		t.Errorf("bridge = %v, want %s", bridgePlugin["bridge"], BridgeName("myproj"))
	}
}

func TestBuildConflist_Subnet(t *testing.T) {
	data, err := BuildConflist("myproj")
	if err != nil {
		t.Fatalf("BuildConflist: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	plugins := parsed["plugins"].([]any)
	bridgePlugin := plugins[0].(map[string]any)
	ipam := bridgePlugin["ipam"].(map[string]any)
	ranges := ipam["ranges"].([]any)
	first := ranges[0].([]any)
	entry := first[0].(map[string]any)
	if entry["subnet"] != SubnetForProject("myproj") {
		t.Errorf("subnet = %v, want %s", entry["subnet"], SubnetForProject("myproj"))
	}
}

func TestConflistName(t *testing.T) {
	name := ConflistName("myproj")
	if name != "nix-compose-myproj.conflist" {
		t.Errorf("ConflistName = %q, want nix-compose-myproj.conflist", name)
	}
}

func TestConflistPath(t *testing.T) {
	s := &Store{ConfDir: "/etc/cni/net.d"}
	path := s.ConflistPath("myproj")
	if path != "/etc/cni/net.d/nix-compose-myproj.conflist" {
		t.Errorf("ConflistPath = %q", path)
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	s := &Store{ConfDir: dir}

	if err := s.Write("testproj"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	path := s.ConflistPath("testproj")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON in written file: %v", err)
	}
	if parsed["cniVersion"] != "0.4.0" {
		t.Errorf("written file cniVersion = %v", parsed["cniVersion"])
	}
}

func TestWriteIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := &Store{ConfDir: dir}

	if err := s.Write("testproj"); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if err := s.Write("testproj"); err != nil {
		t.Fatalf("Write 2: %v", err)
	}

	path := s.ConflistPath("testproj")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("file should not be empty")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	s := &Store{ConfDir: dir}

	if err := s.Write("testproj"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := s.Remove("testproj"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	path := s.ConflistPath("testproj")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed, got err: %v", err)
	}
}

func TestRemoveNonexistent(t *testing.T) {
	dir := t.TempDir()
	s := &Store{ConfDir: dir}

	// Should not error when file doesn't exist.
	if err := s.Remove("nonexistent"); err != nil {
		t.Fatalf("Remove nonexistent should be no-op: %v", err)
	}
}

func TestCheckPlugins_AllPresent(t *testing.T) {
	dir := t.TempDir()
	// Create fake plugin binaries.
	for _, name := range RequiredPlugins {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	s := &Store{ConfDir: t.TempDir(), PluginDirs: []string{dir}}
	missing := s.CheckPlugins()
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
}

func TestCheckPlugins_SomeMissing(t *testing.T) {
	dir := t.TempDir()
	// Only create bridge and portmap.
	for _, name := range []string{"bridge", "portmap"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	s := &Store{ConfDir: t.TempDir(), PluginDirs: []string{dir}}
	missing := s.CheckPlugins()
	if len(missing) != 3 {
		t.Errorf("expected 3 missing, got %d: %v", len(missing), missing)
	}
}

func TestCheckPlugins_NonePresent(t *testing.T) {
	s := &Store{ConfDir: t.TempDir(), PluginDirs: []string{t.TempDir()}}
	missing := s.CheckPlugins()
	if len(missing) != len(RequiredPlugins) {
		t.Errorf("expected %d missing, got %d", len(RequiredPlugins), len(missing))
	}
}

func TestNewStore_Default(t *testing.T) {
	s := NewStore()
	if s.ConfDir != DefaultConfDir {
		t.Errorf("ConfDir = %q, want %q", s.ConfDir, DefaultConfDir)
	}
	if len(s.PluginDirs) == 0 {
		t.Error("expected at least default plugin dirs")
	}
}

func TestNewStore_WithCNIPath(t *testing.T) {
	t.Setenv("CNI_PATH", "/custom/path:/another/path")
	s := NewStore()
	if s.PluginDirs[0] != "/custom/path" {
		t.Errorf("first dir = %q, want /custom/path", s.PluginDirs[0])
	}
	if s.PluginDirs[1] != "/another/path" {
		t.Errorf("second dir = %q, want /another/path", s.PluginDirs[1])
	}
}

func TestSplitPath_Empty(t *testing.T) {
	result := splitPath("")
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestSplitPath_Multiple(t *testing.T) {
	result := splitPath("/a:/b:/c")
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if result[0] != "/a" || result[1] != "/b" || result[2] != "/c" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestCheckPlugins_MultiplePluginDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Spread plugins across two directories.
	for _, name := range []string{"bridge", "host-local"} {
		if err := os.WriteFile(filepath.Join(dir1, name), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"portmap", "firewall", "dnsname"} {
		if err := os.WriteFile(filepath.Join(dir2, name), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	s := &Store{ConfDir: t.TempDir(), PluginDirs: []string{dir1, dir2}}
	missing := s.CheckPlugins()
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
}
