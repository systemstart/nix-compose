// Package nixpins carries the flake revisions that YAML-mode projects are
// evaluated against.
//
// A flake or compose.nix project brings its own nixpkgs, so nix-compose never
// had to have an opinion about which one. A nix-compose.yaml project brings
// nothing — `package: nginx` has to mean *some* nginx — so the pins live here
// and travel with the binary. The consequence is worth stating plainly: in
// YAML mode the nix-compose version determines the package versions, the way a
// distro release does. `nixpkgs:` in the YAML overrides it per project.
//
// These must match flake.lock; pins_test.go fails if they drift.
package nixpins

const (
	// NixpkgsRev is the nixpkgs `package:` resolves against by default.
	NixpkgsRev = "afe3d8ac4395617bdcdac9f188ac8717a062e014"

	// NixOCIRev supplies buildOCIImage, which turns a package's closure into
	// an OCI image (ADR-006, ADR-015).
	NixOCIRev = "038dc0552a97b8c189626b23a5b6b91e52b9c459"
)

// NixpkgsRef returns the default nixpkgs flake reference. It is pinned to a
// full revision rather than a branch so `builtins.getFlake` stays pure — an
// unlocked ref would force --impure on every evaluation.
func NixpkgsRef() string {
	return "github:NixOS/nixpkgs/" + NixpkgsRev
}

// NixOCIRef returns the nix-oci flake reference.
func NixOCIRef() string {
	return "github:systemstart/nix-oci/" + NixOCIRev
}
