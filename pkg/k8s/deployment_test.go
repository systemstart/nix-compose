package k8s

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestConvertDeployment_Minimal(t *testing.T) {
	svc := eval.Service{Image: "nginx:latest"}
	m := convertDeployment("web", svc, nil, RenderOptions{Namespace: "default"})

	d, ok := m.Object.(Deployment)
	if !ok {
		t.Fatal("expected Deployment")
	}
	if d.Metadata.Name != "web" {
		t.Errorf("name = %q, want web", d.Metadata.Name)
	}
	if d.Spec.Replicas != 1 {
		t.Errorf("replicas = %d, want 1", d.Spec.Replicas)
	}
	if len(d.Spec.Template.Spec.Containers) != 1 {
		t.Fatal("expected 1 container")
	}
	c := d.Spec.Template.Spec.Containers[0]
	if c.Image != "nginx:latest" {
		t.Errorf("image = %q, want nginx:latest", c.Image)
	}
	if m.Filename != "web-deployment.yaml" {
		t.Errorf("filename = %q, want web-deployment.yaml", m.Filename)
	}
}

func TestConvertDeployment_WithCommand(t *testing.T) {
	svc := eval.Service{
		Image:   "node:18",
		Command: eval.CommandValue{Parts: []string{"node", "server.js"}},
	}
	m := convertDeployment("api", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	c := d.Spec.Template.Spec.Containers[0]
	if len(c.Command) != 2 || c.Command[0] != "node" {
		t.Errorf("command = %v, want [node server.js]", c.Command)
	}
}

func TestConvertDeployment_NamedPorts(t *testing.T) {
	svc := eval.Service{
		Image: "nginx",
		XNixCompose: &eval.NixComposeExtended{
			NamedPorts: []eval.NamedPort{
				{Name: "http", ContainerPort: 80},
				{Name: "dns", ContainerPort: 53, Protocol: "udp"},
			},
		},
	}
	m := convertDeployment("web", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	ports := d.Spec.Template.Spec.Containers[0].Ports
	if len(ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(ports))
	}
	if ports[0].Name != "http" || ports[0].ContainerPort != 80 {
		t.Errorf("port[0] = %+v, want http:80", ports[0])
	}
	if ports[1].Protocol != "UDP" {
		t.Errorf("port[1].protocol = %q, want UDP", ports[1].Protocol)
	}
}

func TestConvertDeployment_FallbackPorts(t *testing.T) {
	svc := eval.Service{
		Image: "nginx",
		Ports: []string{"8080:80", "53:53/udp"},
	}
	m := convertDeployment("web", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	ports := d.Spec.Template.Spec.Containers[0].Ports
	if len(ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(ports))
	}
	if ports[0].ContainerPort != 80 {
		t.Errorf("port[0].containerPort = %d, want 80", ports[0].ContainerPort)
	}
	if ports[1].Protocol != "UDP" {
		t.Errorf("port[1].protocol = %q, want UDP", ports[1].Protocol)
	}
}

func TestConvertDeployment_Resources(t *testing.T) {
	svc := eval.Service{
		Image: "node:18",
		XNixCompose: &eval.NixComposeExtended{
			Resources: &eval.Resources{
				Limits:   &eval.ResourceSpec{CPU: "1.0", Memory: "512M"},
				Requests: &eval.ResourceSpec{CPU: "0.25", Memory: "128M"},
			},
		},
	}
	m := convertDeployment("api", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	res := d.Spec.Template.Spec.Containers[0].Resources
	if res == nil {
		t.Fatal("expected resources")
		return
	}
	if res.Limits["cpu"] != "1.0" {
		t.Errorf("limits.cpu = %q, want 1.0", res.Limits["cpu"])
	}
	if res.Requests["memory"] != "128M" {
		t.Errorf("requests.memory = %q, want 128M", res.Requests["memory"])
	}
}

func TestConvertDeployment_BothProbes(t *testing.T) {
	svc := eval.Service{
		Image: "node:18",
		XNixCompose: &eval.NixComposeExtended{
			Probes: &eval.Probes{
				Liveness: &eval.Probe{
					HTTPGet:             &eval.ProbeHTTPGet{Path: "/healthz", Port: 3000},
					InitialDelaySeconds: 10,
					PeriodSeconds:       30,
				},
				Readiness: &eval.Probe{
					Exec: &eval.ProbeExec{Command: []string{"cat", "/tmp/ready"}},
				},
			},
		},
	}
	m := convertDeployment("api", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	c := d.Spec.Template.Spec.Containers[0]
	if c.LivenessProbe == nil {
		t.Fatal("expected liveness probe")
		return
	}
	if c.LivenessProbe.HTTPGet == nil || c.LivenessProbe.HTTPGet.Path != "/healthz" {
		t.Errorf("liveness probe HTTPGet = %+v, want /healthz", c.LivenessProbe.HTTPGet)
	}
	if c.LivenessProbe.InitialDelaySeconds != 10 {
		t.Errorf("initialDelaySeconds = %d, want 10", c.LivenessProbe.InitialDelaySeconds)
	}
	if c.ReadinessProbe == nil {
		t.Fatal("expected readiness probe")
		return
	}
	if c.ReadinessProbe.Exec == nil || c.ReadinessProbe.Exec.Command[0] != "cat" {
		t.Errorf("readiness probe exec = %+v, want cat", c.ReadinessProbe.Exec)
	}
}

func TestConvertDeployment_InitContainers(t *testing.T) {
	svc := eval.Service{
		Image: "node:18",
		XNixCompose: &eval.NixComposeExtended{
			InitContainers: []eval.InitContainer{
				{
					Name:        "migrate",
					Image:       "flyway:latest",
					Command:     eval.CommandValue{Parts: []string{"flyway", "migrate"}},
					Environment: map[string]string{"DB_URL": "postgres://db:5432/app"},
				},
			},
		},
	}
	m := convertDeployment("api", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	inits := d.Spec.Template.Spec.InitContainers
	if len(inits) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(inits))
	}
	if inits[0].Name != "migrate" {
		t.Errorf("init name = %q, want migrate", inits[0].Name)
	}
	if inits[0].Image != "flyway:latest" {
		t.Errorf("init image = %q, want flyway:latest", inits[0].Image)
	}
	if len(inits[0].Command) != 2 || inits[0].Command[0] != "flyway" {
		t.Errorf("init command = %v, want [flyway migrate]", inits[0].Command)
	}
	if len(inits[0].Env) != 1 || inits[0].Env[0].Name != "DB_URL" {
		t.Errorf("init env = %v, want DB_URL", inits[0].Env)
	}
}

func TestConvertDeployment_Volumes(t *testing.T) {
	svc := eval.Service{
		Image:   "postgres:15",
		Volumes: []string{"db-data:/var/lib/postgresql/data"},
	}
	compVols := map[string]eval.Volume{"db-data": {}}
	m := convertDeployment("db", svc, compVols, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	c := d.Spec.Template.Spec.Containers[0]
	if len(c.VolumeMounts) != 1 {
		t.Fatalf("expected 1 volume mount, got %d", len(c.VolumeMounts))
	}
	if c.VolumeMounts[0].MountPath != "/var/lib/postgresql/data" {
		t.Errorf("mountPath = %q, want /var/lib/postgresql/data", c.VolumeMounts[0].MountPath)
	}
	podVols := d.Spec.Template.Spec.Volumes
	if len(podVols) != 1 {
		t.Fatalf("expected 1 pod volume, got %d", len(podVols))
	}
	if podVols[0].PersistentVolumeClaim == nil || podVols[0].PersistentVolumeClaim.ClaimName != "db-data" {
		t.Errorf("expected PVC reference to db-data, got %+v", podVols[0])
	}
}

func TestConvertDeployment_HostPathVolume(t *testing.T) {
	svc := eval.Service{
		Image:   "nginx",
		Volumes: []string{"/data:/data"},
	}
	m := convertDeployment("web", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	podVols := d.Spec.Template.Spec.Volumes
	if len(podVols) != 1 {
		t.Fatalf("expected 1 pod volume, got %d", len(podVols))
	}
	if podVols[0].EmptyDir == nil {
		t.Error("expected emptyDir for host path volume")
	}
}

func TestConvertDeployment_EnvFrom(t *testing.T) {
	svc := eval.Service{
		Image: "node:18",
		XNixCompose: &eval.NixComposeExtended{
			EnvFrom: []eval.EnvFromSource{
				{SecretFile: "secrets/api.env"}, //nolint:gosec // test fixture path
			},
		},
	}
	m := convertDeployment("api", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	c := d.Spec.Template.Spec.Containers[0]
	if len(c.EnvFrom) != 1 {
		t.Fatalf("expected 1 envFrom, got %d", len(c.EnvFrom))
	}
	if c.EnvFrom[0].SecretRef == nil || c.EnvFrom[0].SecretRef.Name != "api-secrets" {
		t.Errorf("envFrom secretRef = %+v, want api-secrets", c.EnvFrom[0].SecretRef)
	}
}

func TestConvertDeployment_Labels(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	m := convertDeployment("web", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	if d.Metadata.Labels["app.kubernetes.io/name"] != "web" {
		t.Errorf("missing app.kubernetes.io/name label")
	}
	if d.Metadata.Labels["app.kubernetes.io/managed-by"] != "nix-compose" {
		t.Errorf("missing app.kubernetes.io/managed-by label")
	}
}

func TestConvertDeployment_SortedEnvVars(t *testing.T) {
	svc := eval.Service{
		Image: "node",
		Environment: map[string]string{
			"Z_VAR": "z",
			"A_VAR": "a",
			"M_VAR": "m",
		},
	}
	m := convertDeployment("app", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	envs := d.Spec.Template.Spec.Containers[0].Env
	if len(envs) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(envs))
	}
	if envs[0].Name != "A_VAR" || envs[1].Name != "M_VAR" || envs[2].Name != "Z_VAR" {
		t.Errorf("env vars not sorted: %v", envs)
	}
}

func TestConvertDeployment_WorkingDir(t *testing.T) {
	svc := eval.Service{Image: "node", WorkingDir: "/app"}
	m := convertDeployment("api", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	if d.Spec.Template.Spec.Containers[0].WorkingDir != "/app" {
		t.Errorf("workingDir = %q, want /app", d.Spec.Template.Spec.Containers[0].WorkingDir)
	}
}

func TestParseVolumeString(t *testing.T) {
	tests := []struct {
		input    string
		source   string
		dest     string
		readOnly bool
	}{
		{"db-data:/var/lib/data", "db-data", "/var/lib/data", false},
		{"/host:/container:ro", "/host", "/container", true},
		{"/single", "/single", "/single", false},
		{"/data:/data:rw", "/data", "/data", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			s, d, ro := parseVolumeString(tt.input)
			if s != tt.source || d != tt.dest || ro != tt.readOnly {
				t.Errorf("parseVolumeString(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.input, s, d, ro, tt.source, tt.dest, tt.readOnly)
			}
		})
	}
}

func TestSanitizeVolumeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"db-data", "db-data"},
		{"/var/lib/data", "var-lib-data"},
		{"/data", "data"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeVolumeName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeVolumeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConvertEnvVars_Empty(t *testing.T) {
	got := convertEnvVars(nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestConvertResources_NoXNixCompose(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	got := convertResources(svc)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestConvertProbe_Nil(t *testing.T) {
	got := convertProbe(nil)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestConvertContainerPorts_Empty(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	got := convertContainerPorts(svc)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestConvertDeployment_RestartPolicyOmitted(t *testing.T) {
	svc := eval.Service{Image: "nginx:latest"}
	m := convertDeployment("web", svc, nil, RenderOptions{Namespace: "default"})
	d := m.Object.(Deployment)
	if d.Spec.Template.Spec.RestartPolicy != "" {
		t.Errorf("restartPolicy = %q, want empty (omitted)", d.Spec.Template.Spec.RestartPolicy)
	}
}
