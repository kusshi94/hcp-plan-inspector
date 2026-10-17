{
  description = "HCP Terraform 上の Plan 内容を read-only で取得して表示する CLI ツール";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      pkgsFor = system: import nixpkgs { inherit system; };
      # flake からはタグが見えないので、ビルド元のコミットをバージョンにする。
      version = self.shortRev or self.dirtyShortRev or "dev";
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = pkgs.buildGoModule {
            pname = "hcp-plan-inspector";
            inherit version;

            src = self;

            # go.mod/go.sum が変わったらこのハッシュも更新が必要。
            # `lib.fakeHash` に差し替えて `nix build` すると、正しい値が
            # エラーメッセージ (got: sha256-...) に出るのでそれを貼る。
            vendorHash = "sha256-0iXnOgD5c1hofWLcvsG+WRinCeNWxWtUZxcOgHNN1sk=";

            subPackages = [ "cmd/hcp-plan-inspector" ];

            ldflags = [
              "-s"
              "-w"
              "-X main.version=${version}"
            ];

            meta = {
              description = "HCP Terraform 上の Plan 内容を read-only で取得して表示する CLI ツール";
              homepage = "https://github.com/kusshi94/hcp-plan-inspector";
              mainProgram = "hcp-plan-inspector";
            };
          };
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/hcp-plan-inspector";
        };
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.gotools
              pkgs.golangci-lint
            ];
          };
        }
      );
    };
}
