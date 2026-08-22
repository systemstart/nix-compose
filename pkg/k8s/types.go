package k8s

// TypeMeta describes the API version and kind of a K8s resource.
type TypeMeta struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// ObjectMeta holds standard K8s object metadata.
type ObjectMeta struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

// Deployment represents a K8s Deployment resource.
type Deployment struct {
	TypeMeta `yaml:",inline"`
	Metadata ObjectMeta     `yaml:"metadata"`
	Spec     DeploymentSpec `yaml:"spec"`
}

// DeploymentSpec is the specification of a Deployment.
type DeploymentSpec struct {
	Replicas int             `yaml:"replicas"`
	Selector *LabelSelector  `yaml:"selector"`
	Template PodTemplateSpec `yaml:"template"`
}

// Job represents a K8s Job resource.
type Job struct {
	TypeMeta `yaml:",inline"`
	Metadata ObjectMeta `yaml:"metadata"`
	Spec     JobSpec    `yaml:"spec"`
}

// JobSpec is the specification of a Job.
type JobSpec struct {
	Template PodTemplateSpec `yaml:"template"`
}

// LabelSelector selects pods by labels.
type LabelSelector struct {
	MatchLabels map[string]string `yaml:"matchLabels"`
}

// PodTemplateSpec describes the pod that will be created.
type PodTemplateSpec struct {
	Metadata ObjectMeta `yaml:"metadata"`
	Spec     PodSpec    `yaml:"spec"`
}

// PodSpec defines the desired behaviour of a pod.
type PodSpec struct {
	RestartPolicy  string      `yaml:"restartPolicy,omitempty"`
	InitContainers []Container `yaml:"initContainers,omitempty"`
	Containers     []Container `yaml:"containers"`
	Volumes        []PodVolume `yaml:"volumes,omitempty"`
}

// Container represents a single container within a pod.
type Container struct {
	Name           string                `yaml:"name"`
	Image          string                `yaml:"image"`
	Command        []string              `yaml:"command,omitempty"`
	WorkingDir     string                `yaml:"workingDir,omitempty"`
	Ports          []ContainerPort       `yaml:"ports,omitempty"`
	Env            []EnvVar              `yaml:"env,omitempty"`
	EnvFrom        []EnvFromSource       `yaml:"envFrom,omitempty"`
	Resources      *ResourceRequirements `yaml:"resources,omitempty"`
	LivenessProbe  *ProbeSpec            `yaml:"livenessProbe,omitempty"`
	ReadinessProbe *ProbeSpec            `yaml:"readinessProbe,omitempty"`
	VolumeMounts   []VolumeMount         `yaml:"volumeMounts,omitempty"`
}

// ContainerPort represents a port exposed by a container.
type ContainerPort struct {
	Name          string `yaml:"name,omitempty"`
	ContainerPort int    `yaml:"containerPort"`
	Protocol      string `yaml:"protocol,omitempty"`
}

// EnvVar represents a single environment variable.
type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// EnvFromSource references a secret for env injection.
type EnvFromSource struct {
	SecretRef *SecretRef `yaml:"secretRef,omitempty"`
}

// SecretRef references a K8s Secret by name.
type SecretRef struct {
	Name string `yaml:"name"`
}

// ResourceRequirements describes compute resource requirements.
type ResourceRequirements struct {
	Limits   map[string]string `yaml:"limits,omitempty"`
	Requests map[string]string `yaml:"requests,omitempty"`
}

// ProbeSpec defines a health check probe.
type ProbeSpec struct {
	Exec                *ExecAction    `yaml:"exec,omitempty"`
	HTTPGet             *HTTPGetAction `yaml:"httpGet,omitempty"`
	InitialDelaySeconds int            `yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int            `yaml:"periodSeconds,omitempty"`
	TimeoutSeconds      int            `yaml:"timeoutSeconds,omitempty"`
	FailureThreshold    int            `yaml:"failureThreshold,omitempty"`
}

// ExecAction runs a command inside a container.
type ExecAction struct {
	Command []string `yaml:"command"`
}

// HTTPGetAction describes an HTTP GET probe.
type HTTPGetAction struct {
	Path   string `yaml:"path"`
	Port   int    `yaml:"port"`
	Scheme string `yaml:"scheme,omitempty"`
}

// VolumeMount describes a mount point for a volume in a container.
type VolumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	ReadOnly  bool   `yaml:"readOnly,omitempty"`
}

// PodVolume represents a named volume attached to a pod.
type PodVolume struct {
	Name                  string                `yaml:"name"`
	PersistentVolumeClaim *PVCVolumeSource      `yaml:"persistentVolumeClaim,omitempty"`
	EmptyDir              *EmptyDirVolumeSource `yaml:"emptyDir,omitempty"`
}

// PVCVolumeSource references a PersistentVolumeClaim.
type PVCVolumeSource struct {
	ClaimName string `yaml:"claimName"`
}

// EmptyDirVolumeSource is an empty directory volume.
type EmptyDirVolumeSource struct{}

// K8sService represents a K8s Service resource.
type K8sService struct {
	TypeMeta `yaml:",inline"`
	Metadata ObjectMeta  `yaml:"metadata"`
	Spec     ServiceSpec `yaml:"spec"`
}

// ServiceSpec defines the desired behaviour of a K8s Service.
type ServiceSpec struct {
	Selector map[string]string `yaml:"selector"`
	Ports    []ServicePort     `yaml:"ports"`
}

// ServicePort represents a port exposed by a K8s Service.
type ServicePort struct {
	Name       string `yaml:"name,omitempty"`
	Port       int    `yaml:"port"`
	TargetPort string `yaml:"targetPort"`
	Protocol   string `yaml:"protocol,omitempty"`
}

// Secret represents a K8s Secret resource.
type Secret struct {
	TypeMeta   `yaml:",inline"`
	Metadata   ObjectMeta        `yaml:"metadata"`
	StringData map[string]string `yaml:"stringData"`
}

// PersistentVolumeClaim represents a K8s PVC resource.
type PersistentVolumeClaim struct {
	TypeMeta `yaml:",inline"`
	Metadata ObjectMeta `yaml:"metadata"`
	Spec     PVCSpec    `yaml:"spec"`
}

// PVCSpec defines the desired characteristics of a PVC.
type PVCSpec struct {
	AccessModes []string            `yaml:"accessModes"`
	Resources   PVCResourceRequests `yaml:"resources"`
}

// PVCResourceRequests describes the requested PVC resources.
type PVCResourceRequests struct {
	Requests map[string]string `yaml:"requests"`
}

// Kustomization represents a kustomization.yaml file.
type Kustomization struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Resources  []string `yaml:"resources"`
}

// Manifest wraps a K8s object with its intended filename.
type Manifest struct {
	Object   interface{}
	Filename string
}

// RenderOptions holds configuration for the K8s rendering pipeline.
type RenderOptions struct {
	Namespace string
}
