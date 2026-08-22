# NixOS module that produces a CI-oriented VM image for running integration tests.
#
# Outputs:
#   config.system.build.kernel  — vmlinux (uncompressed kernel)
#   config.system.build.rootfs  — read-only erofs root filesystem image
#
# The closure contains containerd, CNI plugins, Go toolchain, and a ci-runner
# systemd unit that executes /workspace/.ci-command and writes the exit code
# to /workspace/.ci-exit-code before powering off the VM.
{ nixpkgs, system ? "x86_64-linux" }:

let
  pkgs = import nixpkgs { inherit system; };

  nixos = pkgs.nixos ({ config, lib, pkgs, ... }: {
    # ── Boot ──────────────────────────────────────────────────────────
    boot.kernelParams = [ "console=ttyS0" ];

    boot.initrd.systemd.enable = true;

    boot.initrd.availableKernelModules = [
      "virtiofs"
      "overlay"
      "virtio_blk"
      "virtio_pci"
    ];

    boot.kernelModules = [
      "virtiofs"
      "overlay"
      # CRI networking modules
      "br_netfilter"
      "veth"
      "vxlan"
      "nf_nat"
      "xt_MASQUERADE"
    ];

    # ── Sysctl (container networking) ────────────────────────────────
    boot.kernel.sysctl = {
      "net.ipv4.ip_forward" = 1;
      "net.bridge.bridge-nf-call-iptables" = 1;
    };

    # ── Filesystem ────────────────────────────────────────────────────
    fileSystems."/" = {
      device = "/dev/vda";
      fsType = "erofs";
      options = [ "ro" ];
    };

    fileSystems."/nix/store" = {
      device = "nix-store";
      fsType = "virtiofs";
      options = [ "ro" ];
      neededForBoot = true;
    };

    fileSystems."/workspace" = {
      device = "project";
      fsType = "virtiofs";
      options = [ "rw" ];
    };

    # Writable overlay for runtime state — increased sizes for CI.
    fileSystems."/tmp" = {
      device = "tmpfs";
      fsType = "tmpfs";
      options = [ "size=512M" "mode=1777" ];
    };

    fileSystems."/var" = {
      device = "tmpfs";
      fsType = "tmpfs";
      options = [ "size=2G" "mode=0755" ];
    };

    fileSystems."/run" = {
      device = "tmpfs";
      fsType = "tmpfs";
      options = [ "size=128M" "mode=0755" ];
    };

    # ── containerd ────────────────────────────────────────────────────
    virtualisation.containerd.enable = true;

    # ── CNI plugins ───────────────────────────────────────────────────
    environment.variables.CNI_PATH = "${pkgs.cni-plugins}/bin";

    environment.systemPackages = [
      pkgs.cni-plugins
      pkgs.iptables
      # Go toolchain and build deps for integration tests
      pkgs.go
      pkgs.gnumake
      pkgs.gcc
      pkgs.git
    ];

    # ── CNI bridge configuration ──────────────────────────────────────
    environment.etc."cni/net.d/10-bridge.conflist".text = builtins.toJSON {
      cniVersion = "1.0.0";
      name = "bridge";
      plugins = [
        {
          type = "bridge";
          bridge = "cni0";
          isGateway = true;
          ipMasq = true;
          ipam = {
            type = "host-local";
            ranges = [
              [ { subnet = "10.88.0.0/16"; gateway = "10.88.0.1"; } ]
            ];
            routes = [
              { dst = "0.0.0.0/0"; }
            ];
          };
        }
        {
          type = "portmap";
          capabilities = { portMappings = true; };
          snat = true;
        }
      ];
    };

    # ── Networking (systemd-networkd DHCP) ────────────────────────────
    networking.useNetworkd = true;
    networking.useDHCP = false;
    systemd.network.enable = true;
    systemd.network.networks."10-virtio" = {
      matchConfig.Name = "enp0s*";
      networkConfig = {
        DHCP = "ipv4";
      };
    };

    # ── CI runner service ─────────────────────────────────────────────
    systemd.services.ci-runner = {
      description = "CI test runner — executes /workspace/.ci-command";
      wantedBy = [ "multi-user.target" ];
      after = [ "containerd.service" "network-online.target" ];
      requires = [ "containerd.service" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        ExecStart = let
          ciScript = pkgs.writeShellScript "ci-runner.sh" ''
            set -uo pipefail

            CMD="/workspace/.ci-command"

            if [ ! -f "$CMD" ]; then
              echo "ci-runner: ERROR — $CMD not found" >&2
              echo "1" > /workspace/.ci-exit-code
              poweroff
              exit 1
            fi

            chmod +x "$CMD"
            "$CMD"
            RC=$?

            echo "$RC" > /workspace/.ci-exit-code
            sync
            poweroff
          '';
        in "${ciScript}";
      };
    };

    # ── Boot loader (not needed — cloud-hypervisor boots kernel directly) ─
    boot.loader.grub.enable = false;

    # ── Minimal system ────────────────────────────────────────────────
    documentation.enable = false;
    security.polkit.enable = false;
    nix.enable = false;
    services.openssh.enable = false;

    networking.hostName = "ci-vm";
    networking.firewall.enable = false;

    users.mutableUsers = false;
    users.allowNoPasswordLogin = true;

    system.stateVersion = "24.11";
  });

  rootfs = pkgs.callPackage ./make-rootfs.nix {
    toplevel = nixos.config.system.build.toplevel;
  };

  # The uncompressed kernel (vmlinux/Image) is in the dev output.
  kernelPath = "${nixos.config.system.build.kernel.dev}/${
    if pkgs.stdenv.hostPlatform.isAarch64
    then "Image"
    else "vmlinux"
  }";

in {
  kernel = kernelPath;
  inherit rootfs;

  # Combined output: a directory with kernel and rootfs symlinks.
  combined = pkgs.runCommand "ci-vm-image" {} ''
    mkdir -p $out
    ln -s ${kernelPath} $out/kernel
    ln -s ${rootfs} $out/rootfs
  '';
}
