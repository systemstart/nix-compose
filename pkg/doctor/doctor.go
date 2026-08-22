// Package doctor checks whether this machine can actually run a composition.
//
// Every check here exists because the problem it detects cost somebody hours.
// Container runtimes fail in ways that name the symptom and not the cause: a
// missing iptables on the *runtime's* PATH surfaces as "failed to locate
// iptables" during pod setup, and a systemd cgroup driver surfaces as
// "expected cgroupsPath to be of format slice:prefix:name". Neither says what
// to do. The point of this command is that each finding carries the fix.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/logs"
)

// Status is how a check came out.
type Status int

const (
	OK   Status = iota // works
	Warn               // works, but something will surprise you later
	Fail               // will not work
)

func (s Status) Symbol() string {
	switch s {
	case OK:
		return "✓"
	case Warn:
		return "!"
	default:
		return "✗"
	}
}

// Check is one finding. Detail says what is true; Fix says what to do about
// it, and is empty when there is nothing to do.
type Check struct {
	Name   string
	Status Status
	Detail string
	Fix    string
}

// Report is the full set of findings.
type Report struct {
	Checks []Check
}

// Failed reports whether anything will actually prevent a composition running.
func (r *Report) Failed() int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == Fail {
			n++
		}
	}
	return n
}

func (r *Report) add(name string, status Status, detail, fix string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail, Fix: fix})
}

// Run performs every check. It never returns an error: a check that cannot be
// performed is itself a finding.
func Run(ctx context.Context, socketOverride string) *Report {
	r := &Report{}

	client := r.checkCRISocket(ctx, socketOverride)
	if client != nil {
		defer func() { _ = client.Close() }()
		r.checkCgroupDriver(ctx, client)
	}

	r.checkNix(ctx)

	// iptables only bites once CNI plugins exist to invoke it, so the two
	// are reported together rather than as independent failures.
	haveCNI := r.checkCNIPlugins()
	r.checkIptables(haveCNI)

	r.checkLogReadability()

	return r
}

// checkCRISocket is the check everything else depends on. It returns a
// connected client when there is one, so later checks can ask the runtime
// rather than guess.
func (r *Report) checkCRISocket(ctx context.Context, override string) *cri.Client {
	paths := cri.DefaultSocketPaths
	if override != "" {
		paths = []string{override}
	}

	client, err := cri.DetectWithPaths(ctx, paths)
	if err != nil {
		r.add("CRI socket", Fail,
			fmt.Sprintf("no runtime responded (tried %s)", strings.Join(paths, ", ")),
			"start containerd or CRI-O, or point --cri-socket at its socket. "+
				"There is no fallback backend — a runtime is required")
		return nil
	}

	version, err := client.Version(ctx)
	if err != nil {
		r.add("CRI socket", Warn, client.Socket()+" connected but did not answer Version",
			"the socket may belong to a runtime that is still starting")
		return client
	}

	r.add("CRI socket", OK,
		fmt.Sprintf("%s (%s %s)", client.Socket(), version.RuntimeName, version.RuntimeVersion), "")
	return client
}

func (r *Report) checkCgroupDriver(ctx context.Context, client *cri.Client) {
	switch driver := client.CgroupDriver(ctx); driver {
	case "systemd":
		r.add("cgroup driver", OK,
			fmt.Sprintf("systemd — pods are placed under %s", cri.SystemdSlice), "")
	case "cgroupfs":
		r.add("cgroup driver", OK, "cgroupfs — the runtime default is used", "")
	default:
		r.add("cgroup driver", Warn,
			"the runtime does not report one (no RuntimeConfig RPC)",
			"pods use the runtime default. If they fail with \"expected "+
				"cgroupsPath to be of format slice:prefix:name\", the runtime "+
				"is on the systemd driver and is too old to say so")
	}
}

// checkNix matters only for `package:` — a project naming registry images runs
// without Nix at all — so its absence is a warning rather than a failure.
func (r *Report) checkNix(ctx context.Context) {
	path, err := exec.LookPath("nix")
	if err != nil {
		r.add("nix", Warn, "not on PATH",
			"registry images still work; `package:` and flake projects need it")
		return
	}

	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		r.add("nix", Warn, path+" is not runnable",
			"registry images still work; `package:` and flake projects need it")
		return
	}
	version := strings.TrimSpace(string(out))

	// Flakes are not optional here: `package:` resolution uses getFlake.
	if !flakesEnabled(ctx, path) {
		r.add("nix", Fail, version+", flakes disabled",
			"add `experimental-features = nix-command flakes` to nix.conf — "+
				"`package:` cannot be resolved without them")
		return
	}
	r.add("nix", OK, version+", flakes enabled", "")
}

func flakesEnabled(ctx context.Context, nixPath string) bool {
	out, err := exec.CommandContext(ctx, nixPath, "config", "show").Output()
	if err != nil {
		// Older nix spells it differently; assume enabled rather than
		// reporting a failure we are not sure about.
		return true
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "experimental-features") {
			return strings.Contains(line, "flakes")
		}
	}
	return false
}

// checkCNIPlugins reports whether per-project networking is available, and
// returns whether the plugins are there. Their absence is a warning rather
// than a failure: nix-compose falls back to host networking and the
// composition still runs — it is `ports:` that quietly stops meaning anything,
// which is exactly the kind of surprise worth naming up front.
func (r *Report) checkCNIPlugins() bool {
	store := cni.NewStore()
	missing := store.CheckPlugins()

	if len(missing) == 0 {
		r.add("CNI plugins", OK, "all present", "")
		return true
	}

	r.add("CNI plugins", Warn,
		fmt.Sprintf("missing: %s", strings.Join(missing, ", ")),
		fmt.Sprintf("containers fall back to host networking, so `ports:` is "+
			"not mapped and services reach each other only via localhost. "+
			"Install cni-plugins and dnsname-cni, or set CNI_PATH (searched: %s)",
			strings.Join(store.PluginDirs, ", ")))
	return false
}

// checkIptables is the subtle one. iptables being on *our* PATH proves
// nothing: the CNI bridge plugin is executed by the runtime, with the
// runtime's environment. On NixOS the two routinely differ.
func (r *Report) checkIptables(haveCNI bool) {
	_, ourErr := exec.LookPath("iptables")

	// Severity depends on whether anything would call it yet. With no CNI
	// plugins installed, bridge networking is already unavailable for another
	// reason, and a second failure line would just be noise.
	missingStatus := Fail
	if !haveCNI {
		missingStatus = Warn
	}

	runtimePath, found := containerdPath()
	if !found {
		detail := "on this shell's PATH; the runtime's PATH could not be read"
		if ourErr != nil {
			detail = "not on this shell's PATH, and the runtime's could not be read"
		}
		r.add("iptables", Warn, detail,
			"what matters is the *runtime's* PATH, since it executes the CNI "+
				"plugin — reading it needs the same user the runtime runs as. "+
				"If pod setup fails with \"failed to locate iptables\", add it "+
				"to the containerd unit's PATH")
		return
	}

	if onPath(runtimePath, "iptables") {
		r.add("iptables", OK, "on the runtime's PATH", "")
		return
	}

	detail := "not on the runtime's PATH"
	if ourErr == nil {
		detail += " (it is on this shell's, which does not help)"
	}
	r.add("iptables", missingStatus, detail,
		"containerd executes the CNI bridge plugin with its own environment, "+
			"so pod setup fails with \"failed to locate iptables\". Add it to "+
			"the containerd unit's PATH and restart it")
}

// containerdPath returns the PATH of a running containerd, if it can be read.
func containerdPath() (string, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", false
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(comm)) != "containerd" {
			continue
		}
		environ, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			// Running as another user without permission to read it.
			return "", false
		}
		for _, kv := range strings.Split(string(environ), "\x00") {
			if path, ok := strings.CutPrefix(kv, "PATH="); ok {
				return path, true
			}
		}
		return "", false
	}
	return "", false
}

func onPath(path, binary string) bool {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, binary)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// checkLogReadability catches the friction that makes `nix-compose logs`
// fail on a rootful runtime: containerd creates the log file 0640 root:root
// and never chowns it, so a non-root user cannot read what it wrote.
func (r *Report) checkLogReadability() {
	if os.Geteuid() == 0 {
		r.add("container logs", OK, "running as root; log files are readable", "")
		return
	}

	seen, unreadable, sample := probeLogs(logs.DefaultLogBase)

	// No log has been written yet — usually a machine that has not run a
	// container since boot. Saying "readable" here would be a claim this check
	// has no evidence for, and it is the claim that misleads: the finding is
	// most valuable precisely when someone is about to debug a container that
	// will not start.
	if !seen {
		r.add("container logs", Warn,
			"no container logs written yet — readability unverified",
			fmt.Sprintf("nothing under %s to test against. Re-run doctor after starting a "+
				"service; if `nix-compose logs` then prints nothing, this is why", logs.DefaultLogBase))
		return
	}

	if !unreadable {
		r.add("container logs", OK, "readable", "")
		return
	}

	r.add("container logs", Warn,
		fmt.Sprintf("%s is not readable by this user", sample),
		"containerd creates log files 0640 root:root without chowning them, so "+
			"`nix-compose logs` fails while the container itself is fine. Read "+
			"the exit status with `nix-compose ps`, or run as root")
}

// probeLogs looks for evidence rather than predicting. It reports whether any
// log file exists at all (seen) separately from whether one could not be
// opened (unreadable) — conflating the two is what let an empty log directory
// pass as proof of readability.
func probeLogs(base string) (seen, unreadable bool, sample string) {
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || unreadable {
			return nil //nolint:nilerr // an unwalkable tree is not a finding
		}
		if !strings.HasSuffix(path, ".log") {
			return nil
		}
		seen = true
		f, err := os.Open(path)
		if err != nil {
			unreadable = true
			sample = path
			return filepath.SkipAll
		}
		_ = f.Close()
		return nil
	})
	return seen, unreadable, sample
}
