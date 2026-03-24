#!/bin/sh
set -eu

OWNER="${OWNER:-beeper}"
REPO="${REPO:-agentremote}"
VERSION="${VERSION:-latest}"
BINDIR="${BINDIR:-}"

need_cmd() {
	command -v "$1" >/dev/null 2>&1
}

fail() {
	printf '%s\n' "error: $*" >&2
	exit 1
}

detect_os() {
	case "$(uname -s)" in
		Linux) printf '%s\n' "linux" ;;
		Darwin) printf '%s\n' "darwin" ;;
		*) fail "unsupported operating system: $(uname -s)" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
		x86_64|amd64) printf '%s\n' "amd64" ;;
		arm64|aarch64) printf '%s\n' "arm64" ;;
		*) fail "unsupported architecture: $(uname -m)" ;;
	esac
}

normalize_version() {
	if [ "$VERSION" = "latest" ]; then
		printf '%s\n' "latest"
		return
	fi
	case "$VERSION" in
		v*) printf '%s\n' "$VERSION" ;;
		*) printf 'v%s\n' "$VERSION" ;;
	esac
}

resolve_latest_version() {
	api_url="https://api.github.com/repos/$OWNER/$REPO/releases/latest"

	if need_cmd curl; then
		response="$(curl -fsSL "$api_url")" || fail "no published GitHub release found for $OWNER/$REPO; create and publish a v* tag before using VERSION=latest"
	elif need_cmd wget; then
		response="$(wget -qO - "$api_url")" || fail "no published GitHub release found for $OWNER/$REPO; create and publish a v* tag before using VERSION=latest"
	else
		fail "curl or wget is required"
	fi

	version="$(printf '%s' "$response" | tr -d '\n' | sed -n 's/.*"tag_name":"\([^"]*\)".*/\1/p')"
	if [ -z "$version" ]; then
		fail "failed to resolve the latest release tag for $OWNER/$REPO"
	fi

	printf '%s\n' "$version"
}

asset_name() {
	version_no_v="$1"
	os="$2"
	arch="$3"
	printf 'agentremote_v%s_%s_%s.tar.gz\n' "$version_no_v" "$os" "$arch"
}

download() {
	url="$1"
	dest="$2"
	if need_cmd curl; then
		if ! curl -fsSL "$url" -o "$dest"; then
			case "$url" in
				https://github.com/"$OWNER"/"$REPO"/releases/latest/download/*)
					fail "no published GitHub release found for $OWNER/$REPO; create and publish a v* tag before using VERSION=latest"
					;;
				*)
					fail "failed to download $url"
					;;
			esac
		fi
		return
	fi
	if need_cmd wget; then
		if ! wget -qO "$dest" "$url"; then
			case "$url" in
				https://github.com/"$OWNER"/"$REPO"/releases/latest/download/*)
					fail "no published GitHub release found for $OWNER/$REPO; create and publish a v* tag before using VERSION=latest"
					;;
				*)
					fail "failed to download $url"
					;;
			esac
		fi
		return
	fi
	fail "curl or wget is required"
}

pick_bindir() {
	home_dir="${HOME:-}"

	if [ -n "$BINDIR" ]; then
		mkdir -p "$BINDIR"
		printf '%s\n' "$BINDIR"
		return
	fi

	if [ -n "$home_dir" ]; then
		for candidate in "$home_dir/.local/bin" "$home_dir/bin"; do
			mkdir -p "$candidate"
			if [ -w "$candidate" ]; then
				printf '%s\n' "$candidate"
				return
			fi
		done
	fi

	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		printf '%s\n' "/usr/local/bin"
		return
	fi

	fail "could not find a writable install directory; set BINDIR=/path/to/bin"
}

checksum_for() {
	file_name="$1"
	checksums_file="$2"
	awk -v target="$file_name" '$2 == target { print $1; exit }' "$checksums_file"
}

verify_checksum() {
	file_path="$1"
	expected="$2"
	if [ -z "$expected" ]; then
		fail "missing checksum for $(basename "$file_path")"
	fi
	if need_cmd shasum; then
		actual="$(shasum -a 256 "$file_path" | awk '{print $1}')"
	elif need_cmd sha256sum; then
		actual="$(sha256sum "$file_path" | awk '{print $1}')"
	else
		fail "shasum or sha256sum is required"
	fi
	if [ "$actual" != "$expected" ]; then
		fail "checksum mismatch for $(basename "$file_path")"
	fi
}

install_binary() {
	archive_path="$1"
	dest_dir="$2"
	tmp_extract="$3"

	tar -xzf "$archive_path" -C "$tmp_extract"
	if [ ! -f "$tmp_extract/agentremote" ]; then
		fail "release archive did not contain agentremote"
	fi

	dest_path="$dest_dir/agentremote"
	if need_cmd install; then
		install -m 0755 "$tmp_extract/agentremote" "$dest_path"
	else
		cp "$tmp_extract/agentremote" "$dest_path"
		chmod 0755 "$dest_path"
	fi

	printf '%s\n' "$dest_path"
}

path_hint() {
	bin_dir="$1"
	case ":${PATH:-}:" in
		*:"$bin_dir":*) return 0 ;;
	esac
	printf '%s\n' "warning: $bin_dir is not on PATH" >&2
	printf '%s\n' "add this to your shell profile:" >&2
	printf '%s\n' "  export PATH=\"$bin_dir:\$PATH\"" >&2
}

main() {
	os="$(detect_os)"
	arch="$(detect_arch)"
	requested_version="$(normalize_version)"
	case "$requested_version" in
		latest)
			version="$(resolve_latest_version)"
			;;
		*)
			version="$requested_version"
			;;
	esac
	version_no_v="${version#v}"
	bin_dir="$(pick_bindir)"
	asset="$(asset_name "$version_no_v" "$os" "$arch")"

	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT INT TERM

	case "$requested_version" in
		latest)
			base_url="https://github.com/$OWNER/$REPO/releases/latest/download"
			;;
		*)
			base_url="https://github.com/$OWNER/$REPO/releases/download/$version"
			;;
	esac

	archive_path="$tmp_dir/$asset"
	checksums_path="$tmp_dir/checksums.txt"
	extract_dir="$tmp_dir/extracted"
	mkdir -p "$extract_dir"

	printf '%s\n' "Downloading $asset"
	download "$base_url/$asset" "$archive_path"
	download "$base_url/checksums.txt" "$checksums_path"

	expected_checksum="$(checksum_for "$asset" "$checksums_path")"
	verify_checksum "$archive_path" "$expected_checksum"

	dest_path="$(install_binary "$archive_path" "$bin_dir" "$extract_dir")"
	path_hint "$bin_dir"

	printf '%s\n' "Installed $dest_path"
	"$dest_path" --version
}

main "$@"
