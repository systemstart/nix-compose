package k8s

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// testPlan plans a service's volumes under the empty-dir policy, which is the
// behaviour the converter tests predating ConfigMap generation assume.
func testPlan(t *testing.T, name string, svc eval.Service, compVolumes map[string]eval.Volume) *volumePlan {
	t.Helper()
	return planVolumes(name, svc, compVolumes, RenderOptions{
		ProjectDir:            ".",
		UnrepresentableMounts: MountPolicyEmptyDir,
	})
}

// mustConvert converts a composition, failing the test on error.
func mustConvert(t *testing.T, comp *eval.Composition, secrets map[string]map[string]string, opts RenderOptions) []Manifest {
	t.Helper()
	result, err := Convert(comp, secrets, opts)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return result.Manifests
}

// bindMountProject writes a project directory containing one config file and
// returns a composition that bind-mounts it into a service.
func bindMountProject(t *testing.T, volumes ...string) (dir string, comp *eval.Composition) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "container", "files", "auth", "hydra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "container", "files", "auth", "hydra", "hydra.yml"),
		[]byte("serve:\n  public:\n    port: 4444\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(volumes) == 0 {
		volumes = []string{"./container/files/auth/hydra/hydra.yml:/etc/hydra/hydra.yml:ro"}
	}
	return dir, &eval.Composition{
		Services: map[string]eval.Service{
			"hydra": {Image: "oryd/hydra:v26.2.0", Volumes: volumes},
		},
	}
}

func findManifest[T any](manifests []Manifest) (T, bool) {
	for _, m := range manifests {
		if obj, ok := m.Object.(T); ok {
			return obj, true
		}
	}
	var zero T
	return zero, false
}

func TestConvert_BindMountedFileBecomesConfigMap(t *testing.T) {
	dir, comp := bindMountProject(t)

	manifests := mustConvert(t, comp, nil, RenderOptions{Namespace: "example", ProjectDir: dir})

	cm, ok := findManifest[ConfigMap](manifests)
	if !ok {
		t.Fatal("no ConfigMap generated for the bind-mounted file")
	}
	if cm.Metadata.Name != "hydra-hydra-yml" {
		t.Errorf("ConfigMap name = %q, want hydra-hydra-yml", cm.Metadata.Name)
	}
	if cm.Metadata.Namespace != "example" {
		t.Errorf("ConfigMap namespace = %q, want example", cm.Metadata.Namespace)
	}
	if got := cm.Data["hydra.yml"]; !strings.Contains(got, "port: 4444") {
		t.Errorf("ConfigMap data[hydra.yml] = %q, want the file's content", got)
	}

	d, ok := findManifest[Deployment](manifests)
	if !ok {
		t.Fatal("no Deployment generated")
	}
	podVols := d.Spec.Template.Spec.Volumes
	if len(podVols) != 1 {
		t.Fatalf("expected 1 pod volume, got %d", len(podVols))
	}
	if podVols[0].EmptyDir != nil {
		t.Error("bind-mounted file rendered as an emptyDir, so the container starts without its config")
	}
	if podVols[0].ConfigMap == nil || podVols[0].ConfigMap.Name != "hydra-hydra-yml" {
		t.Errorf("pod volume = %+v, want a configMap reference to hydra-hydra-yml", podVols[0])
	}
	if !rfc1123Label.MatchString(podVols[0].Name) {
		t.Errorf("pod volume name %q is not an RFC 1123 label, so the API server rejects it", podVols[0].Name)
	}

	mounts := d.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(mounts) != 1 {
		t.Fatalf("expected 1 volume mount, got %d", len(mounts))
	}
	want := VolumeMount{Name: "hydra-hydra-yml", MountPath: "/etc/hydra/hydra.yml", SubPath: "hydra.yml", ReadOnly: true}
	if mounts[0] != want {
		t.Errorf("volume mount = %+v, want %+v", mounts[0], want)
	}
	if mounts[0].Name != podVols[0].Name {
		t.Errorf("mount names volume %q but the pod declares %q", mounts[0].Name, podVols[0].Name)
	}
}

func TestConvert_RefusesUnrepresentableBindMount(t *testing.T) {
	dir, _ := bindMountProject(t)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte{0xff, 0xd8, 0x00, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	// A readable file that is nonetheless not part of the project.
	outside := filepath.Join(t.TempDir(), "elsewhere.yml")
	if err := os.WriteFile(outside, []byte("key: value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		volume string
		want   string
	}{
		{"directory", "./assets:/srv/assets:ro", "is a directory"},
		{"missing file", "./nope.yml:/etc/nope.yml:ro", "cannot read host path"},
		{"outside the project", outside + ":/etc/elsewhere.yml:ro", "outside the project directory"},
		{"home relative", "~/conf.yml:/etc/conf.yml:ro", "home directory"},
		{"binary file", "./logo.png:/srv/logo.png:ro", "not valid UTF-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp := &eval.Composition{Services: map[string]eval.Service{
				"hydra": {Image: "oryd/hydra:v26.2.0", Volumes: []string{tt.volume}},
			}}
			_, err := Convert(comp, nil, RenderOptions{Namespace: "example", ProjectDir: dir})
			if err == nil {
				t.Fatalf("Convert accepted %q; an unrepresentable mount must fail the render, "+
					"not become an emptyDir the container starts without", tt.volume)
			}
			msg := err.Error()
			for _, want := range []string{"hydra", tt.volume, tt.want} {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q does not mention %q", msg, want)
				}
			}
		})
	}
}

func TestConvert_EmptyDirPolicyWarnsInsteadOfFailing(t *testing.T) {
	dir, comp := bindMountProject(t, "/var/lib/data:/data")

	result, err := Convert(comp, nil, RenderOptions{
		Namespace:             "example",
		ProjectDir:            dir,
		UnrepresentableMounts: MountPolicyEmptyDir,
	})
	if err != nil {
		t.Fatalf("Convert with the empty-dir policy: %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "hydra") || !strings.Contains(result.Warnings[0], "/var/lib/data:/data") {
		t.Errorf("warning %q does not name the service and the volume", result.Warnings[0])
	}

	d, _ := findManifest[Deployment](result.Manifests)
	podVols := d.Spec.Template.Spec.Volumes
	if len(podVols) != 1 || podVols[0].EmptyDir == nil {
		t.Fatalf("expected 1 emptyDir pod volume, got %+v", podVols)
	}
	if !rfc1123Label.MatchString(podVols[0].Name) {
		t.Errorf("pod volume name %q is not an RFC 1123 label", podVols[0].Name)
	}
}

func TestConvert_SameFileMountedTwiceSharesOneVolume(t *testing.T) {
	src := "./container/files/auth/hydra/hydra.yml"
	dir, comp := bindMountProject(t, src+":/etc/hydra/hydra.yml:ro", src+":/etc/hydra/copy.yml:ro")

	manifests := mustConvert(t, comp, nil, RenderOptions{Namespace: "example", ProjectDir: dir})

	configMaps := 0
	for _, m := range manifests {
		if _, ok := m.Object.(ConfigMap); ok {
			configMaps++
		}
	}
	if configMaps != 1 {
		t.Errorf("expected 1 ConfigMap for one source mounted twice, got %d", configMaps)
	}

	d, _ := findManifest[Deployment](manifests)
	if got := len(d.Spec.Template.Spec.Volumes); got != 1 {
		t.Errorf("expected 1 pod volume, got %d", got)
	}
	if got := len(d.Spec.Template.Spec.Containers[0].VolumeMounts); got != 2 {
		t.Errorf("expected 2 volume mounts, got %d", got)
	}
}

func TestConvert_DistinctSourcesSharingABasenameGetDistinctNames(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "conf.yml"), []byte("from: "+sub+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	comp := &eval.Composition{Services: map[string]eval.Service{
		"app": {Image: "app", Volumes: []string{"./a/conf.yml:/etc/a.yml:ro", "./b/conf.yml:/etc/b.yml:ro"}},
	}}

	manifests := mustConvert(t, comp, nil, RenderOptions{Namespace: "default", ProjectDir: dir})

	d, _ := findManifest[Deployment](manifests)
	podVols := d.Spec.Template.Spec.Volumes
	if len(podVols) != 2 {
		t.Fatalf("expected 2 pod volumes, got %d", len(podVols))
	}
	if podVols[0].Name == podVols[1].Name {
		t.Fatalf("two sources sharing a basename both got the name %q", podVols[0].Name)
	}
	for _, pv := range podVols {
		if !rfc1123Label.MatchString(pv.Name) {
			t.Errorf("pod volume name %q is not an RFC 1123 label", pv.Name)
		}
	}
}

func TestConvert_InitContainerBindMountSharesThePlan(t *testing.T) {
	dir, _ := bindMountProject(t)
	src := "./container/files/auth/hydra/hydra.yml"
	comp := &eval.Composition{Services: map[string]eval.Service{
		"hydra": {
			Image:   "oryd/hydra:v26.2.0",
			Volumes: []string{src + ":/etc/hydra/hydra.yml:ro"},
			XNixCompose: &eval.NixComposeExtended{
				InitContainers: []eval.InitContainer{
					{Name: "migrate", Image: "oryd/hydra:v26.2.0", Volumes: []string{src + ":/etc/hydra/hydra.yml:ro"}},
				},
			},
		},
	}}

	manifests := mustConvert(t, comp, nil, RenderOptions{Namespace: "example", ProjectDir: dir})

	d, _ := findManifest[Deployment](manifests)
	inits := d.Spec.Template.Spec.InitContainers
	if len(inits) != 1 || len(inits[0].VolumeMounts) != 1 {
		t.Fatalf("expected 1 init container with 1 mount, got %+v", inits)
	}
	if inits[0].VolumeMounts[0].Name != d.Spec.Template.Spec.Volumes[0].Name {
		t.Errorf("init container mounts %q but the pod declares %q",
			inits[0].VolumeMounts[0].Name, d.Spec.Template.Spec.Volumes[0].Name)
	}
}

func TestResolveBindPath_NixStorePathIsRepresentable(t *testing.T) {
	const storePath = nixStorePrefix + "abcdef-hydra-config/hydra.yml"

	got, err := resolveBindPath(storePath, t.TempDir())
	if err != nil {
		t.Fatalf("resolveBindPath(%q): %v", storePath, err)
	}
	if got != storePath {
		t.Errorf("resolveBindPath(%q) = %q, want it unchanged", storePath, got)
	}
}

func TestReadBindSource_RefusesOversizeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, make([]byte, maxConfigMapBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := readBindSource("./big.txt", dir)
	if err == nil || !strings.Contains(err.Error(), "ConfigMap limit") {
		t.Fatalf("readBindSource on an oversize file = %v, want a ConfigMap limit error", err)
	}
}

func TestConvert_ReportsEveryRefusalAtOnce(t *testing.T) {
	dir := t.TempDir()
	comp := &eval.Composition{Services: map[string]eval.Service{
		"alloy": {Image: "alloy", Volumes: []string{"/var/run/docker.sock:/var/run/docker.sock:ro"}},
		"hydra": {Image: "hydra", Volumes: []string{"./missing.yml:/etc/hydra/hydra.yml:ro"}},
		"keto":  {Image: "keto", Volumes: []string{"./also-missing.yml:/etc/keto/keto.yml:ro"}},
	}}

	_, err := Convert(comp, nil, RenderOptions{Namespace: "example", ProjectDir: dir})
	if err == nil {
		t.Fatal("Convert accepted three unrepresentable mounts")
	}

	var unrepresentable *UnrepresentableMountError
	if !errors.As(err, &unrepresentable) {
		t.Fatalf("error is %T, want *UnrepresentableMountError", err)
	}
	if len(unrepresentable.Refusals) != 3 {
		t.Errorf("reported %d refusals, want 3 — one render should name them all, "+
			"not fail again on the next one", len(unrepresentable.Refusals))
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "3 bind mounts cannot be rendered:") {
		t.Errorf("error %q does not lead with the count", msg)
	}
	for _, svc := range []string{"alloy", "hydra", "keto"} {
		if !strings.Contains(msg, svc) {
			t.Errorf("error %q does not name service %q", msg, svc)
		}
	}
}

func TestReadBindSource_RefusesNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}

	// os.ReadFile on a FIFO blocks until a writer appears, so this has to be
	// refused on the stat, not attempted.
	done := make(chan error, 1)
	go func() {
		_, _, err := readBindSource("./pipe", dir)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "named pipe") {
			t.Fatalf("readBindSource on a FIFO = %v, want a named pipe refusal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readBindSource blocked on a FIFO instead of refusing it")
	}
}

// pemKeyProject writes a project whose only mount is a private key.
func pemKeyProject(t *testing.T) (dir string, comp *eval.Composition) {
	t.Helper()
	dir = t.TempDir()
	pem := "-----BEGIN PRIVATE KEY-----\nMIIBVAIBADANBg==\n-----END PRIVATE KEY-----\n"
	if err := os.WriteFile(filepath.Join(dir, "tls-key.pem"), []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, &eval.Composition{Services: map[string]eval.Service{
		"ingress": {Image: "traefik", Volumes: []string{"./tls-key.pem:/etc/certs/tls-key.pem:ro"}},
	}}
}

func TestConvert_RefusesSecretMaterialByDefault(t *testing.T) {
	dir, comp := pemKeyProject(t)

	_, err := Convert(comp, nil, RenderOptions{Namespace: "default", ProjectDir: dir})
	if err == nil {
		t.Fatal("Convert rendered a private key into a ConfigMap; a warning is not a control — " +
			"a CI job that renders and commits carries the key into version control")
	}

	var refused *MountRefusalError
	if !errors.As(err, &refused) {
		t.Fatalf("error is %T, want *MountRefusalError", err)
	}
	msg := err.Error()
	for _, want := range []string{"ingress", "tls-key.pem", "not a Secret"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	if !slices.Contains(refused.Overrides, "--secret-material=configmap") {
		t.Errorf("overrides = %v, want the flag that renders it anyway", refused.Overrides)
	}
}

func TestConvert_RefusalsNameOnlyTheOverridesThatApply(t *testing.T) {
	dir, _ := pemKeyProject(t)
	comp := &eval.Composition{Services: map[string]eval.Service{
		"ingress": {Image: "traefik", Volumes: []string{"./tls-key.pem:/etc/certs/tls-key.pem:ro"}},
		"web":     {Image: "nginx", Volumes: []string{"./nope.yml:/etc/nope.yml:ro"}},
	}}

	_, err := Convert(comp, nil, RenderOptions{Namespace: "default", ProjectDir: dir})
	var refused *MountRefusalError
	if !errors.As(err, &refused) {
		t.Fatalf("error is %T, want *MountRefusalError", err)
	}
	want := []string{"--unrepresentable-mounts=empty-dir", "--secret-material=configmap"}
	for _, w := range want {
		if !slices.Contains(refused.Overrides, w) {
			t.Errorf("overrides = %v, missing %q", refused.Overrides, w)
		}
	}
	if len(refused.Overrides) != len(want) {
		t.Errorf("overrides = %v, want exactly the two that apply", refused.Overrides)
	}
}

func TestConvert_WarnsWhenAConfigMapWouldCarryAPrivateKey(t *testing.T) {
	dir := t.TempDir()
	pem := "-----BEGIN PRIVATE KEY-----\nMIIBVAIBADANBg==\n-----END PRIVATE KEY-----\n"
	if err := os.WriteFile(filepath.Join(dir, "tls-key.pem"), []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	comp := &eval.Composition{Services: map[string]eval.Service{
		"ingress": {Image: "traefik", Volumes: []string{"./tls-key.pem:/etc/certs/tls-key.pem:ro"}},
	}}

	result, err := Convert(comp, nil, RenderOptions{
		Namespace:      "default",
		ProjectDir:     dir,
		SecretMaterial: SecretMaterialConfigMap,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning about the key material, got %v", result.Warnings)
	}
	for _, want := range []string{"ingress", "tls-key.pem", "private key", "not a Secret"} {
		if !strings.Contains(result.Warnings[0], want) {
			t.Errorf("warning %q does not mention %q", result.Warnings[0], want)
		}
	}
}
