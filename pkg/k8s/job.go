package k8s

import (
	"github.com/systemstart/nix-compose/pkg/eval"
)

// convertJob converts a named service to a K8s Job manifest.
func convertJob(name string, svc eval.Service, compVolumes map[string]eval.Volume, opts RenderOptions) Manifest {
	labels := standardLabels(name)
	containers := []Container{buildMainContainer(name, svc, opts)}
	podVolumes := convertPodVolumes(svc.Volumes, compVolumes)

	restartPolicy := "Never"
	if svc.Restart == "on-failure" {
		restartPolicy = "OnFailure"
	}

	job := Job{
		TypeMeta: TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		Metadata: ObjectMeta{Name: name, Namespace: opts.Namespace, Labels: labels},
		Spec: JobSpec{
			Template: PodTemplateSpec{
				Metadata: ObjectMeta{Labels: labels},
				Spec: PodSpec{
					RestartPolicy:  restartPolicy,
					InitContainers: convertInitContainers(svc),
					Containers:     containers,
					Volumes:        podVolumes,
				},
			},
		},
	}
	return Manifest{Object: job, Filename: name + "-job.yaml"}
}
