package k8s

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// MarshalManifest serializes a single manifest to YAML bytes.
func MarshalManifest(m Manifest) ([]byte, error) {
	data, err := yaml.Marshal(m.Object)
	if err != nil {
		return nil, fmt.Errorf("marshaling manifest %s: %w", m.Filename, err)
	}
	return data, nil
}

// WriteMultiDoc writes all manifests as a multi-document YAML stream to w.
func WriteMultiDoc(w io.Writer, manifests []Manifest) error {
	for i, m := range manifests {
		if i > 0 {
			if _, err := fmt.Fprintln(w, "---"); err != nil {
				return fmt.Errorf("writing separator: %w", err)
			}
		}
		data, err := MarshalManifest(m)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("writing manifest %s: %w", m.Filename, err)
		}
	}
	return nil
}

// WriteDirectory writes each manifest as an individual file and generates kustomization.yaml.
func WriteDirectory(dir string, manifests []Manifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	filenames := make([]string, 0, len(manifests))
	for _, m := range manifests {
		data, err := MarshalManifest(m)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, m.Filename)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", m.Filename, err)
		}
		filenames = append(filenames, m.Filename)
	}

	return writeKustomization(dir, filenames)
}
