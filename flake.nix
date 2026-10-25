{
  description = "1loop: a coding agent for your project, with your model";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      version = nixpkgs.lib.fileContents ./VERSION;
    in
    {
      packages.${system} = rec {
        oneloop = pkgs.callPackage ./packaging/nix/package.nix { inherit version; };
        default = oneloop;
      };
      apps.${system}.default = {
        type = "app";
        program = "${self.packages.${system}.default}/bin/1loop";
      };
      overlays.default = final: prev: {
        oneloop = final.callPackage ./packaging/nix/package.nix { inherit version; };
      };
    };
}
