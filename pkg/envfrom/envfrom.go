package envfrom

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// CommandRunner abstracts command execution for sops decryption.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error)
}

// Resolver resolves envFrom sources into environment variable maps.
type Resolver struct {
	ProjectDir string
	Runner     CommandRunner
}

// Resolve resolves a list of EnvFromSource entries into a merged env map.
func (r *Resolver) Resolve(ctx context.Context, sources []eval.EnvFromSource) (map[string]string, error) {
	merged := make(map[string]string)
	for _, src := range sources {
		data, err := r.loadSource(ctx, src)
		if err != nil {
			return nil, err
		}
		if data == nil {
			continue
		}

		env, err := parseEnvFile(data)
		if err != nil {
			return nil, fmt.Errorf("parsing env from source: %w", err)
		}

		for k, v := range applyPrefix(env, src.Prefix) {
			merged[k] = v
		}
	}
	return merged, nil
}

func (r *Resolver) loadSource(ctx context.Context, src eval.EnvFromSource) ([]byte, error) {
	switch {
	case src.SecretFile != "":
		path := r.resolvePath(src.SecretFile)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading secret file %q: %w", src.SecretFile, err)
		}
		return data, nil
	case src.SopsFile != "":
		return r.decryptSops(ctx, src.SopsFile)
	default:
		return nil, nil
	}
}

func (r *Resolver) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(r.ProjectDir, path)
}

func (r *Resolver) decryptSops(ctx context.Context, sopsFile string) ([]byte, error) {
	if r.Runner == nil {
		return nil, fmt.Errorf("sops decryption requires a command runner")
	}
	path := r.resolvePath(sopsFile)
	stdout, stderr, err := r.Runner.Run(ctx, "sops", "--decrypt", path)
	if err != nil {
		return nil, fmt.Errorf("sops decrypt %q: %s: %w", sopsFile, string(stderr), err)
	}
	return stdout, nil
}

// parseEnvFile parses a dotenv-format file into a key-value map.
// Supports comments (#), empty lines, and optional quoting.
func parseEnvFile(data []byte) (map[string]string, error) {
	env := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, val, ok := parseEnvLine(line); ok {
			env[key] = val
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning env file: %w", err)
	}
	return env, nil
}

// parseEnvLine parses a single KEY=VALUE line.
func parseEnvLine(line string) (string, string, bool) {
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	if key == "" {
		return "", "", false
	}
	val := stripQuotes(strings.TrimSpace(line[idx+1:]))
	return key, val, true
}

// stripQuotes removes surrounding single or double quotes.
func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// applyPrefix prepends a prefix to all keys in the map.
func applyPrefix(env map[string]string, prefix string) map[string]string {
	if prefix == "" {
		return env
	}
	result := make(map[string]string, len(env))
	for k, v := range env {
		result[prefix+k] = v
	}
	return result
}

// ResolveEnvFrom resolves envFrom sources for all services in a composition.
// Explicit environment variables on the service take precedence over envFrom values.
func ResolveEnvFrom(ctx context.Context, comp *eval.Composition, resolver *Resolver) error {
	if resolver == nil {
		return nil
	}
	for name, svc := range comp.Services {
		if svc.XNixCompose == nil || len(svc.XNixCompose.EnvFrom) == 0 {
			continue
		}

		resolved, err := resolver.Resolve(ctx, svc.XNixCompose.EnvFrom)
		if err != nil {
			return fmt.Errorf("service %q envFrom: %w", name, err)
		}

		// Merge: explicit vars win over envFrom.
		if svc.Environment == nil {
			svc.Environment = make(map[string]string)
		}
		for k, v := range resolved {
			if _, exists := svc.Environment[k]; !exists {
				svc.Environment[k] = v
			}
		}
		comp.Services[name] = svc
	}
	return nil
}
