package cri

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestParsePort(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *runtimev1.PortMapping
		wantErr bool
	}{
		{
			name:  "host:container",
			input: "8080:80",
			want: &runtimev1.PortMapping{
				HostPort:      8080,
				ContainerPort: 80,
				Protocol:      runtimev1.Protocol_TCP,
			},
		},
		{
			name:  "host:container/udp",
			input: "8080:80/udp",
			want: &runtimev1.PortMapping{
				HostPort:      8080,
				ContainerPort: 80,
				Protocol:      runtimev1.Protocol_UDP,
			},
		},
		{
			name:  "hostIP:host:container",
			input: "127.0.0.1:8080:80",
			want: &runtimev1.PortMapping{
				HostIp:        "127.0.0.1",
				HostPort:      8080,
				ContainerPort: 80,
				Protocol:      runtimev1.Protocol_TCP,
			},
		},
		{
			name:  "container-only",
			input: "80",
			want: &runtimev1.PortMapping{
				ContainerPort: 80,
				Protocol:      runtimev1.Protocol_TCP,
			},
		},
		{
			name:    "invalid",
			input:   "abc",
			wantErr: true,
		},
		{
			name:    "too many colons",
			input:   "1:2:3:4",
			wantErr: true,
		},
		{
			name:    "invalid protocol",
			input:   "80/xyz",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePort(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.HostPort != tt.want.HostPort {
				t.Errorf("HostPort = %d, want %d", got.HostPort, tt.want.HostPort)
			}
			if got.ContainerPort != tt.want.ContainerPort {
				t.Errorf("ContainerPort = %d, want %d", got.ContainerPort, tt.want.ContainerPort)
			}
			if got.Protocol != tt.want.Protocol {
				t.Errorf("Protocol = %v, want %v", got.Protocol, tt.want.Protocol)
			}
			if got.HostIp != tt.want.HostIp {
				t.Errorf("HostIp = %q, want %q", got.HostIp, tt.want.HostIp)
			}
		})
	}
}

func TestParsePorts(t *testing.T) {
	ports := []string{"8080:80", "9090:90/udp"}
	named := []eval.NamedPort{
		{Name: "http", ContainerPort: 3000, HostPort: 3000, Protocol: "tcp"},
		{Name: "grpc", ContainerPort: 50051, Protocol: "udp"},
	}

	result, err := ParsePorts(ports, named)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 port mappings, got %d", len(result))
	}

	// First two from string ports.
	if result[0].ContainerPort != 80 || result[0].HostPort != 8080 {
		t.Errorf("port 0: got %d:%d, want 8080:80", result[0].HostPort, result[0].ContainerPort)
	}
	if result[1].Protocol != runtimev1.Protocol_UDP {
		t.Errorf("port 1: expected UDP")
	}

	// Named ports.
	if result[2].ContainerPort != 3000 || result[2].HostPort != 3000 {
		t.Errorf("port 2: got %d:%d, want 3000:3000", result[2].HostPort, result[2].ContainerPort)
	}
	if result[3].Protocol != runtimev1.Protocol_UDP {
		t.Errorf("port 3: expected UDP for grpc named port")
	}
}

func TestParsePortsEmpty(t *testing.T) {
	result, err := ParsePorts(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 ports, got %d", len(result))
	}
}

func TestEnvsToKeyValues(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		kvs := EnvsToKeyValues(nil)
		if kvs != nil {
			t.Errorf("expected nil, got %v", kvs)
		}
	})

	t.Run("single", func(t *testing.T) {
		kvs := EnvsToKeyValues(map[string]string{"FOO": "bar"})
		if len(kvs) != 1 {
			t.Fatalf("expected 1, got %d", len(kvs))
		}
		if kvs[0].Key != "FOO" || kvs[0].Value != "bar" {
			t.Errorf("got %s=%s, want FOO=bar", kvs[0].Key, kvs[0].Value)
		}
	})

	t.Run("multiple", func(t *testing.T) {
		kvs := EnvsToKeyValues(map[string]string{"A": "1", "B": "2", "C": "3"})
		if len(kvs) != 3 {
			t.Errorf("expected 3, got %d", len(kvs))
		}
	})
}

func TestParseStopSignal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want runtimev1.Signal
	}{
		{"SIGTERM", "SIGTERM", runtimev1.Signal_SIGTERM},
		{"SIGKILL", "SIGKILL", runtimev1.Signal_SIGKILL},
		{"SIGUSR1", "SIGUSR1", runtimev1.Signal_SIGUSR1},
		{"case-insensitive", "sigterm", runtimev1.Signal_SIGTERM},
		{"mixed-case", "SigKill", runtimev1.Signal_SIGKILL},
		{"without-SIG-prefix", "TERM", runtimev1.Signal_SIGTERM},
		{"without-SIG-prefix-lower", "kill", runtimev1.Signal_SIGKILL},
		{"empty", "", runtimev1.Signal_RUNTIME_DEFAULT},
		{"bogus", "NOTASIGNAL", runtimev1.Signal_RUNTIME_DEFAULT},
		{"SIGHUP", "SIGHUP", runtimev1.Signal_SIGHUP},
		{"SIGINT", "SIGINT", runtimev1.Signal_SIGINT},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseStopSignal(tt.in)
			if got != tt.want {
				t.Errorf("ParseStopSignal(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildContainerConfig_StopSignal(t *testing.T) {
	svc := eval.Service{
		Image:      "nginx",
		StopSignal: "SIGUSR1",
	}
	cfg := BuildContainerConfig("web", svc, "proj", "v1", nil)
	if cfg.StopSignal != runtimev1.Signal_SIGUSR1 {
		t.Errorf("StopSignal = %v, want SIGUSR1", cfg.StopSignal)
	}
}

func TestBuildContainerConfig_StopSignalEmpty(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	cfg := BuildContainerConfig("web", svc, "proj", "v1", nil)
	if cfg.StopSignal != runtimev1.Signal_RUNTIME_DEFAULT {
		t.Errorf("StopSignal = %v, want RUNTIME_DEFAULT", cfg.StopSignal)
	}
}

func buildTestPodConfig() *runtimev1.PodSandboxConfig {
	svc := eval.Service{
		Image:    "nginx:latest",
		Hostname: "myhost",
		Ports:    []string{"8080:80"},
		XNixCompose: &eval.NixComposeExtended{
			NamedPorts: []eval.NamedPort{
				{Name: "metrics", ContainerPort: 9090, HostPort: 9090},
			},
		},
	}
	return BuildPodConfig("myproject", "web", svc, "v1", PodNetworkHost)
}

func TestBuildPodConfig_Metadata(t *testing.T) {
	cfg := buildTestPodConfig()
	if cfg.Metadata.Name != "myproject-web" {
		t.Errorf("Name = %q, want myproject-web", cfg.Metadata.Name)
	}
	if cfg.Metadata.Namespace != "nix-compose" {
		t.Errorf("Namespace = %q, want nix-compose", cfg.Metadata.Namespace)
	}
	if cfg.Metadata.Uid == "" {
		t.Error("Uid should not be empty")
	}
}

func TestBuildPodConfig_HostAndLogs(t *testing.T) {
	cfg := buildTestPodConfig()
	// The helper builds a host-network pod, which shares the host's UTS
	// namespace and so cannot carry a hostname of its own.
	if cfg.Hostname != "" {
		t.Errorf("Hostname = %q, want empty for a host-network pod", cfg.Hostname)
	}
	if cfg.LogDirectory != "/tmp/nix-compose-logs/myproject/web" {
		t.Errorf("LogDirectory = %q", cfg.LogDirectory)
	}
}

func TestBuildPodConfig_Hostname(t *testing.T) {
	svc := eval.Service{Image: "nginx:latest", Hostname: "myhost"}
	cfg := BuildPodConfig("myproject", "web", svc, "v1", PodNetworkCNI)
	if cfg.Hostname != "myhost" {
		t.Errorf("Hostname = %q, want myhost", cfg.Hostname)
	}
}

func TestBuildPodConfig_Labels(t *testing.T) {
	cfg := buildTestPodConfig()
	if cfg.Labels[LabelProject] != "myproject" {
		t.Errorf("project label = %q", cfg.Labels[LabelProject])
	}
	if cfg.Labels[LabelService] != "web" {
		t.Errorf("service label = %q", cfg.Labels[LabelService])
	}
	if cfg.Labels[LabelVersion] != "v1" {
		t.Errorf("version label = %q", cfg.Labels[LabelVersion])
	}
}

func TestBuildPodConfig_Ports(t *testing.T) {
	cfg := buildTestPodConfig()
	if len(cfg.PortMappings) != 2 {
		t.Fatalf("expected 2 port mappings, got %d", len(cfg.PortMappings))
	}
}

func TestBuildPodConfig_HostNetwork(t *testing.T) {
	cfg := buildTestPodConfig()
	if cfg.Linux == nil || cfg.Linux.SecurityContext == nil || cfg.Linux.SecurityContext.NamespaceOptions == nil {
		t.Fatal("expected Linux.SecurityContext.NamespaceOptions to be set")
		return
	}
	if cfg.Linux.SecurityContext.NamespaceOptions.Network != runtimev1.NamespaceMode_NODE {
		t.Errorf("Network = %v, want NODE", cfg.Linux.SecurityContext.NamespaceOptions.Network)
	}
}

func TestBuildPodConfig_CNINetwork(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	cfg := BuildPodConfig("proj", "web", svc, "v1", PodNetworkCNI)
	if cfg.Linux.SecurityContext.NamespaceOptions.Network != runtimev1.NamespaceMode_POD {
		t.Errorf("Network = %v, want POD", cfg.Linux.SecurityContext.NamespaceOptions.Network)
	}
}

func TestBuildPodConfig_HostNetworkExplicit(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	cfg := BuildPodConfig("proj", "web", svc, "v1", PodNetworkHost)
	if cfg.Linux.SecurityContext.NamespaceOptions.Network != runtimev1.NamespaceMode_NODE {
		t.Errorf("Network = %v, want NODE", cfg.Linux.SecurityContext.NamespaceOptions.Network)
	}
}

func TestBuildPodConfig_DefaultHostname(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	cfg := BuildPodConfig("proj", "web", svc, "v1", PodNetworkCNI)
	if cfg.Hostname != "web" {
		t.Errorf("Hostname = %q, want service name %q", cfg.Hostname, "web")
	}
}

// TestBuildPodConfig_HostNetworkDropsHostname covers the runc rule that a pod
// in the host's network namespace shares its UTS namespace: asking for a
// hostname there fails sandbox creation outright.
func TestBuildPodConfig_HostNetworkDropsHostname(t *testing.T) {
	svc := eval.Service{Image: "nginx", Hostname: "myhost"}
	cfg := BuildPodConfig("proj", "web", svc, "v1", PodNetworkHost)
	if cfg.Hostname != "" {
		t.Errorf("Hostname = %q, want empty for a host-network pod", cfg.Hostname)
	}
}

func buildTestContainerConfig() *runtimev1.ContainerConfig {
	svc := eval.Service{
		Image:       "nginx:latest",
		Entrypoint:  eval.CommandValue{Parts: []string{"/entrypoint.sh"}},
		Command:     eval.CommandValue{Parts: []string{"serve", "--port", "80"}},
		Environment: map[string]string{"ENV": "prod"},
		WorkingDir:  "/app",
		User:        "1000",
		Privileged:  true,
	}
	return BuildContainerConfig("web", svc, "myproject", "v1", nil)
}

func TestBuildContainerConfig_MetadataAndImage(t *testing.T) {
	cfg := buildTestContainerConfig()
	if cfg.Metadata.Name != "web" {
		t.Errorf("Name = %q, want web", cfg.Metadata.Name)
	}
	if cfg.Image.Image != "nginx:latest" {
		t.Errorf("Image = %q, want nginx:latest", cfg.Image.Image)
	}
}

func TestBuildContainerConfig_EntrypointAndCommand(t *testing.T) {
	cfg := buildTestContainerConfig()
	if len(cfg.Command) != 1 || cfg.Command[0] != "/entrypoint.sh" {
		t.Errorf("Command = %v, want [/entrypoint.sh]", cfg.Command)
	}
	if len(cfg.Args) != 3 || cfg.Args[0] != "serve" {
		t.Errorf("Args = %v, want [serve --port 80]", cfg.Args)
	}
}

func TestBuildContainerConfig_WorkdirAndEnv(t *testing.T) {
	cfg := buildTestContainerConfig()
	if cfg.WorkingDir != "/app" {
		t.Errorf("WorkingDir = %q, want /app", cfg.WorkingDir)
	}
	if len(cfg.Envs) != 1 || cfg.Envs[0].Key != "ENV" || cfg.Envs[0].Value != "prod" {
		t.Errorf("Envs = %v, want [{ENV prod}]", cfg.Envs)
	}
}

func TestBuildContainerConfig_Labels(t *testing.T) {
	cfg := buildTestContainerConfig()
	if cfg.Labels[LabelProject] != "myproject" {
		t.Errorf("project label = %q", cfg.Labels[LabelProject])
	}
}

func TestBuildContainerConfig_SecurityContext(t *testing.T) {
	cfg := buildTestContainerConfig()
	if cfg.Linux == nil || cfg.Linux.SecurityContext == nil {
		t.Fatal("expected Linux.SecurityContext")
		return
	}
	if !cfg.Linux.SecurityContext.Privileged {
		t.Error("expected Privileged=true")
	}
	if cfg.Linux.SecurityContext.RunAsUser == nil || cfg.Linux.SecurityContext.RunAsUser.Value != 1000 {
		t.Errorf("RunAsUser = %v, want 1000", cfg.Linux.SecurityContext.RunAsUser)
	}
}

func TestBuildContainerConfig_LogPath(t *testing.T) {
	cfg := buildTestContainerConfig()
	if cfg.LogPath != "0.log" {
		t.Errorf("LogPath = %q, want 0.log", cfg.LogPath)
	}
}

func TestBuildContainerConfig_NoEntrypoint(t *testing.T) {
	svc := eval.Service{
		Image:   "alpine",
		Command: eval.CommandValue{Parts: []string{"echo", "hello"}},
	}

	cfg := BuildContainerConfig("test", svc, "proj", "v1", nil)

	if len(cfg.Command) != 0 {
		t.Errorf("Command should be empty when no entrypoint, got %v", cfg.Command)
	}
	if len(cfg.Args) != 2 || cfg.Args[0] != "echo" {
		t.Errorf("Args = %v, want [echo hello]", cfg.Args)
	}
}

// TestBuildContainerConfig_User covers the whole `user:` grammar. The
// `uid:gid` form is the one the config reference documents, and it used to be
// dropped in silence — ParseInt failed on the colon and the error was
// discarded, so the container ran as root with no warning anywhere.
func TestBuildContainerConfig_User(t *testing.T) {
	tests := []struct {
		name         string
		user         string
		wantUID      *int64
		wantGID      *int64
		wantUsername string
	}{
		{name: "empty", user: ""},
		{name: "numeric uid", user: "1000", wantUID: ptrInt64(1000)},
		{name: "uid and gid", user: "1000:1000", wantUID: ptrInt64(1000), wantGID: ptrInt64(1000)},
		{name: "uid and other gid", user: "0:2000", wantUID: ptrInt64(0), wantGID: ptrInt64(2000)},
		{name: "username", user: "nobody", wantUsername: "nobody"},
		{name: "username and gid", user: "nobody:100", wantUsername: "nobody", wantGID: ptrInt64(100)},
		// A named group has nowhere to go: CRI's RunAsGroup is numeric-only.
		{name: "username and named group", user: "nobody:nogroup", wantUsername: "nobody"},
		// No user means no group — CRI requires a user alongside RunAsGroup.
		{name: "gid only", user: ":100"},
		{name: "separator only", user: ":"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := BuildContainerConfig("test", eval.Service{Image: "alpine", User: tt.user}, "proj", "v1", nil)
			sc := cfg.Linux.SecurityContext

			assertInt64Value(t, "RunAsUser", sc.RunAsUser, tt.wantUID)
			assertInt64Value(t, "RunAsGroup", sc.RunAsGroup, tt.wantGID)

			if sc.RunAsUsername != tt.wantUsername {
				t.Errorf("RunAsUsername = %q, want %q", sc.RunAsUsername, tt.wantUsername)
			}
			// CRI: only one of RunAsUser / RunAsUsername may be set.
			if sc.RunAsUser != nil && sc.RunAsUsername != "" {
				t.Errorf("both RunAsUser (%d) and RunAsUsername (%q) set", sc.RunAsUser.Value, sc.RunAsUsername)
			}
		})
	}
}

func ptrInt64(v int64) *int64 { return &v }

// assertInt64Value compares a CRI Int64Value against an optional expectation,
// treating "should be unset" as a case in its own right rather than as zero —
// RunAsUser 0 (root) and RunAsUser unset are different things.
func assertInt64Value(t *testing.T, name string, got *runtimev1.Int64Value, want *int64) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %v, want nil", name, got.Value)
	case want != nil && got == nil:
		t.Errorf("%s = nil, want %d", name, *want)
	case want != nil && got.Value != *want:
		t.Errorf("%s = %d, want %d", name, got.Value, *want)
	}
}

func TestServiceLabels(t *testing.T) {
	labels := ServiceLabels("myproj", "web", "v2")
	if labels[LabelProject] != "myproj" {
		t.Errorf("project = %q", labels[LabelProject])
	}
	if labels[LabelService] != "web" {
		t.Errorf("service = %q", labels[LabelService])
	}
	if labels[LabelVersion] != "v2" {
		t.Errorf("version = %q", labels[LabelVersion])
	}
}

// --- Volume mount tests ---

func stubResolver(project, name string) (string, error) {
	return "/volumes/" + project + "/" + name, nil
}

func TestParseVolumeMount_BindAbsolute(t *testing.T) {
	m, err := ParseVolumeMount("/host/path:/container:ro", "proj", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.HostPath != "/host/path" {
		t.Errorf("HostPath = %q, want /host/path", m.HostPath)
	}
	if m.ContainerPath != "/container" {
		t.Errorf("ContainerPath = %q, want /container", m.ContainerPath)
	}
	if !m.Readonly {
		t.Error("expected Readonly=true")
	}
}

func TestParseVolumeMount_BindRelative(t *testing.T) {
	m, err := ParseVolumeMount("./data:/app/data", "proj", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ContainerPath != "/app/data" {
		t.Errorf("ContainerPath = %q, want /app/data", m.ContainerPath)
	}
	if m.Readonly {
		t.Error("expected Readonly=false")
	}
}

func TestParseVolumeMount_Named(t *testing.T) {
	vols := map[string]eval.Volume{"pgdata": {}}
	m, err := ParseVolumeMount("pgdata:/var/lib/postgresql/data", "proj", vols, stubResolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.HostPath != "/volumes/proj/pgdata" {
		t.Errorf("HostPath = %q, want /volumes/proj/pgdata", m.HostPath)
	}
	if m.ContainerPath != "/var/lib/postgresql/data" {
		t.Errorf("ContainerPath = %q", m.ContainerPath)
	}
}

func TestParseVolumeMount_ReadOnly(t *testing.T) {
	m, err := ParseVolumeMount("/src:/dst:ro", "proj", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.Readonly {
		t.Error("expected Readonly=true")
	}
}

func TestParseVolumeMount_SinglePart(t *testing.T) {
	m, err := ParseVolumeMount("/var/data", "proj", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil for anonymous volume, got %v", m)
	}
}

func TestBuildMounts_Combined(t *testing.T) {
	svc := eval.Service{
		Volumes: []string{
			"/host:/container",
			"pgdata:/var/lib/postgresql/data",
		},
		Tmpfs: []string{"/tmp"},
		XNixCompose: &eval.NixComposeExtended{
			UseHostStore:  true,
			NixStorePaths: []string{"/nix/store/abc123"},
		},
	}
	vols := map[string]eval.Volume{"pgdata": {}}

	mounts, err := BuildMounts(svc, "proj", vols, stubResolver)
	if err != nil {
		t.Fatalf("BuildMounts: %v", err)
	}
	if len(mounts) != 4 {
		t.Fatalf("expected 4 mounts, got %d", len(mounts))
	}

	// Bind mount.
	if mounts[0].HostPath != "/host" || mounts[0].ContainerPath != "/container" {
		t.Errorf("mount 0: %v", mounts[0])
	}
	// Named volume.
	if mounts[1].HostPath != "/volumes/proj/pgdata" {
		t.Errorf("mount 1 HostPath = %q", mounts[1].HostPath)
	}
	// Tmpfs.
	if mounts[2].HostPath != "tmpfs" || mounts[2].ContainerPath != "/tmp" {
		t.Errorf("mount 2: %v", mounts[2])
	}
	// Nix store.
	if mounts[3].HostPath != "/nix/store/abc123" || !mounts[3].Readonly {
		t.Errorf("mount 3: %v", mounts[3])
	}
}

func TestClampPort(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int32
	}{
		{"normal port", 80, 80},
		{"port exceeds max", 70000, 65535},
		{"negative port", -1, 0},
		{"zero port", 0, 0},
		{"max valid port", 65535, 65535},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampPort(tt.in)
			if got != tt.want {
				t.Errorf("clampPort(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsNamedVolume(t *testing.T) {
	compVolumes := map[string]eval.Volume{
		"pgdata": {},
	}

	tests := []struct {
		name   string
		source string
		vols   map[string]eval.Volume
		want   bool
	}{
		{"named volume in map", "pgdata", compVolumes, true},
		{"named volume not in map but no slashes", "mydata", compVolumes, true},
		{"host path with /", "/host/path", compVolumes, false},
		{"relative path with ./", "./data", compVolumes, false},
		{"relative path with ../", "../data", compVolumes, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNamedVolume(tt.source, tt.vols)
			if got != tt.want {
				t.Errorf("isNamedVolume(%q) = %v, want %v", tt.source, got, tt.want)
			}
		})
	}
}

func TestBuildMounts_Empty(t *testing.T) {
	svc := eval.Service{}
	mounts, err := BuildMounts(svc, "proj", nil, nil)
	if err != nil {
		t.Fatalf("BuildMounts: %v", err)
	}
	if mounts != nil {
		t.Errorf("expected nil, got %v", mounts)
	}
}
