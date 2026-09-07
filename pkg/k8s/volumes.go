package k8s

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// maxConfigMapBytes is the API server's limit on the total size of a
// ConfigMap. A larger file has to become an image layer or a volume.
const maxConfigMapBytes = 1 << 20

// pemPrivateKeyMarker matches the PEM header of every private key type,
// which is the one form of secret in a config file that can be recognised
// without guessing.
const pemPrivateKeyMarker = "PRIVATE KEY-----"

// nixStorePrefix is readable, deterministic and content-addressed, so a
// bind mount from it is representable even though it is outside the project.
const nixStorePrefix = "/nix/store/"

// volumePlan is the resolved mapping from one service's compose volume
// strings to K8s pod volumes, container mounts and generated ConfigMaps.
//
// Mounts and pod volumes are planned together because their names have to
// agree: a VolumeMount naming a volume the pod does not declare is rejected
// by the API server just as loudly as an invalid name.
type volumePlan struct {
	mounts     map[string]VolumeMount
	podVolumes []PodVolume
	configMaps []ConfigMap
	warnings   []string
	refusals   []error
	// names maps an assigned volume name to the source that claimed it, so
	// two sources that sanitize alike get distinct names.
	names map[string]string
}

// planVolumes resolves every volume string of a service and its init
// containers. A bind mount that cannot be represented is recorded in
// p.refusals rather than returned, so one render reports all of them
// instead of one per run.
func planVolumes(svcName string, svc eval.Service, compVolumes map[string]eval.Volume, opts RenderOptions) *volumePlan {
	p := &volumePlan{
		mounts: make(map[string]VolumeMount),
		names:  make(map[string]string),
	}
	for _, vol := range serviceVolumeStrings(svc) {
		p.add(svcName, vol, compVolumes, opts)
	}
	return p
}

// serviceVolumeStrings returns the service's own volume strings followed by
// those of its init containers, in declaration order.
func serviceVolumeStrings(svc eval.Service) []string {
	vols := svc.Volumes
	if svc.XNixCompose != nil {
		for _, ic := range svc.XNixCompose.InitContainers {
			vols = append(vols, ic.Volumes...)
		}
	}
	return vols
}

// add resolves a single compose volume string into the plan.
func (p *volumePlan) add(svcName, vol string, compVolumes map[string]eval.Volume, opts RenderOptions) {
	if _, done := p.mounts[vol]; done {
		return
	}
	source, dest, readOnly := parseVolumeString(vol)
	if dest == "" {
		return
	}
	if !isBindMount(source) {
		p.addNamedVolume(vol, source, dest, readOnly, compVolumes)
		return
	}

	content, filename, err := readBindSource(source, opts.ProjectDir)
	if err != nil {
		p.addUnrepresentable(svcName, vol, source, dest, readOnly, err, opts)
		return
	}
	p.addConfigMap(svcName, vol, source, dest, content, filename, opts)
}

// addNamedVolume plans a named volume: a PVC when the composition declares
// it, an empty directory when it does not.
func (p *volumePlan) addNamedVolume(vol, source, dest string, readOnly bool, compVolumes map[string]eval.Volume) {
	name := p.claimName(sanitizeName(source), source)
	pv := PodVolume{Name: name}
	if _, declared := compVolumes[source]; declared {
		pv.PersistentVolumeClaim = &PVCVolumeSource{ClaimName: source}
	} else {
		pv.EmptyDir = &EmptyDirVolumeSource{}
	}
	p.addPodVolume(pv)
	p.mounts[vol] = VolumeMount{Name: name, MountPath: dest, ReadOnly: readOnly}
}

// addConfigMap plans a bind-mounted file as a generated ConfigMap mounted
// with subPath, which is the idiomatic K8s equivalent of mounting one file.
func (p *volumePlan) addConfigMap(svcName, vol, source, dest, content, filename string, opts RenderOptions) {
	name := p.claimName(sanitizeName(svcName+"-"+filename), source)
	key := configMapKey(filename)

	if strings.Contains(content, pemPrivateKeyMarker) {
		p.warnings = append(p.warnings, fmt.Sprintf(
			"service %q: volume %q: ConfigMap %q carries a private key in plain text, and a ConfigMap is not a Secret "+
				"— anyone who can read the namespace, or the rendered file, can read the key; replace the mount with a "+
				"Secret you manage", svcName, vol, name))
	}

	p.addPodVolume(PodVolume{Name: name, ConfigMap: &ConfigMapVolumeSource{Name: name}})
	if !p.hasConfigMap(name) {
		p.configMaps = append(p.configMaps, ConfigMap{
			TypeMeta: TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			Metadata: ObjectMeta{Name: name, Namespace: opts.Namespace, Labels: standardLabels(svcName)},
			Data:     map[string]string{key: content},
		})
	}
	// A ConfigMap volume is read-only regardless of what the compose file
	// asked for, so the mount says so rather than implying writes land
	// anywhere.
	p.mounts[vol] = VolumeMount{Name: name, MountPath: dest, SubPath: key, ReadOnly: true}
}

// addUnrepresentable applies the configured policy to a bind mount with no
// K8s equivalent: refuse, or fall back to an empty directory with a warning.
func (p *volumePlan) addUnrepresentable(svcName, vol, source, dest string, readOnly bool, cause error, opts RenderOptions) {
	if opts.UnrepresentableMounts != MountPolicyEmptyDir {
		p.refusals = append(p.refusals, fmt.Errorf("service %q: volume %q: %w", svcName, vol, cause))
		return
	}
	p.warnings = append(p.warnings,
		fmt.Sprintf("service %q: volume %q: %v; rendered as an empty directory, so the container starts without it", svcName, vol, cause))

	name := p.claimName(sanitizeName(source), source)
	p.addPodVolume(PodVolume{Name: name, EmptyDir: &EmptyDirVolumeSource{}})
	p.mounts[vol] = VolumeMount{Name: name, MountPath: dest, ReadOnly: readOnly}
}

// claimName reserves a volume name for a source, disambiguating with a hash
// suffix when a different source already holds it. The same source asked
// twice gets the same name, so mounting one file at two paths shares one
// volume.
func (p *volumePlan) claimName(base, source string) string {
	if owner, taken := p.names[base]; !taken || owner == source {
		p.names[base] = source
		return base
	}
	name := withSuffix(base, shortHash(source))
	p.names[name] = source
	return name
}

// addPodVolume appends a pod volume unless one of that name is present.
func (p *volumePlan) addPodVolume(pv PodVolume) {
	for _, existing := range p.podVolumes {
		if existing.Name == pv.Name {
			return
		}
	}
	p.podVolumes = append(p.podVolumes, pv)
}

// hasConfigMap reports whether a ConfigMap of that name is already planned.
func (p *volumePlan) hasConfigMap(name string) bool {
	for _, cm := range p.configMaps {
		if cm.Metadata.Name == name {
			return true
		}
	}
	return false
}

// mountsFor returns the container mounts for a list of volume strings, in
// declaration order.
func (p *volumePlan) mountsFor(volumes []string) []VolumeMount {
	if len(volumes) == 0 {
		return nil
	}
	mounts := make([]VolumeMount, 0, len(volumes))
	seen := make(map[string]bool, len(volumes))
	for _, vol := range volumes {
		m, ok := p.mounts[vol]
		if !ok || seen[vol] {
			continue
		}
		seen[vol] = true
		mounts = append(mounts, m)
	}
	if len(mounts) == 0 {
		return nil
	}
	return mounts
}

// isBindMount reports whether a volume source is a host path rather than a
// named volume, using the same classification as the orchestrate converter
// (ADR-016).
func isBindMount(source string) bool {
	return strings.HasPrefix(source, "/") || strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "~") || strings.HasPrefix(source, "..")
}

// readBindSource reads the file a bind mount points at, returning its
// content and base filename. The error says why a source cannot become a
// ConfigMap, and is what the caller reports to the user.
func readBindSource(source, projectDir string) (content, filename string, err error) {
	path, err := resolveBindPath(source, projectDir)
	if err != nil {
		return "", "", err
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("cannot read host path %s: %w", source, err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("host path %s is a directory, which the k8s target cannot represent; "+
			"mount the files individually, or declare a named volume and populate it", source)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("host path %s is a %s, not a regular file, so it has no content to carry into the cluster",
			source, describeMode(info.Mode()))
	}
	if info.Size() > maxConfigMapBytes {
		return "", "", fmt.Errorf("host path %s is %d bytes, over the %d-byte ConfigMap limit; "+
			"put it in the image or a named volume", source, info.Size(), maxConfigMapBytes)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("cannot read host path %s: %w", source, err)
	}
	if !utf8.Valid(data) {
		return "", "", fmt.Errorf("host path %s is not valid UTF-8, so it cannot become a ConfigMap; "+
			"put binary content in the image", source)
	}
	return string(data), filepath.Base(path), nil
}

// describeMode names a non-regular file type for an error message.
func describeMode(mode os.FileMode) string {
	switch {
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeNamedPipe != 0:
		return "named pipe"
	case mode&os.ModeDevice != 0:
		return "device node"
	default:
		return mode.Type().String()
	}
}

// resolveBindPath turns a bind-mount source into a path that may be read.
// A source has to stay inside the project directory or the Nix store: those
// are the two places whose content is part of the composition rather than of
// the machine that happens to be rendering.
func resolveBindPath(source, projectDir string) (string, error) {
	if strings.HasPrefix(source, "~") {
		return "", fmt.Errorf("host path %s is relative to a home directory, which is not part of the project", source)
	}
	if strings.HasPrefix(source, nixStorePrefix) {
		return source, nil
	}
	if projectDir == "" {
		projectDir = "."
	}

	root := absReal(projectDir)
	path := source
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = absReal(path)
	// A project file may be a symlink into the store — a `result` link, or a
	// Nix-generated config — which is still representable.
	if strings.HasPrefix(path, nixStorePrefix) {
		return path, nil
	}

	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("host path %s is outside the project directory, so its content is not part of the "+
			"composition; declare a named volume, or add the file to the project", source)
	}
	return path, nil
}

// absReal resolves a path to an absolute one with symlinks expanded, falling
// back to the plain absolute form when either step fails — the containment
// check below has to compare like with like.
func absReal(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// parseVolumeString parses a compose volume string "source:dest[:options]".
func parseVolumeString(vol string) (source, dest string, readOnly bool) {
	parts := strings.SplitN(vol, ":", 3)
	switch len(parts) {
	case 1:
		return parts[0], parts[0], false
	case 2: //nolint:mnd // source:dest
		return parts[0], parts[1], false
	default:
		return parts[0], parts[1], eval.MountOptionsReadOnly(parts[2])
	}
}
