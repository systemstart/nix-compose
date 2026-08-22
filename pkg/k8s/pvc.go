package k8s

// convertPVC creates a PersistentVolumeClaim manifest for a named volume.
func convertPVC(name string, opts RenderOptions) Manifest {
	pvc := PersistentVolumeClaim{
		TypeMeta: TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		Metadata: ObjectMeta{
			Name:      name,
			Namespace: opts.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "nix-compose",
			},
		},
		Spec: PVCSpec{
			AccessModes: []string{"ReadWriteOnce"},
			Resources: PVCResourceRequests{
				Requests: map[string]string{"storage": "1Gi"},
			},
		},
	}
	return Manifest{Object: pvc, Filename: name + "-pvc.yaml"}
}
