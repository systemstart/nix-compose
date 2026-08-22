package manifest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest represents a single resource declaration in the standard envelope format.
type Manifest struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       any      `yaml:"spec" json:"spec"`
}

// Metadata holds resource identification fields.
type Metadata struct {
	Name      string            `yaml:"name" json:"name"`
	Namespace string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// Validate checks that required fields are present.
func (m *Manifest) Validate() []error {
	var errs []error
	if m.APIVersion == "" {
		errs = append(errs, fmt.Errorf("apiVersion is required"))
	}
	if m.Kind == "" {
		errs = append(errs, fmt.Errorf("kind is required"))
	}
	if m.Metadata.Name == "" {
		errs = append(errs, fmt.Errorf("metadata.name is required"))
	}
	return errs
}

// LoadFile reads a YAML file and returns all manifests found in it.
// Supports multi-document YAML files separated by "---".
func LoadFile(path string) ([]Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var manifests []Manifest
	decoder := yaml.NewDecoder(f)
	for {
		var m Manifest
		err := decoder.Decode(&m)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		// Skip empty documents (e.g. trailing "---")
		if m.APIVersion == "" && m.Kind == "" && m.Metadata.Name == "" {
			continue
		}
		manifests = append(manifests, m)
	}
	return manifests, nil
}

// LoadPath loads manifests from a file or directory (recursive).
// YAML files are identified by .yaml and .yml extensions.
func LoadPath(path string) ([]Manifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	if !info.IsDir() {
		return LoadFile(path)
	}

	var all []Manifest
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		manifests, err := LoadFile(p)
		if err != nil {
			return err
		}
		all = append(all, manifests...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", path, err)
	}
	return all, nil
}

// ValidateAll validates a slice of manifests and returns all errors grouped by manifest.
func ValidateAll(manifests []Manifest) map[string][]error {
	result := make(map[string][]error)
	for i, m := range manifests {
		errs := m.Validate()
		if len(errs) > 0 {
			key := fmt.Sprintf("document %d", i+1)
			if m.Metadata.Name != "" {
				key = m.Metadata.Name
			}
			result[key] = errs
		}
	}
	return result
}
