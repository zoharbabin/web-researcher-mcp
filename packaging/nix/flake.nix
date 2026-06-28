{
  description = "Your AI research assistant that cites real sources and stays honest";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachSystem [
      "x86_64-linux"
      "aarch64-linux"
      "x86_64-darwin"
      "aarch64-darwin"
    ] (system:
      let
        pkgs = import nixpkgs { inherit system; };

        version = "1.37.3";

        # Pre-built binaries per platform — matches GoReleaser archive names.
        # Update hashes by running: nix-prefetch-url --unpack <url>
        platformMap = {
          "x86_64-linux" = {
            url = "https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${version}/web-researcher-mcp_${version}_linux_amd64.tar.gz";
            hash = "sha256-47fFrls8neO3PH39pt4CfK49YauwQPVlfjd8NsVRyhY=";
          };
          "aarch64-linux" = {
            url = "https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${version}/web-researcher-mcp_${version}_linux_arm64.tar.gz";
            hash = "sha256-0rM7An9fxekAP66Ln9XNPkHc08gRPYhGLvUJW7FRqb4=";
          };
          "x86_64-darwin" = {
            url = "https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${version}/web-researcher-mcp_${version}_darwin_amd64.tar.gz";
            hash = "sha256-oy/3UzoppcTg9YapQRT2gsS3vY77FnOpos5jWbHBesA=";
          };
          "aarch64-darwin" = {
            url = "https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${version}/web-researcher-mcp_${version}_darwin_arm64.tar.gz";
            hash = "sha256-8kt8OmpSv1+iK77IPbksJ6w8r7jzlq6UssbMQB4bj2k=";
          };
        };

        platform = platformMap.${system};

        web-researcher-mcp = pkgs.stdenvNoCC.mkDerivation {
          pname = "web-researcher-mcp";
          inherit version;

          src = pkgs.fetchurl {
            inherit (platform) url hash;
          };

          sourceRoot = ".";

          nativeBuildInputs = [ pkgs.gnutar ];

          installPhase = ''
            runHook preInstall
            install -Dm755 web-researcher-mcp "$out/bin/web-researcher-mcp"
            # Lens JSON files enable site-restricted search domains.
            if [ -d lenses ]; then
              install -dm755 "$out/share/web-researcher-mcp/lenses"
              install -Dm644 lenses/*.json "$out/share/web-researcher-mcp/lenses/"
            fi
            runHook postInstall
          '';

          meta = with pkgs.lib; {
            description = "Your AI research assistant that cites real sources and stays honest";
            longDescription = ''
              web-researcher-mcp is an MCP server that gives AI assistants web search,
              content extraction, academic/patent/SEC/case-law/economic data lookup,
              and multi-step research with real, verifiable citations.
            '';
            homepage = "https://github.com/zoharbabin/web-researcher-mcp";
            license = licenses.mit;
            maintainers = [ {
              name = "Zohar Babin";
              email = "zohar.babin@kaltura.com";
              github = "zoharbabin";
              githubId = 1633843;
            } ];
            platforms = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
            mainProgram = "web-researcher-mcp";
          };
        };
      in {
        packages = {
          inherit web-researcher-mcp;
          default = web-researcher-mcp;
        };

        apps.default = {
          type = "app";
          program = "${web-researcher-mcp}/bin/web-researcher-mcp";
        };

        # Development shell with the binary on PATH.
        devShells.default = pkgs.mkShell {
          packages = [ web-researcher-mcp ];
        };
      });
}
