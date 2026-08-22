package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testdataPath(name string) string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata", name)
}

func loadMinimalComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("minimal.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestParseComposition_Minimal_ServiceCount(t *testing.T) {
	comp := loadMinimalComposition(t)
	if len(comp.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(comp.Services))
	}
}

func TestParseComposition_Minimal_WebServiceFields(t *testing.T) {
	comp := loadMinimalComposition(t)
	web, ok := comp.Services["web"]
	if !ok {
		t.Fatal("missing service 'web'")
	}
	if web.Image != "nginx:latest" {
		t.Errorf("image = %q, want %q", web.Image, "nginx:latest")
	}
	if len(web.Ports) != 1 || web.Ports[0] != "8080:80" {
		t.Errorf("ports = %v, want [8080:80]", web.Ports)
	}
	if web.Environment["NGINX_HOST"] != "localhost" {
		t.Errorf("env NGINX_HOST = %q, want %q", web.Environment["NGINX_HOST"], "localhost")
	}
}

func TestParseComposition_Minimal_XNixCompose(t *testing.T) {
	comp := loadMinimalComposition(t)
	web := comp.Services["web"]
	if web.XNixCompose == nil || web.XNixCompose.ServiceInfo == nil {
		t.Fatal("missing x-nix-compose.serviceInfo")
		return
	}
	if len(web.XNixCompose.ServiceInfo.DefaultExec) != 1 || web.XNixCompose.ServiceInfo.DefaultExec[0] != "bash" {
		t.Errorf("defaultExec = %v, want [bash]", web.XNixCompose.ServiceInfo.DefaultExec)
	}
}

func TestParseComposition_Minimal_Networks(t *testing.T) {
	comp := loadMinimalComposition(t)
	if len(comp.Networks) != 1 {
		t.Errorf("expected 1 network, got %d", len(comp.Networks))
	}
	if comp.Networks["default"].Name != "minimal_default" {
		t.Errorf("network name = %q, want %q", comp.Networks["default"].Name, "minimal_default")
	}
}

func loadTwoServicesComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("two-services.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(comp.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(comp.Services))
	}
	return comp
}

func TestParseComposition_TwoServices_WebDependsOn(t *testing.T) {
	comp := loadTwoServicesComposition(t)
	web := comp.Services["web"]
	if web.DependsOn.IsEmpty() {
		t.Fatal("web depends_on should not be empty")
	}
	entry, ok := web.DependsOn.Entries["api"]
	if !ok {
		t.Fatal("web should depend on api")
	}
	if entry.Condition != "service_healthy" {
		t.Errorf("condition = %q, want %q", entry.Condition, "service_healthy")
	}
	if len(web.Volumes) != 1 {
		t.Errorf("web volumes = %v, want 1 entry", web.Volumes)
	}
}

func TestParseComposition_TwoServices_APICommand(t *testing.T) {
	comp := loadTwoServicesComposition(t)
	api := comp.Services["api"]
	if api.Command.IsEmpty() {
		t.Fatal("api command should not be empty")
	}
	if len(api.Command.Parts) != 2 || api.Command.Parts[0] != "node" {
		t.Errorf("api command = %v, want [node server.js]", api.Command.Parts)
	}
}

func TestParseComposition_TwoServices_APIHealthcheck(t *testing.T) {
	comp := loadTwoServicesComposition(t)
	api := comp.Services["api"]
	if api.Healthcheck == nil {
		t.Fatal("api healthcheck should not be nil")
		return
	}
	if api.Healthcheck.Interval != "10s" {
		t.Errorf("healthcheck interval = %q, want %q", api.Healthcheck.Interval, "10s")
	}
	if api.Healthcheck.Retries != 3 {
		t.Errorf("healthcheck retries = %d, want 3", api.Healthcheck.Retries)
	}
	if len(api.Healthcheck.Test.Parts) != 4 {
		t.Errorf("healthcheck test = %v, want 4 parts", api.Healthcheck.Test.Parts)
	}
}

func TestParseComposition_TwoServices_APIVolumes(t *testing.T) {
	comp := loadTwoServicesComposition(t)
	api := comp.Services["api"]
	if len(api.Volumes) != 2 {
		t.Errorf("api volumes = %v, want 2 entries", api.Volumes)
	}
}

func TestParseComposition_TwoServices_NamedVolumes(t *testing.T) {
	comp := loadTwoServicesComposition(t)
	if len(comp.Volumes) != 1 {
		t.Errorf("expected 1 volume, got %d", len(comp.Volumes))
	}
	if _, ok := comp.Volumes["api-data"]; !ok {
		t.Error("missing volume 'api-data'")
	}
}

func TestCommandValue_String(t *testing.T) {
	var cv CommandValue
	err := cv.UnmarshalJSON([]byte(`"echo hello"`))
	if err != nil {
		t.Fatalf("unmarshal string: %v", err)
	}
	if len(cv.Parts) != 1 || cv.Parts[0] != "echo hello" {
		t.Errorf("parts = %v, want [echo hello]", cv.Parts)
	}
}

func TestCommandValue_Array(t *testing.T) {
	var cv CommandValue
	err := cv.UnmarshalJSON([]byte(`["echo", "hello"]`))
	if err != nil {
		t.Fatalf("unmarshal array: %v", err)
	}
	if len(cv.Parts) != 2 || cv.Parts[0] != "echo" || cv.Parts[1] != "hello" {
		t.Errorf("parts = %v, want [echo hello]", cv.Parts)
	}
}

func TestCommandValue_Null(t *testing.T) {
	var cv CommandValue
	err := cv.UnmarshalJSON([]byte(`null`))
	if err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if !cv.IsEmpty() {
		t.Error("expected empty after null")
	}
}

func TestDependsOnValue_List(t *testing.T) {
	var dv DependsOnValue
	err := dv.UnmarshalJSON([]byte(`["db", "redis"]`))
	if err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(dv.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(dv.Entries))
	}
	if dv.Entries["db"].Condition != "service_started" {
		t.Errorf("db condition = %q, want service_started", dv.Entries["db"].Condition)
	}
}

func TestDependsOnValue_Map(t *testing.T) {
	var dv DependsOnValue
	err := dv.UnmarshalJSON([]byte(`{"db": {"condition": "service_healthy"}}`))
	if err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	if dv.Entries["db"].Condition != "service_healthy" {
		t.Errorf("db condition = %q, want service_healthy", dv.Entries["db"].Condition)
	}
}

func TestParseComposition_InvalidJSON(t *testing.T) {
	_, err := ParseComposition([]byte(`{invalid`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCommandValue_MarshalJSON_Empty(t *testing.T) {
	cv := CommandValue{}
	data, err := cv.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("got %s, want null", string(data))
	}
}

func TestCommandValue_MarshalJSON_NonEmpty(t *testing.T) {
	cv := CommandValue{Parts: []string{"echo", "hello"}}
	data, err := cv.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `["echo","hello"]` {
		t.Errorf("got %s, want [\"echo\",\"hello\"]", string(data))
	}
}

func TestCommandValue_InvalidJSON(t *testing.T) {
	var cv CommandValue
	err := cv.UnmarshalJSON([]byte(`123`))
	if err == nil {
		t.Error("expected error for numeric value")
	}
}

func TestDependsOnValue_MarshalJSON_Empty(t *testing.T) {
	dv := DependsOnValue{}
	data, err := dv.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("got %s, want null", string(data))
	}
}

func TestDependsOnValue_MarshalJSON_NonEmpty(t *testing.T) {
	dv := DependsOnValue{
		Entries: map[string]DependsOnEntry{
			"db": {Condition: "service_healthy"},
		},
	}
	data, err := dv.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) == "null" {
		t.Error("should not marshal to null when entries exist")
	}
}

func TestDependsOnValue_InvalidJSON(t *testing.T) {
	var dv DependsOnValue
	err := dv.UnmarshalJSON([]byte(`123`))
	if err == nil {
		t.Error("expected error for numeric value")
	}
}

func TestDependsOnValue_Null(t *testing.T) {
	var dv DependsOnValue
	err := dv.UnmarshalJSON([]byte(`null`))
	if err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if !dv.IsEmpty() {
		t.Error("expected empty after null")
	}
}

func loadHostStoreComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("host-store.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestParseComposition_HostStore_UseHostStore(t *testing.T) {
	comp := loadHostStoreComposition(t)
	app := comp.Services["app"]
	if app.XNixCompose == nil {
		t.Fatal("missing x-nix-compose")
		return
	}
	if !app.XNixCompose.UseHostStore {
		t.Error("useHostStore should be true")
	}
}

func TestParseComposition_HostStore_NixStorePaths(t *testing.T) {
	comp := loadHostStoreComposition(t)
	app := comp.Services["app"]
	if len(app.XNixCompose.NixStorePaths) != 2 {
		t.Fatalf("expected 2 nix store paths, got %d", len(app.XNixCompose.NixStorePaths))
	}
	if app.XNixCompose.NixStorePaths[0] != "/nix/store/abc123-myapp" {
		t.Errorf("first path = %q, want /nix/store/abc123-myapp", app.XNixCompose.NixStorePaths[0])
	}
}

func loadWithProfilesComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("with-profiles.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestParseComposition_Profiles_TopLevel(t *testing.T) {
	comp := loadWithProfilesComposition(t)
	web := comp.Services["web"]
	if len(web.Profiles) != 1 || web.Profiles[0] != "frontend" {
		t.Errorf("web profiles = %v, want [frontend]", web.Profiles)
	}
	api := comp.Services["api"]
	if len(api.Profiles) != 1 || api.Profiles[0] != "backend" {
		t.Errorf("api profiles = %v, want [backend]", api.Profiles)
	}
}

func TestParseComposition_Profiles_NoProfile(t *testing.T) {
	comp := loadWithProfilesComposition(t)
	worker := comp.Services["worker"]
	if len(worker.Profiles) != 0 {
		t.Errorf("worker should have no profiles, got %v", worker.Profiles)
	}
}

func TestParseComposition_Profiles_Legacy(t *testing.T) {
	data := []byte(`{"services":{"web":{"image":"nginx","x-nix-compose":{"profiles":["frontend"]}}}}`)
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	web := comp.Services["web"]
	if web.XNixCompose == nil || len(web.XNixCompose.Profiles) != 1 || web.XNixCompose.Profiles[0] != "frontend" {
		t.Error("legacy x-nix-compose.profiles should still parse")
	}
}

// --- Resource limits ---

func loadResourceLimitsComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("resource-limits.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestParseComposition_ResourceLimits(t *testing.T) {
	comp := loadResourceLimitsComposition(t)
	api := comp.Services["api"]
	if api.XNixCompose == nil || api.XNixCompose.Resources == nil {
		t.Fatal("missing resources")
		return
	}
	r := api.XNixCompose.Resources
	if r.Limits == nil {
		t.Fatal("missing limits")
		return
	}
	if r.Limits.CPU != "1.0" {
		t.Errorf("limits.cpu = %q, want 1.0", r.Limits.CPU)
	}
	if r.Limits.Memory != "512M" {
		t.Errorf("limits.memory = %q, want 512M", r.Limits.Memory)
	}
	if r.Requests == nil {
		t.Fatal("missing requests")
		return
	}
	if r.Requests.CPU != "0.25" {
		t.Errorf("requests.cpu = %q, want 0.25", r.Requests.CPU)
	}
	if r.Requests.Memory != "128M" {
		t.Errorf("requests.memory = %q, want 128M", r.Requests.Memory)
	}
}

// --- Probes ---

func loadProbesComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("probes.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestParseComposition_Probes_Liveness_HTTPGet(t *testing.T) {
	comp := loadProbesComposition(t)
	api := comp.Services["api"]
	if api.XNixCompose == nil || api.XNixCompose.Probes == nil {
		t.Fatal("missing probes")
		return
	}
	lp := api.XNixCompose.Probes.Liveness
	if lp == nil {
		t.Fatal("missing liveness probe")
		return
	}
	if lp.HTTPGet == nil {
		t.Fatal("missing httpGet")
		return
	}
	if lp.HTTPGet.Path != "/healthz" {
		t.Errorf("path = %q, want /healthz", lp.HTTPGet.Path)
	}
	if lp.HTTPGet.Port != 3000 {
		t.Errorf("port = %d, want 3000", lp.HTTPGet.Port)
	}
}

func TestParseComposition_Probes_Liveness_Timing(t *testing.T) {
	comp := loadProbesComposition(t)
	lp := comp.Services["api"].XNixCompose.Probes.Liveness
	if lp.InitialDelaySeconds != 10 {
		t.Errorf("initialDelaySeconds = %d, want 10", lp.InitialDelaySeconds)
	}
	if lp.PeriodSeconds != 30 {
		t.Errorf("periodSeconds = %d, want 30", lp.PeriodSeconds)
	}
	if lp.TimeoutSeconds != 5 {
		t.Errorf("timeoutSeconds = %d, want 5", lp.TimeoutSeconds)
	}
	if lp.FailureThreshold != 3 {
		t.Errorf("failureThreshold = %d, want 3", lp.FailureThreshold)
	}
}

func TestParseComposition_Probes_Readiness(t *testing.T) {
	comp := loadProbesComposition(t)
	api := comp.Services["api"]
	rp := api.XNixCompose.Probes.Readiness
	if rp == nil {
		t.Fatal("missing readiness probe")
		return
	}
	if rp.Exec == nil {
		t.Fatal("missing exec")
		return
	}
	if len(rp.Exec.Command) != 2 || rp.Exec.Command[0] != "cat" {
		t.Errorf("exec command = %v, want [cat /tmp/ready]", rp.Exec.Command)
	}
	if rp.PeriodSeconds != 10 {
		t.Errorf("periodSeconds = %d, want 10", rp.PeriodSeconds)
	}
}

// --- Named Ports ---

func loadNamedPortsComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("named-ports.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestParseComposition_NamedPorts(t *testing.T) {
	comp := loadNamedPortsComposition(t)
	api := comp.Services["api"]
	if api.XNixCompose == nil {
		t.Fatal("missing x-nix-compose")
		return
	}
	nps := api.XNixCompose.NamedPorts
	if len(nps) != 6 {
		t.Fatalf("expected 6 named ports, got %d", len(nps))
	}
	if nps[0].Name != "http" || nps[0].ContainerPort != 3000 || nps[0].HostPort != 8080 {
		t.Errorf("port[0] = %+v, want http:3000:8080", nps[0])
	}
	if nps[2].Protocol != "udp" {
		t.Errorf("port[2].protocol = %q, want udp", nps[2].Protocol)
	}
	if nps[3].HostPort != 0 {
		t.Errorf("port[3].hostPort = %d, want 0 (unset)", nps[3].HostPort)
	}
}

func TestParseComposition_NamedPorts_HostIP(t *testing.T) {
	comp := loadNamedPortsComposition(t)
	nps := comp.Services["api"].XNixCompose.NamedPorts
	if nps[4].Name != "admin" || nps[4].HostIP != "127.0.0.1" || nps[4].HostPort != 8081 || nps[4].ContainerPort != 8081 {
		t.Errorf("port[4] = %+v, want admin:127.0.0.1:8081:8081", nps[4])
	}
	if nps[5].Name != "internal" || nps[5].HostIP != "127.0.0.1" || nps[5].HostPort != 0 || nps[5].ContainerPort != 8082 {
		t.Errorf("port[5] = %+v, want internal:127.0.0.1::8082", nps[5])
	}
}

// --- Init Containers ---

func loadInitContainersComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("init-containers.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestParseComposition_InitContainers(t *testing.T) {
	comp := loadInitContainersComposition(t)
	api := comp.Services["api"]
	if api.XNixCompose == nil {
		t.Fatal("missing x-nix-compose")
		return
	}
	ics := api.XNixCompose.InitContainers
	if len(ics) != 2 {
		t.Fatalf("expected 2 init containers, got %d", len(ics))
	}
	if ics[0].Name != "migrate" {
		t.Errorf("initContainers[0].name = %q, want migrate", ics[0].Name)
	}
	if ics[0].Image != "flyway:latest" {
		t.Errorf("initContainers[0].image = %q, want flyway:latest", ics[0].Image)
	}
	if len(ics[0].Command.Parts) != 2 || ics[0].Command.Parts[0] != "flyway" {
		t.Errorf("initContainers[0].command = %v, want [flyway migrate]", ics[0].Command.Parts)
	}
	if ics[0].Environment["DB_URL"] != "postgres://db:5432/app" {
		t.Errorf("initContainers[0].env.DB_URL = %q", ics[0].Environment["DB_URL"])
	}
	if len(ics[0].Volumes) != 1 {
		t.Errorf("initContainers[0].volumes = %v, want 1 entry", ics[0].Volumes)
	}
	if ics[1].Name != "seed" {
		t.Errorf("initContainers[1].name = %q, want seed", ics[1].Name)
	}
}

// --- New fields: tmpfs, hostname, build, per-service networks ---

func TestParseComposition_Tmpfs(t *testing.T) {
	data := []byte(`{"services":{"app":{"image":"loki","tmpfs":["/tmp","/run"]}}}`)
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(comp.Services["app"].Tmpfs) != 2 || comp.Services["app"].Tmpfs[0] != "/tmp" {
		t.Errorf("tmpfs = %v, want [/tmp /run]", comp.Services["app"].Tmpfs)
	}
}

func TestParseComposition_Hostname(t *testing.T) {
	data := []byte(`{"services":{"web":{"image":"nginx","hostname":"web-host"}}}`)
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if comp.Services["web"].Hostname != "web-host" {
		t.Errorf("hostname = %q, want web-host", comp.Services["web"].Hostname)
	}
}

func TestParseComposition_Build(t *testing.T) {
	data := []byte(`{"services":{"app":{"build":{"context":"./app","dockerfile":"Dockerfile.prod","args":{"VER":"1"},"target":"runtime"}}}}`)
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := comp.Services["app"].Build
	if b == nil {
		t.Fatal("expected non-nil build")
		return
	}
	if b.Context != "./app" {
		t.Errorf("context = %q, want ./app", b.Context)
	}
	if b.Dockerfile != "Dockerfile.prod" {
		t.Errorf("dockerfile = %q, want Dockerfile.prod", b.Dockerfile)
	}
	if b.Target != "runtime" {
		t.Errorf("target = %q, want runtime", b.Target)
	}
	if b.Args["VER"] != "1" {
		t.Errorf("args = %v, want VER=1", b.Args)
	}
}

func TestServiceNetworks_ListForm(t *testing.T) {
	var sn ServiceNetworks
	err := sn.UnmarshalJSON([]byte(`["frontend","backend"]`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sn.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(sn.Entries))
	}
	if _, ok := sn.Entries["frontend"]; !ok {
		t.Error("missing frontend")
	}
}

func TestServiceNetworks_MapForm(t *testing.T) {
	var sn ServiceNetworks
	err := sn.UnmarshalJSON([]byte(`{"frontend":{"aliases":["web"]},"backend":{}}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sn.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(sn.Entries))
	}
	if len(sn.Entries["frontend"].Aliases) != 1 || sn.Entries["frontend"].Aliases[0] != "web" {
		t.Errorf("frontend aliases = %v, want [web]", sn.Entries["frontend"].Aliases)
	}
}

func TestServiceNetworks_Null(t *testing.T) {
	var sn ServiceNetworks
	err := sn.UnmarshalJSON([]byte(`null`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !sn.IsEmpty() {
		t.Error("expected empty after null")
	}
}

func TestServiceNetworks_Invalid(t *testing.T) {
	var sn ServiceNetworks
	err := sn.UnmarshalJSON([]byte(`123`))
	if err == nil {
		t.Error("expected error for numeric value")
	}
}

func TestServiceNetworks_MarshalJSON_Empty(t *testing.T) {
	sn := ServiceNetworks{}
	data, err := sn.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("got %s, want null", string(data))
	}
}

func TestServiceNetworks_MarshalJSON_WithEntries(t *testing.T) {
	sn := ServiceNetworks{
		Entries: map[string]ServiceNetworkConfig{
			"frontend": {Aliases: []string{"web"}},
			"backend":  {},
		},
	}
	data, err := sn.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) == "null" {
		t.Error("expected non-null JSON for non-empty entries")
	}
	// Verify it round-trips.
	var parsed map[string]ServiceNetworkConfig
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if len(parsed) != 2 {
		t.Errorf("expected 2 entries, got %d", len(parsed))
	}
	if len(parsed["frontend"].Aliases) != 1 || parsed["frontend"].Aliases[0] != "web" {
		t.Errorf("frontend aliases = %v, want [web]", parsed["frontend"].Aliases)
	}
}

// --- EnvFrom ---

func loadEnvFromComposition(t *testing.T) *Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath("envfrom.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestParseComposition_EnvFrom(t *testing.T) {
	comp := loadEnvFromComposition(t)
	api := comp.Services["api"]
	if api.XNixCompose == nil {
		t.Fatal("missing x-nix-compose")
		return
	}
	ef := api.XNixCompose.EnvFrom
	if len(ef) != 2 {
		t.Fatalf("expected 2 envFrom sources, got %d", len(ef))
	}
	if ef[0].SecretFile != "secrets/api.env" {
		t.Errorf("envFrom[0].secretFile = %q, want secrets/api.env", ef[0].SecretFile)
	}
	if ef[0].Prefix != "" {
		t.Errorf("envFrom[0].prefix = %q, want empty", ef[0].Prefix)
	}
	if ef[1].Prefix != "APP_" {
		t.Errorf("envFrom[1].prefix = %q, want APP_", ef[1].Prefix)
	}
}
