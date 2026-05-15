#!/usr/bin/env bash
set -euo pipefail

repo="alibaba/skill-up"
project="skill-up"
requested_version="${SKILL_UP_VERSION:-latest}"
install_dir="${INSTALL_DIR:-$HOME/.local/bin}"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'error: required command not found: %s\n' "$1" >&2
    exit 1
  fi
}

detect_os() {
  case "$(uname -s)" in
    Linux) echo "linux" ;;
    Darwin) echo "darwin" ;;
    MINGW* | MSYS* | CYGWIN*) echo "windows" ;;
    *)
      printf 'error: unsupported OS: %s\n' "$(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo "amd64" ;;
    arm64 | aarch64) echo "arm64" ;;
    *)
      printf 'error: unsupported architecture: %s\n' "$(uname -m)" >&2
      exit 1
      ;;
  esac
}

latest_tag() {
  curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${repo}/releases/latest" |
    sed 's#.*/tag/##'
}

checksum_file() {
  local file="$1"
  local checksums="$2"

  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$(dirname "$file")" && grep "  $(basename "$file")$" "$checksums" | sha256sum -c -)
    return
  fi

  if command -v shasum >/dev/null 2>&1; then
    (cd "$(dirname "$file")" && grep "  $(basename "$file")$" "$checksums" | shasum -a 256 -c -)
    return
  fi

  echo "warning: sha256sum/shasum not found; skipping checksum verification" >&2
}

need_cmd curl
need_cmd sed
need_cmd grep

os="$(detect_os)"
arch="$(detect_arch)"

tag="${requested_version}"
if [ "$tag" = "latest" ]; then
  tag="$(latest_tag)"
elif [ "${tag#v}" = "$tag" ]; then
  tag="v${tag}"
fi
version="${tag#v}"

archive_ext="tar.gz"
binary_name="${project}"
if [ "$os" = "windows" ]; then
  archive_ext="zip"
  binary_name="${project}.exe"
  need_cmd unzip
else
  need_cmd tar
fi

archive="${project}_${version}_${os}_${arch}.${archive_ext}"
base_url="https://github.com/${repo}/releases/download/${tag}"
archive_url="${base_url}/${archive}"
checksums_url="${base_url}/${project}_${version}_checksums.txt"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

echo "Downloading ${archive_url}"
curl -fL "$archive_url" -o "${tmp_dir}/${archive}"

echo "Downloading checksums"
curl -fL "$checksums_url" -o "${tmp_dir}/checksums.txt"
checksum_file "${tmp_dir}/${archive}" "${tmp_dir}/checksums.txt"

if [ "$archive_ext" = "zip" ]; then
  unzip -q "${tmp_dir}/${archive}" -d "$tmp_dir"
else
  tar -xzf "${tmp_dir}/${archive}" -C "$tmp_dir"
fi

mkdir -p "$install_dir"
cp "${tmp_dir}/${binary_name}" "${install_dir}/${binary_name}"
chmod 0755 "${install_dir}/${binary_name}"

if [ "$binary_name" != "$project" ]; then
  echo "Installed ${binary_name} to ${install_dir}"
else
  echo "Installed ${project} to ${install_dir}/${project}"
fi

case ":$PATH:" in
  *":${install_dir}:"*) ;;
  *)
    cat <<EOF

${install_dir} is not in PATH. Add it with:

  export PATH="\$PATH:${install_dir}"
EOF
    ;;
esac
