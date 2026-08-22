# Build a read-only erofs root filesystem image from a NixOS toplevel closure.
#
# The resulting image is suitable for cloud-hypervisor's
#   --disk path=<rootfs>,readonly=on
{ runCommand, erofs-utils, closureInfo, toplevel }:

let
  closure = closureInfo { rootPaths = [ toplevel ]; };
in
runCommand "nix-compose-rootfs.erofs" {
  nativeBuildInputs = [ erofs-utils ];
  passthru = { inherit toplevel; };
} ''
  # Create a temporary root directory with the NixOS toplevel layout.
  root=$(mktemp -d)

  # Populate /nix/store with symlinks to the closure contents.
  mkdir -p "$root/nix/store"
  while IFS= read -r path; do
    cp -a "$path" "$root$path"
  done < ${closure}/store-paths

  # Activate the toplevel: create /etc, /bin/sh, /init symlinks.
  ln -s ${toplevel}/init "$root/init"
  mkdir -p "$root/etc"
  ln -s ${toplevel}/etc/os-release "$root/etc/os-release" || true
  mkdir -p "$root/bin"
  ln -s ${toplevel}/sw/bin/sh "$root/bin/sh" || true
  mkdir -p "$root/sbin"
  ln -s ${toplevel}/init "$root/sbin/init"

  # Create mount points for virtiofs and tmpfs.
  mkdir -p "$root/nix/store"
  mkdir -p "$root/workspace"
  mkdir -p "$root/tmp"
  mkdir -p "$root/var"
  mkdir -p "$root/run"
  mkdir -p "$root/proc"
  mkdir -p "$root/sys"
  mkdir -p "$root/dev"

  # Build the erofs image.
  mkfs.erofs "$out" "$root"
''
