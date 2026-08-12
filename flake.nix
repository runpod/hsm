{
  description = "runpod/hsm static-analysis flake — go vet, staticcheck, golangci-lint, gosec, govulncheck (technique ported from runpod/aiapi).";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachSystem
      [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ]
      (
        system:
        let
          pkgs = import nixpkgs { inherit system; };

          # Concurrency-focused Go static analysis. go vet's copylocks/atomic/
          # loopclosure analyzers plus staticcheck catch the class of bug we
          # just fixed by hand; gosec/govulncheck round out correctness/security.
          tools = [
            pkgs.go
            pkgs.golangci-lint
            pkgs.gosec
            pkgs.go-tools # staticcheck
            pkgs.govulncheck
            pkgs.git
          ];

          analyze = pkgs.writeShellApplication {
            name = "analyze";
            runtimeInputs = tools;
            text = ''
              set -euo pipefail
              echo "== go vet (incl. copylocks, atomic, loopclosure) =="
              go vet ./...
              echo "== staticcheck =="
              staticcheck ./...
              echo "== golangci-lint =="
              golangci-lint run --config nix/golangci.yml --timeout 5m ./...
              echo "== gosec =="
              gosec -quiet ./... || true
              echo "== govulncheck =="
              govulncheck ./... || true
              echo "== go test -race =="
              go test -race -count=1 ./...
            '';
          };
        in
        {
          devShells.default = pkgs.mkShell { packages = tools; };
          apps.default = {
            type = "app";
            program = "${analyze}/bin/analyze";
            meta.description = "Run the full Go static-analysis sweep over hsm.";
          };
        }
      );
}
