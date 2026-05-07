#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
fetch-embed-tools.sh

Downloads tool binaries into ingest/embedtools/assets/<goos>/ for bundled packages.
The same directory can still be used by optional embedtools builds.
Note: when fetching ffmpeg, this script also fetches ffprobe from the same build (if available).

Examples:
  scripts/fetch-embed-tools.sh --os windows --arch amd64
  scripts/fetch-embed-tools.sh --os darwin --arch arm64 --tools yt-dlp,ffmpeg,node

Flags:
  --os <goos>        windows|linux|darwin (default: GOOS env or `go env GOOS`)
  --arch <goarch>    amd64|arm64         (default: GOARCH env or `go env GOARCH`)
  --tools <csv>      yt-dlp,ffmpeg,node  (default: yt-dlp,ffmpeg,node)
EOF
}

die() { echo "error: $*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

csv_has() {
  local csv="$1"
  local want="$2"
  IFS=',' read -r -a parts <<<"$csv"
  for p in "${parts[@]}"; do
    if [[ "${p}" == "${want}" ]]; then
      return 0
    fi
  done
  return 1
}

tmpdir=""
cleanup() {
  if [[ -n "${tmpdir}" && -d "${tmpdir}" ]]; then
    rm -rf "${tmpdir}"
  fi
}
trap cleanup EXIT

GOOS="${GOOS:-}"
GOARCH="${GOARCH:-}"
TOOLS="yt-dlp,ffmpeg,node"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --os) GOOS="${2:-}"; shift 2 ;;
    --arch) GOARCH="${2:-}"; shift 2 ;;
    --tools) TOOLS="${2:-}"; shift 2 ;;
    *) die "unknown arg: $1 (use --help)" ;;
  esac
done

need curl

PYTHON="${PYTHON:-}"
if [[ -z "${PYTHON}" ]]; then
  if command -v python3 >/dev/null 2>&1; then
    PYTHON="python3"
  elif command -v python >/dev/null 2>&1; then
    PYTHON="python"
  else
    die "missing required command: python3 (or python)"
  fi
fi

if [[ -z "${GOOS}" ]]; then GOOS="$(go env GOOS)"; fi
if [[ -z "${GOARCH}" ]]; then GOARCH="$(go env GOARCH)"; fi

case "${GOOS}" in
  windows|linux|darwin) ;;
  *) die "unsupported --os: ${GOOS}" ;;
esac

case "${GOARCH}" in
  amd64|arm64) ;;
  *) die "unsupported --arch: ${GOARCH}" ;;
esac

# Required for extracting linux ffmpeg tarballs and Node's non-Windows archives.
if { csv_has "${TOOLS}" "ffmpeg" && [[ "${GOOS}" == "linux" ]]; } || { csv_has "${TOOLS}" "node" && [[ "${GOOS}" != "windows" ]]; }; then
  need tar
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Embedding assets live next to the embedtools package so go:embed patterns are simple.
out_dir="${root}/ingest/embedtools/assets/${GOOS}"
mkdir -p "${out_dir}"

tmpdir="$(mktemp -d)"

download() {
  local url="$1"
  local out="$2"
  echo "download: ${url}"
  # `--retry-all-errors` helps with transient DNS and GitHub edge hiccups.
  curl -fsSL --retry 6 --retry-all-errors --retry-delay 2 --connect-timeout 20 -o "${out}" "${url}"
}

extract_zip_member() {
  local zip="$1"
  local member_suffix="$2"
  local out="$3"
  "${PYTHON}" - "${zip}" "${member_suffix}" "${out}" <<'PY'
import os, sys, zipfile
zip_path, suffix, out_path = sys.argv[1], sys.argv[2], sys.argv[3]
with zipfile.ZipFile(zip_path) as z:
  names = z.namelist()
  cands = [n for n in names if n.endswith(suffix) and not n.endswith("/")]
  if not cands:
    raise SystemExit(f"zip member not found (suffix={suffix}): {zip_path}")
  # Prefer shortest path (usually the canonical bin/<file>).
  cands.sort(key=len)
  name = cands[0]
  os.makedirs(os.path.dirname(out_path) or ".", exist_ok=True)
  with z.open(name) as src, open(out_path, "wb") as dst:
    dst.write(src.read())
print(f"extract: {name} -> {out_path}")
PY
}

chmod_x() {
  local path="$1"
  if [[ "${GOOS}" != "windows" ]]; then
    chmod 0755 "${path}"
  fi
}

fetch_ytdlp() {
  local target="${out_dir}/yt-dlp"
  if [[ "${GOOS}" == "windows" ]]; then
    target="${out_dir}/yt-dlp.exe"
    download "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe" "${target}"
  elif [[ "${GOOS}" == "darwin" ]]; then
    # Use native macOS universal binary so bundled builds do not depend on system python/CLT.
    local asset=""
    case "${GOARCH}" in
      arm64|amd64) asset="yt-dlp_macos" ;;
      *) die "no yt-dlp asset mapping for ${GOOS}/${GOARCH}" ;;
    esac
    download "https://github.com/yt-dlp/yt-dlp/releases/latest/download/${asset}" "${target}"
  else
    # Linux uses the generic unix launcher.
    download "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp" "${target}"
  fi
  chmod_x "${target}"
}

fetch_node() {
  local platform=""
  local index_key=""
  local archive_ext=""
  local target="${out_dir}/node"

  case "${GOOS}/${GOARCH}" in
    windows/amd64) platform="win-x64"; index_key="win-x64-zip"; archive_ext="zip"; target="${out_dir}/node.exe" ;;
    windows/arm64) platform="win-arm64"; index_key="win-arm64-zip"; archive_ext="zip"; target="${out_dir}/node.exe" ;;
    linux/amd64) platform="linux-x64"; index_key="linux-x64"; archive_ext="tar.xz" ;;
    linux/arm64) platform="linux-arm64"; index_key="linux-arm64"; archive_ext="tar.xz" ;;
    darwin/amd64) platform="darwin-x64"; index_key="osx-x64-tar"; archive_ext="tar.gz" ;;
    darwin/arm64) platform="darwin-arm64"; index_key="osx-arm64-tar"; archive_ext="tar.gz" ;;
    *) die "no node asset mapping for ${GOOS}/${GOARCH}" ;;
  esac

  local index="${tmpdir}/node-index.json"
  download "https://nodejs.org/dist/index.json" "${index}"

  local version=""
  version="$("${PYTHON}" - "${index}" "${index_key}" <<'PY'
import json, sys
index_path, index_key = sys.argv[1], sys.argv[2]
with open(index_path, "r", encoding="utf-8") as f:
  releases = json.load(f)

def has_platform(release):
  return index_key in release.get("files", [])

for release in releases:
  if release.get("lts") and has_platform(release):
    print(release["version"])
    raise SystemExit(0)
for release in releases:
  if has_platform(release):
    print(release["version"])
    raise SystemExit(0)
raise SystemExit(f"node release not found for file key: {index_key}")
PY
)"

  local archive="${tmpdir}/node.${archive_ext}"
  local asset="node-${version}-${platform}.${archive_ext}"
  download "https://nodejs.org/dist/${version}/${asset}" "${archive}"

  if [[ "${archive_ext}" == "zip" ]]; then
    extract_zip_member "${archive}" "node.exe" "${target}"
  else
    local xdir="${tmpdir}/node-extract"
    mkdir -p "${xdir}"
    tar -C "${xdir}" -xf "${archive}"
    local node_found=""
    node_found="$(find "${xdir}" -type f -path "*/bin/node" -perm -u+x 2>/dev/null | head -n 1 || true)"
    if [[ -z "${node_found}" ]]; then
      node_found="$(find "${xdir}" -type f -path "*/bin/node" 2>/dev/null | head -n 1 || true)"
    fi
    [[ -n "${node_found}" ]] || die "node binary not found inside: ${archive}"
    cp -f "${node_found}" "${target}"
  fi

  chmod_x "${target}"
}

fetch_ffmpeg() {
  local archive="${tmpdir}/ffmpeg"
  local ffmpeg_target="${out_dir}/ffmpeg"
  local ffprobe_target="${out_dir}/ffprobe"

  if [[ "${GOOS}" == "windows" ]]; then
    ffmpeg_target="${out_dir}/ffmpeg.exe"
    ffprobe_target="${out_dir}/ffprobe.exe"
    archive="${archive}.zip"
    # Static (non-shared) Windows build.
    download "https://github.com/BtbN/FFmpeg-Builds/releases/latest/download/ffmpeg-master-latest-win64-gpl.zip" "${archive}"
    extract_zip_member "${archive}" "bin/ffmpeg.exe" "${ffmpeg_target}"
    extract_zip_member "${archive}" "bin/ffprobe.exe" "${ffprobe_target}"
  elif [[ "${GOOS}" == "darwin" ]]; then
    # BtbN/FFmpeg-Builds does not ship macOS artifacts. Use Martin Riedl's signed builds.
    # Note: ffmpeg and ffprobe are shipped as separate zip files.
    # Scripting URLs documented on https://ffmpeg.martin-riedl.de/.
    archive="${archive}.zip"
    local archive_probe="${tmpdir}/ffprobe.zip"
    local arch="${GOARCH}"
    download "https://ffmpeg.martin-riedl.de/redirect/latest/macos/${arch}/release/ffmpeg.zip" "${archive}"
    extract_zip_member "${archive}" "ffmpeg" "${ffmpeg_target}"
    download "https://ffmpeg.martin-riedl.de/redirect/latest/macos/${arch}/release/ffprobe.zip" "${archive_probe}"
    extract_zip_member "${archive_probe}" "ffprobe" "${ffprobe_target}"
    chmod_x "${ffmpeg_target}"
    chmod_x "${ffprobe_target}"
  else
    local platform=""
    case "${GOOS}/${GOARCH}" in
      linux/amd64) platform="linux64" ;;
      linux/arm64) platform="linuxarm64" ;;
      *) die "no ffmpeg asset mapping for ${GOOS}/${GOARCH}" ;;
    esac

    archive="${archive}.tar.xz"
    download "https://github.com/BtbN/FFmpeg-Builds/releases/latest/download/ffmpeg-master-latest-${platform}-gpl.tar.xz" "${archive}"

    local xdir="${tmpdir}/ffmpeg-extract"
    mkdir -p "${xdir}"
    tar -C "${xdir}" -xf "${archive}"

    local ffmpeg_found=""
    ffmpeg_found="$(find "${xdir}" -type f -name ffmpeg -perm -u+x 2>/dev/null | head -n 1 || true)"
    if [[ -z "${ffmpeg_found}" ]]; then
      ffmpeg_found="$(find "${xdir}" -type f -name ffmpeg 2>/dev/null | head -n 1 || true)"
    fi
    [[ -n "${ffmpeg_found}" ]] || die "ffmpeg binary not found inside: ${archive}"
    cp -f "${ffmpeg_found}" "${ffmpeg_target}"
    chmod_x "${ffmpeg_target}"

    local ffprobe_found=""
    ffprobe_found="$(find "${xdir}" -type f -name ffprobe -perm -u+x 2>/dev/null | head -n 1 || true)"
    if [[ -z "${ffprobe_found}" ]]; then
      ffprobe_found="$(find "${xdir}" -type f -name ffprobe 2>/dev/null | head -n 1 || true)"
    fi
    [[ -n "${ffprobe_found}" ]] || die "ffprobe binary not found inside: ${archive}"
    cp -f "${ffprobe_found}" "${ffprobe_target}"
    chmod_x "${ffprobe_target}"
  fi
}

echo "target: ${out_dir}"
echo "tools:  ${TOOLS}"

if csv_has "${TOOLS}" "yt-dlp"; then fetch_ytdlp; fi
if csv_has "${TOOLS}" "node"; then fetch_node; fi
if csv_has "${TOOLS}" "ffmpeg"; then fetch_ffmpeg; fi

echo "ok:"
ls -la "${out_dir}"
