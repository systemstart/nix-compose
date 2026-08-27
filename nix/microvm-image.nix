# NixOS module that produces a minimal VM closure for nix-compose microVM mode.
#
# Outputs:
#   config.system.build.kernel  — vmlinux (uncompressed kernel)
#   config.system.build.rootfs  — read-only erofs root filesystem image
#
# The closure contains containerd, CNI plugins, and the nix-compose serve
# systemd unit. It is designed to boot under cloud-hypervisor with virtiofs
# mounts for /nix/store (read-only) and /workspace (read-write).
{ nixpkgs, nix-compose-bin, system ? "x86_64-linux" }:

let
  pkgs = import nixpkgs { inherit system; };

  nixos = pkgs.nixos ({ config, lib, pkgs, ... }: {
    # ── Boot ──────────────────────────────────────────────────────────
    boot.kernelParams = [ "console=ttyS0" ];

    boot.initrd.systemd.enable = true;

    boot.initrd.availableKernelModules = [
      # The virtio vsock guest driver. There is no module called
      # "virtio_vsock": CONFIG_VIRTIO_VSOCKETS builds
      # net/vmw_vsock/vmw_vsock_virtio_transport.ko, and modprobe pulls in
      # vsock.ko and vmw_vsock_virtio_transport_common.ko behind it.
      "vmw_vsock_virtio_transport"
      "virtiofs"
      "overlay"
      "virtio_blk"
      "virtio_pci"
    ];

    boot.kernelModules = [
      "vmw_vsock_virtio_transport"
      "virtiofs"
      "overlay"
    ];

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

    # Writable overlay for runtime state.
    fileSystems."/tmp" = {
      device = "tmpfs";
      fsType = "tmpfs";
      options = [ "size=256M" "mode=1777" ];
    };

    fileSystems."/var" = {
      device = "tmpfs";
      fsType = "tmpfs";
      options = [ "size=512M" "mode=0755" ];
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
    ];

    # ── nix-compose serve ─────────────────────────────────────────────
    systemd.services.nix-compose-serve = {
      description = "nix-compose orchestrate server (vsock)";
      wantedBy = [ "multi-user.target" ];
      after = [ "containerd.service" ];
      requires = [ "containerd.service" ];
      serviceConfig = {
        ExecStart = "${nix-compose-bin}/bin/nix-compose serve --vsock-port 1024 --portfwd-port 1025";
        Restart = "on-failure";
        RestartSec = "2s";
        Type = "simple";
      };
    };

    # ── Boot loader (not needed — cloud-hypervisor boots kernel directly) ─
    boot.loader.grub.enable = false;

    # ── Minimal system ────────────────────────────────────────────────
    documentation.enable = false;
    security.polkit.enable = false;
    nix.enable = false;
    services.openssh.enable = false;

    networking.hostName = "nix-compose-vm";
    networking.firewall.enable = false;

    users.mutableUsers = false;
    # This is an appliance image: nothing logs in. It is driven entirely over
    # vsock by the nix-compose-serve unit, and openssh is off, so there is no
    # login path a password would guard.
    users.allowNoPasswordLogin = true;

    system.stateVersion = "24.11";
  });

  rootfs = pkgs.callPackage ./make-rootfs.nix {
    toplevel = nixos.config.system.build.toplevel;
  };

in {
  kernel = "${nixos.config.system.build.kernel}/${
    if pkgs.stdenv.hostPlatform.isAarch64
    then "Image"
    else "vmlinux"
  }";
  inherit rootfs;

  # Combined output: a directory with kernel and rootfs symlinks.
  combined = pkgs.runCommand "nix-compose-microvm-image" {} ''
    mkdir -p $out
    ln -s ${nixos.config.system.build.kernel}/${
      if pkgs.stdenv.hostPlatform.isAarch64
      then "Image"
      else "vmlinux"
    } $out/kernel
    ln -s ${rootfs} $out/rootfs
  '';
}
