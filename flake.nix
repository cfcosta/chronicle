{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      nixpkgs,
      flake-utils,

      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.default = pkgs.buildGo123Module {
          pname = "wot-relay";
          version = builtins.hashFile "sha256" ./go.sum;
          src = ./.;
          vendorHash = "sha256-XDtWQEyRomWXJ2P4xXY+s2NEx6vRFzKFG6/r5suEiSs=";
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go_1_23
          ];
        };
      }
    );
}
