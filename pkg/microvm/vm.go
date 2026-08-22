package microvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Status represents the lifecycle state of a microVM.
type Status int

const (
	Stopped Status = iota
	Starting
	Running
	Stopping
	Failed
)

func (s Status) String() string {
	switch s {
	case Stopped:
		return "stopped"
	case Starting:
		return "starting"
	case Running:
		return "running"
	case Stopping:
		return "stopping"
	case Failed:
		return "failed"
	default:
		return "unknown"
	}
}

// Share describes a virtiofs shared directory between host and VM.
type Share struct {
	Tag        string
	SourcePath string
	ReadOnly   bool
	Socket     string // auto-generated virtiofsd socket path
}

// Config holds all parameters needed to start a microVM.
type Config struct {
	Kernel        string // path to vmlinux kernel image
	RootFS        string // path to root filesystem image
	VCPUs         int
	MemoryMB      int
	CID           uint32 // vsock context ID (must be >= 3)
	VsockPort     uint32 // vsock port the gRPC server inside the VM listens on
	Shares        []Share
	TAPDevice     string // optional TAP device for networking
	MACAddress    string // optional MAC address for the TAP device
	APISocket     string // cloud-hypervisor API socket path
	Console       string // console device (e.g. "off", "tty", "pty")
	Serial        string // serial device (e.g. "off", "tty")
	HypervisorBin string // path to cloud-hypervisor binary
	VirtiofsdBin  string // path to virtiofsd binary
}

// VM manages the lifecycle of a single microVM instance.
type VM interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Status() Status
	CID() uint32
	VsockPort() uint32
	Wait() error
}

const (
	defaultVCPUs         = 1
	defaultMemoryMB      = 512
	defaultVsockPort     = 1024
	defaultHypervisorBin = "cloud-hypervisor"
	defaultVirtiofsdBin  = "virtiofsd"
	defaultConsole       = "off"
	defaultSerial        = "tty"
	minCID               = 3
)

// validateConfig checks required fields and returns an error if any are invalid.
func validateConfig(cfg Config) error {
	if cfg.Kernel == "" {
		return fmt.Errorf("microvm: kernel path is required")
	}
	if _, err := os.Stat(cfg.Kernel); err != nil {
		return fmt.Errorf("microvm: kernel %s: %w", cfg.Kernel, err)
	}
	if cfg.RootFS == "" {
		return fmt.Errorf("microvm: rootfs path is required")
	}
	if _, err := os.Stat(cfg.RootFS); err != nil {
		return fmt.Errorf("microvm: rootfs %s: %w", cfg.RootFS, err)
	}
	if cfg.CID < minCID {
		return fmt.Errorf("microvm: CID must be >= %d, got %d", minCID, cfg.CID)
	}
	return nil
}

// applyDefaults fills zero-valued optional fields with defaults.
func applyDefaults(cfg *Config) {
	if cfg.VCPUs <= 0 {
		cfg.VCPUs = defaultVCPUs
	}
	if cfg.MemoryMB <= 0 {
		cfg.MemoryMB = defaultMemoryMB
	}
	if cfg.VsockPort == 0 {
		cfg.VsockPort = defaultVsockPort
	}
	if cfg.HypervisorBin == "" {
		cfg.HypervisorBin = defaultHypervisorBin
	}
	if cfg.VirtiofsdBin == "" {
		cfg.VirtiofsdBin = defaultVirtiofsdBin
	}
	if cfg.Console == "" {
		cfg.Console = defaultConsole
	}
	if cfg.Serial == "" {
		cfg.Serial = defaultSerial
	}
	if cfg.APISocket == "" {
		cfg.APISocket = filepath.Join(os.TempDir(), fmt.Sprintf("chv-%d.sock", cfg.CID))
	}
}

// New validates the config and returns a VM ready to start.
func New(cfg Config) (VM, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	return newVM(cfg), nil
}
