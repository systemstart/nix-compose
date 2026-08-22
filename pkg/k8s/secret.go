package k8s

// convertSecret converts resolved envFrom variables to a K8s Secret manifest.
// Returns nil if there are no resolved secrets.
func convertSecret(name string, resolvedEnv map[string]string, opts RenderOptions) *Manifest {
	if len(resolvedEnv) == 0 {
		return nil
	}

	secret := Secret{
		TypeMeta: TypeMeta{APIVersion: "v1", Kind: "Secret"},
		Metadata: ObjectMeta{
			Name:      name + "-secrets",
			Namespace: opts.Namespace,
			Labels:    standardLabels(name),
		},
		StringData: resolvedEnv,
	}
	return &Manifest{Object: secret, Filename: name + "-secret.yaml"}
}
