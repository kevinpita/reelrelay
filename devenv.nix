{ pkgs, ... }:
{
  languages.go = {
    enable = true;
    package = pkgs.go_1_27;
  };

  packages = with pkgs; [
    actionlint
    ffmpeg
    gcc
    git
    golangci-lint
    govulncheck
    hadolint
    just
    kubeconform
    kubectl
    kubernetes-helm
    nixfmt
    shellcheck
    yt-dlp
  ];

  # Just loads .env at runtime. Keep credentials out of Nix evaluation.
  dotenv.disableHint = true;

  enterTest = ''
    just check
  '';
}
