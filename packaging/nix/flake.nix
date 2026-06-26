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

        version = "1.36.4";

        # Pre-built binaries per platform — matches GoReleaser archive names.
        # Update hashes by running: nix-prefetch-url --unpack <url>
        platformMap = {
          "x86_64-linux" = {
            url = "https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${version}/web-researcher-mcp_${version}_linux_amd64.tar.gz";
            hash = "sha256-1i9HaBhli4GMtzJFHS/7N/uPrLG5X5B8T5EW22vt5jc=";
          };
          "aarch64-linux" = {
            url = "https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${version}/web-researcher-mcp_${version}_linux_arm64.tar.gz";
            hash = "sha256-qdcxmAW3SVFgApNvx/6Rlf5wFfjgk/fCajB6Lkw8ue4=";
          };
          "x86_64-darwin" = {
            url = "https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${version}/web-researcher-mcp_${version}_darwin_amd64.tar.gz";
            hash = "sha256-J+GzQVIS0255nQe8/pzJlL00Uhn3vYxxvU6te3Nb/7c=";
          };
          "aarch64-darwin" = {
            url = "https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${version}/web-researcher-mcp_${version}_darwin_arm64.tar.gz";
            hash = "sha256-uKv7noDb44hbJ/Qhp4ZGIEvakSGBi4dybAQzIr8OueA=";
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
