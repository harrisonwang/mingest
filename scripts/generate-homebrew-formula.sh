#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<USAGE
Usage: $0 --tag <tag> --repo <owner/repo> --checksums <SHA256SUMS.txt> [--output <path>]

Example:
  $0 --tag v0.4.2 --repo harrisonwang/mingest --checksums artifacts/SHA256SUMS.txt --output Formula/mingest.rb
USAGE
}

TAG=""
REPO=""
CHECKSUMS=""
OUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:-}"
      shift 2
      ;;
    --repo)
      REPO="${2:-}"
      shift 2
      ;;
    --checksums)
      CHECKSUMS="${2:-}"
      shift 2
      ;;
    --output)
      OUT="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ -z "$TAG" || -z "$REPO" || -z "$CHECKSUMS" ]]; then
  usage
  exit 2
fi

if [[ ! -f "$CHECKSUMS" ]]; then
  echo "checksums file not found: $CHECKSUMS" >&2
  exit 1
fi

VER_NUM="${TAG#v}"
asset_darwin_amd64="mingest_${TAG}_darwin_amd64_slim.tar.gz"
asset_darwin_arm64="mingest_${TAG}_darwin_arm64_slim.tar.gz"
asset_linux_amd64="mingest_${TAG}_linux_amd64_slim.tar.gz"

sha_for() {
  local name="$1"
  awk -v n="$name" '$2==n {print $1}' "$CHECKSUMS" | head -n 1
}

sha_darwin_amd64="$(sha_for "$asset_darwin_amd64")"
sha_darwin_arm64="$(sha_for "$asset_darwin_arm64")"
sha_linux_amd64="$(sha_for "$asset_linux_amd64")"

if [[ -z "$sha_darwin_amd64" || -z "$sha_darwin_arm64" || -z "$sha_linux_amd64" ]]; then
  echo "failed to resolve one or more checksums from $CHECKSUMS" >&2
  exit 1
fi

url_base="https://github.com/${REPO}/releases/download/${TAG}"

render() {
  cat <<RUBY
class Mingest < Formula
  desc "Local video archiving CLI powered by yt-dlp and ffmpeg"
  homepage "https://github.com/${REPO}"
  version "${VER_NUM}"
  license "AGPL-3.0-only"

  depends_on "yt-dlp"
  depends_on "ffmpeg"
  depends_on "deno"

  on_macos do
    on_arm do
      url "${url_base}/${asset_darwin_arm64}"
      sha256 "${sha_darwin_arm64}"
    end

    on_intel do
      url "${url_base}/${asset_darwin_amd64}"
      sha256 "${sha_darwin_amd64}"
    end
  end

  on_linux do
    url "${url_base}/${asset_linux_amd64}"
    sha256 "${sha_linux_amd64}"
  end

  def install
    bin.install "mingest/mingest"
  end

  test do
    output = shell_output("#{bin}/mingest --version")
    assert_match "mingest", output
  end
end
RUBY
}

if [[ -n "$OUT" ]]; then
  mkdir -p "$(dirname "$OUT")"
  render > "$OUT"
else
  render
fi
