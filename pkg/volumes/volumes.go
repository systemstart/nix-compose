package volumes

import (
	"fmt"
	"os"
	"path/filepath"
)

// Store manages named volume directories on disk.
// Volumes live at Root/{project}/{name}/.
type Store struct {
	Root string
}

// NewStore creates a Store using the default root directory
// (~/.local/share/nix-compose/volumes/).
func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("determining home directory: %w", err)
	}
	return &Store{Root: filepath.Join(home, ".local", "share", "nix-compose", "volumes")}, nil
}

// Path returns the absolute path for a named volume (pure computation, no I/O).
func (s *Store) Path(project, name string) string {
	return filepath.Join(s.Root, project, name)
}

// Ensure creates the volume directory if it doesn't exist and returns the absolute path.
func (s *Store) Ensure(project, name string) (string, error) {
	p := s.Path(project, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", fmt.Errorf("creating volume directory %s: %w", p, err)
	}
	return p, nil
}

// Remove removes a single named volume directory.
func (s *Store) Remove(project, name string) error {
	p := s.Path(project, name)
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("removing volume %s/%s: %w", project, name, err)
	}
	return nil
}

// RemoveAll removes all volumes for a project.
func (s *Store) RemoveAll(project string) error {
	p := filepath.Join(s.Root, project)
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("removing volumes for project %s: %w", project, err)
	}
	return nil
}

// List returns the names of all volumes for a project.
// Returns nil if the project directory doesn't exist.
func (s *Store) List(project string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, project))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing volumes for %s: %w", project, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
