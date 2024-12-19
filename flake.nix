{
  description = "Development environment for Go 1.23.4";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };
      in {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go_1_23
            gopls  # Go language server
            go-tools  # Additional Go tools
          ];

          shellHook = ''
            echo "Go 1.23.4 development environment activated"
            go version
          '';
        };
      }
    );
}