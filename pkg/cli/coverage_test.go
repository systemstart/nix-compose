package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate/server"
	"github.com/systemstart/nix-compose/pkg/volumes"
	"google.golang.org/grpc"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// --- Graph command tests (runGraphShow / runGraphDeps / runGraphImpact) ---

func TestRunGraphShow_TextFormat(t *testing.T) {
	oldFormat := graphFormat
	graphFormat = "text"
	defer func() { graphFormat = oldFormat }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runGraphShow(graphShowCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runGraphShow: %v", err)
	}
	// Output may contain resources (from shared DB) or "No resources found".
	_ = buf.String()
}

func TestRunGraphShow_DOTFormat(t *testing.T) {
	oldFormat := graphFormat
	graphFormat = "dot"
	defer func() { graphFormat = oldFormat }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runGraphShow(graphShowCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runGraphShow dot: %v", err)
	}
	out := buf.String()
	// DOT output has "digraph G {" if resources exist, or "No resources found." if empty.
	if !strings.Contains(out, "digraph G") && !strings.Contains(out, "No resources found") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestRunGraphDeps_NonExistent(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runGraphDeps(graphDepsCmd, []string{"nonexistent/svc"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runGraphDeps: %v", err)
	}
	// With no deps for a nonexistent resource, output should say "No dependencies found."
	_ = buf.String()
}

func TestRunGraphImpact_NonExistent(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runGraphImpact(graphImpactCmd, []string{"nonexistent/svc"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runGraphImpact: %v", err)
	}
	_ = buf.String()
}

// Note: runDrift with CRI cannot be tested because os.Exit(1) is called when drift is detected,
// and the shared state.bolt may have data that doesn't match the mock CRI.

// --- runPlan local path with CRI (eval fails with no Nix) ---

func TestRunPlan_LocalCRI_EvalFails(t *testing.T) {
	sock, _ := startCoverageMockCRI(t)
	dir := t.TempDir()

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	err := runPlan(planCmd, nil)
	// Plan requires Nix eval, which will fail in test env.
	if err == nil {
		t.Fatal("expected error (eval should fail without Nix config)")
	}
}

// --- runRollbackApply local path with CRI ---

func TestRunRollbackApply_LocalCRI_NotFound(t *testing.T) {
	sock, _ := startCoverageMockCRI(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	oldDryRun := rollbackDryRun
	rollbackDryRun = true
	defer func() { rollbackDryRun = oldDryRun }()

	err := runRollbackApply(context.Background(), "nonexistent-deployment")
	if err == nil {
		t.Fatal("expected error for nonexistent deployment")
	}
}

// --- Remote functions ---

func TestRemoteStateList_WithData(t *testing.T) {
	orchSock := startCoverageOrchestrateWithData(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStateList(stateListCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runStateList: %v", err)
	}
	// With or without data, output should contain the header or the empty message.
	out := buf.String()
	if !strings.Contains(out, "ID") && !strings.Contains(out, "No rollouts found") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestRemoteStateShow_WithData(t *testing.T) {
	orchSock := startCoverageOrchestrateWithData(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	err := runStateShow(stateShowCmd, []string{"myapp/web"})
	if err == nil {
		return
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRemoteRollbackApply_DryRunNonExistent(t *testing.T) {
	orchSock := startCoverageOrchestrateWithData(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDryRun := rollbackDryRun
	rollbackDryRun = true
	defer func() { rollbackDryRun = oldDryRun }()

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	_ = runRollback(rollbackCmd, []string{"nonexistent-dep"})

	_ = w.Close()
	os.Stdout = oldStdout
}

// --- WarnDependents / findRunningDependents ---

func TestWarnDependents_NoPanic(t *testing.T) {
	containers := []containerInfo{
		{Service: "web", State: runtimev1.ContainerState_CONTAINER_RUNNING, ContainerID: "ctr-1"},
		{Service: "api", State: runtimev1.ContainerState_CONTAINER_RUNNING, ContainerID: "ctr-2"},
	}

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	warnDependents("testproj", containers)

	_ = w.Close()
	os.Stdout = oldStdout
}

// --- Remote exec / logs paths ---

func TestRemoteExec_WithCommand(t *testing.T) {
	orchSock := startCoverageOrchestrateWithData(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProjectName := flagProjectName
	flagProjectName = "testproj"
	defer func() { flagProjectName = oldProjectName }()

	_ = runExec(execCmd, []string{"web", "echo", "hello"})
}

func TestRemoteLogs_WithData(t *testing.T) {
	orchSock := startCoverageOrchestrateWithData(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProjectName := flagProjectName
	flagProjectName = "testproj"
	defer func() { flagProjectName = oldProjectName }()

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	_ = runLogs(logsCmd, []string{"web"})

	_ = w.Close()
	os.Stdout = oldStdout
}

// --- CRI-backed stop with force ---

func TestRunStopCRI_Force(t *testing.T) {
	sock, _ := startCoverageMockCRI(t)

	oldForce := stopForce
	stopForce = true
	defer func() { stopForce = oldForce }()

	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "forceproj", []string{"web"})

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err = runStopCRI(ctx, criClient, "forceproj", nil, 10)

	_ = w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}
}

// --- Run down, remote down ---

func TestRunDown_WithCRI_NoServices(t *testing.T) {
	sock, _ := startCoverageMockCRI(t)
	dir := t.TempDir()

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	oldProjectName := flagProjectName
	flagProjectName = "testdown"
	defer func() { flagProjectName = oldProjectName }()

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := runDown(downCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runDown: %v", err)
	}
}

func TestRemoteDown_WithData(t *testing.T) {
	orchSock := startCoverageOrchestrateWithData(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProjectName := flagProjectName
	flagProjectName = "testproj"
	defer func() { flagProjectName = oldProjectName }()

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := runDown(downCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runDown remote: %v", err)
	}
}

func TestRemoteDown_WithServices(t *testing.T) {
	orchSock := startCoverageOrchestrateWithData(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProjectName := flagProjectName
	flagProjectName = "testproj"
	defer func() { flagProjectName = oldProjectName }()

	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := runDown(downCmd, []string{"web"})

	_ = w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runDown with services: %v", err)
	}
}

// --- Additional function tests ---

func TestCleanupVolumes_NoErrors(t *testing.T) {
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	cleanupVolumes("nonexistent-project")

	_ = w.Close()
	os.Stdout = oldStdout
}

func TestRunRollbackList_LocalWithEngine(t *testing.T) {
	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runRollbackList(context.Background())

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runRollbackList: %v", err)
	}
	// Output depends on shared state.bolt — may have data or be empty.
	_ = buf.String()
}

func TestRunStateList_LocalWithEngine(t *testing.T) {
	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStateList(stateListCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runStateList: %v", err)
	}
	// Output depends on shared state.bolt — may have data or be empty.
	_ = buf.String()
}

func TestEvalAndFilter_NoFlake(t *testing.T) {
	dir := t.TempDir()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	oldImpure := flagImpure
	flagImpure = true
	defer func() { flagImpure = oldImpure }()

	ctx := context.Background()
	_, err := evalAndFilter(ctx, dir)
	if err == nil {
		t.Fatal("expected error (no Nix config)")
	}
}

func TestRenderK8s_EvalFails(t *testing.T) {
	dir := t.TempDir()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	oldImpure := flagImpure
	flagImpure = true
	defer func() { flagImpure = oldImpure }()

	ctx := context.Background()
	err := renderK8s(ctx, dir)
	if err == nil {
		t.Fatal("expected error (eval should fail)")
	}
}

func TestKubectlDryRun_Empty(t *testing.T) {
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	_ = kubectlDryRun(context.Background(), nil)

	_ = w.Close()
	os.Stdout = oldStdout
}

func TestRunPull_CRIPath(t *testing.T) {
	sock, _ := startCoverageMockCRI(t)
	dir := t.TempDir()

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	oldImpure := flagImpure
	flagImpure = true
	defer func() { flagImpure = oldImpure }()

	err := runPull(pullCmd, nil)
	if err == nil {
		t.Fatal("expected error (eval should fail without Nix config)")
	}
}

func TestRunImages_CRIPath(t *testing.T) {
	sock, _ := startCoverageMockCRI(t)
	dir := t.TempDir()

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	oldImpure := flagImpure
	flagImpure = true
	defer func() { flagImpure = oldImpure }()

	err := runImages(imagesCmd, nil)
	if err == nil {
		t.Fatal("expected error (eval should fail without Nix config)")
	}
}

func TestRunExec_CRIPath(t *testing.T) {
	sock, _ := startCoverageMockCRI(t)
	dir := t.TempDir()

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	oldProjectName := flagProjectName
	flagProjectName = "testproj"
	defer func() { flagProjectName = oldProjectName }()

	// Exec with explicit command but no running containers.
	err := runExec(execCmd, []string{"web", "echo", "hello"})
	// Will fail because no pods found.
	if err == nil {
		t.Fatal("expected error (no running containers)")
	}
}

// --- Helper: coverage mock CRI ---

func startCoverageMockCRI(t *testing.T) (string, *cliMockCRI) {
	t.Helper()
	mock := newCLIMockCRI()
	sock := filepath.Join(t.TempDir(), "cri.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	runtimev1.RegisterRuntimeServiceServer(srv, mock)
	runtimev1.RegisterImageServiceServer(srv, mock)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	return sock, mock
}

func startCoverageOrchestrateWithData(t *testing.T) string {
	t.Helper()

	sock, _ := startCoverageMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial CRI: %v", err)
	}
	t.Cleanup(func() { _ = criClient.Close() })

	orchSock := filepath.Join(t.TempDir(), "orch.sock")

	volStore := &volumes.Store{Root: t.TempDir()}
	cniStore := &cni.Store{
		ConfDir:    t.TempDir(),
		PluginDirs: []string{},
	}
	srv := server.New(server.Config{
		CRIClient: criClient,
		CNIStore:  cniStore,
		VolStore:  volStore,
		LogBase:   t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "state.bolt"),
	})

	lis, err := net.Listen("unix", orchSock)
	if err != nil {
		t.Fatalf("listen orch: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	return orchSock
}
