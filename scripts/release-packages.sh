#!/bin/bash
# Build release packages for Linux, macOS and Windows.
#
#   scripts/release-packages.sh 0.1.0 "$(git rev-parse HEAD)" dist
#
# VERSION must match internal/version.Version. The script writes, into OUTDIR:
#
#   coigue_<version>_linux_<arch>.deb     Ubuntu, Debian and other deb systems
#   coigue-<version>-1.<rpmarch>.rpm       Fedora and other rpm systems
#   coigue_<version>_linux_<arch>.tar.gz   the same Linux binary, unpacked
#   coigue_<version>_darwin_<arch>.tar.gz  macOS
#   coigue_<version>_windows_<arch>.zip    Windows
#   SHA256SUMS
#
# Every archive contains the binary, LICENSE and THIRD_PARTY_NOTICES.md.
# deb and rpm install the binary at /usr/bin/coigue and the two documents
# under /usr/share/doc/coigue/. Binaries are static (CGO disabled).

set -euo pipefail

nfpm_version=2.48.0
nfpm_sha256=6d7f9a28b2f397934403950fcbba83f598b08d02edbe79a0815b3b4897341750

if [ "$#" -ne 3 ]; then
	echo "usage: scripts/release-packages.sh VERSION COMMIT OUTDIR" >&2
	exit 1
fi

version=$1
commit=$2
outdir=$3

cd "$(dirname "$0")/.."

if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "VERSION must be MAJOR.MINOR.PATCH, not ${version}" >&2
	exit 1
fi
if ! printf '%s\n' "$commit" | grep -Eq '^[0-9a-fA-F]{7,40}$'; then
	echo "COMMIT must be a git sha, not ${commit}" >&2
	exit 1
fi
case "$outdir" in
"" | /* | *..*)
	echo "OUTDIR must be a relative path inside the repository, not ${outdir}" >&2
	exit 1
	;;
esac

source_version=$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/version/version.go)
if [ "$source_version" != "$version" ]; then
	echo "internal/version.Version is ${source_version}, requested ${version}" >&2
	exit 1
fi

module=github.com/fclaude/coigue
ldflags="-s -w -X ${module}/internal/version.Version=${version} -X ${module}/internal/version.PackageRevision=v${version} -X ${module}/internal/version.SourceCommit=${commit}"

rpm_arch() {
	case "$1" in
	amd64) printf '%s\n' x86_64 ;;
	arm64) printf '%s\n' aarch64 ;;
	*)
		echo "no rpm architecture for ${1}" >&2
		exit 1
		;;
	esac
}

# dpkg-deb and rpm are required so a package that omitted the binary or the
# notices fails here, instead of shipping.
if ! command -v dpkg-deb >/dev/null || ! command -v rpm >/dev/null || ! command -v python3 >/dev/null; then
	echo "dpkg-deb, rpm and python3 are required" >&2
	exit 1
fi

check_tree() {
	local listing=$1 stem=$2 binary=$3
	require_line "$listing" "^${stem}/${binary}$" "${stem}/${binary}"
	require_line "$listing" "^${stem}/LICENSE$" "${stem}/LICENSE"
	require_line "$listing" "^${stem}/THIRD_PARTY_NOTICES.md$" "${stem}/THIRD_PARTY_NOTICES.md"
}

require_line() {
	local listing=$1 pattern=$2 what=$3
	if ! printf '%s\n' "$listing" | grep -q "$pattern"; then
		echo "${what} is missing from the package" >&2
		printf '%s\n' "$listing" >&2
		exit 1
	fi
}

check_deb() {
	local deb=$1 arch=$2 listing
	listing=$(dpkg-deb -c "$deb")
	require_line "$listing" '^-rwxr-xr-x .* \./usr/bin/coigue$' "${deb}: /usr/bin/coigue mode 0755"
	require_line "$listing" ' \./usr/share/doc/coigue/LICENSE$' "${deb}: LICENSE"
	require_line "$listing" ' \./usr/share/doc/coigue/THIRD_PARTY_NOTICES.md$' "${deb}: THIRD_PARTY_NOTICES.md"
	# Debian version is upstream version plus the package revision from nfpm's release.
	if [ "$(dpkg-deb -f "$deb" Version)" != "${version}-1" ]; then
		echo "${deb} version is $(dpkg-deb -f "$deb" Version), want ${version}-1" >&2
		exit 1
	fi
	if [ "$(dpkg-deb -f "$deb" Architecture)" != "$arch" ]; then
		echo "${deb} architecture is $(dpkg-deb -f "$deb" Architecture), want ${arch}" >&2
		exit 1
	fi
}

check_rpm() {
	local rpmfile=$1 arch=$2 listing mode
	listing=$(rpm --dbpath "$rpm_db" -qp -l "$rpmfile")
	require_line "$listing" '^/usr/bin/coigue$' "${rpmfile}: /usr/bin/coigue"
	require_line "$listing" '^/usr/share/doc/coigue/LICENSE$' "${rpmfile}: LICENSE"
	require_line "$listing" '^/usr/share/doc/coigue/THIRD_PARTY_NOTICES.md$' "${rpmfile}: THIRD_PARTY_NOTICES.md"
	mode=$(rpm --dbpath "$rpm_db" -qp --dump "$rpmfile" | awk '$1 == "/usr/bin/coigue" { print $5 }')
	if [ "$mode" != "0100755" ]; then
		echo "${rpmfile}: /usr/bin/coigue mode is ${mode}, want 0100755" >&2
		exit 1
	fi
	if [ "$(rpm --dbpath "$rpm_db" -qp --queryformat '%{VERSION}' "$rpmfile")" != "$version" ]; then
		echo "${rpmfile} version is not ${version}" >&2
		exit 1
	fi
	if [ "$(rpm --dbpath "$rpm_db" -qp --queryformat '%{ARCH}' "$rpmfile")" != "$arch" ]; then
		echo "${rpmfile} arch is not ${arch}" >&2
		exit 1
	fi
}

nfpm_dir=""
rpm_db=""
cleanup() {
	if [ -n "$nfpm_dir" ]; then rm -rf "$nfpm_dir"; fi
	if [ -n "$rpm_db" ]; then rm -rf "$rpm_db"; fi
}
trap cleanup EXIT

install_nfpm() {
	local archive
	nfpm_dir=$(mktemp -d)
	archive="${nfpm_dir}/nfpm.tar.gz"
	curl -sSfL -o "$archive" "https://github.com/goreleaser/nfpm/releases/download/v${nfpm_version}/nfpm_${nfpm_version}_Linux_x86_64.tar.gz"
	echo "${nfpm_sha256}  ${archive}" | sha256sum -c - >&2
	tar -xzf "$archive" -C "$nfpm_dir" nfpm
	if [ ! -x "${nfpm_dir}/nfpm" ]; then
		echo "nfpm archive did not contain the nfpm binary" >&2
		exit 1
	fi
	printf '%s\n' "${nfpm_dir}/nfpm"
}

archive_zip() {
	local stage=$1 dest=$2
	python3 - "$stage" "$dest" <<'PY'
import pathlib, sys, zipfile
stage, dest = sys.argv[1], sys.argv[2]
root = pathlib.Path(stage)
with zipfile.ZipFile(dest, "w", compression=zipfile.ZIP_DEFLATED) as zipped:
    for path in sorted(root.rglob("*")):
        if path.is_file():
            zipped.write(path, path.relative_to(root.parent).as_posix())
PY
}

nfpm=$(install_nfpm)
# A private database keeps `rpm -qp` from touching /var/lib/rpm.
rpm_db=$(mktemp -d)
rpm --initdb --dbpath "$rpm_db"
host_os=$(go env GOOS)
host_arch=$(go env GOARCH)
rm -rf "$outdir"
mkdir -p "$outdir"

targets=(
	"linux amd64"
	"linux arm64"
	"darwin amd64"
	"darwin arm64"
	"windows amd64"
	"windows arm64"
)

for target in "${targets[@]}"; do
	goos=${target%% *}
	goarch=${target##* }
	stem="coigue_${version}_${goos}_${goarch}"
	work=$(mktemp -d)
	stage="${work}/${stem}"
	mkdir -p "$stage"
	case "$goos" in
	windows) binary="${stage}/coigue.exe" ;;
	*) binary="${stage}/coigue" ;;
	esac

	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -ldflags "$ldflags" -o "$binary" ./cmd/coigue
	cp LICENSE THIRD_PARTY_NOTICES.md "$stage/"

	if [ "$goos" = "$host_os" ] && [ "$goarch" = "$host_arch" ]; then
		got=$("$binary" --version)
		want="coigue ${version} (main build v${version}; commit ${commit})"
		if [ "$got" != "$want" ]; then
			echo "--version printed ${got}" >&2
			echo "wanted          ${want}" >&2
			exit 1
		fi
	fi

	if [ "$goos" = "linux" ]; then
		export ARCH="$goarch" VERSION="$version"
		export SRC_BINARY="$binary" SRC_LICENSE="${stage}/LICENSE" SRC_NOTICES="${stage}/THIRD_PARTY_NOTICES.md"
		deb="${outdir}/${stem}.deb"
		rpmfile="${outdir}/coigue-${version}-1.$(rpm_arch "$goarch").rpm"
		"$nfpm" package --config packaging/nfpm.yaml --packager deb --target "$deb"
		"$nfpm" package --config packaging/nfpm.yaml --packager rpm --target "$rpmfile"
		check_deb "$deb" "$goarch"
		check_rpm "$rpmfile" "$(rpm_arch "$goarch")"
		tar -C "$work" -czf "${outdir}/${stem}.tar.gz" "$stem"
		check_tree "$(tar -tzf "${outdir}/${stem}.tar.gz")" "$stem" "coigue"
	elif [ "$goos" = "windows" ]; then
		archive_zip "$stage" "${outdir}/${stem}.zip"
		check_tree "$(python3 -c 'import sys,zipfile; print("\n".join(zipfile.ZipFile(sys.argv[1]).namelist()))' "${outdir}/${stem}.zip")" "$stem" "coigue.exe"
	else
		tar -C "$work" -czf "${outdir}/${stem}.tar.gz" "$stem"
		check_tree "$(tar -tzf "${outdir}/${stem}.tar.gz")" "$stem" "coigue"
	fi
	rm -rf "$work"
done

# The checksum file lives outside the directory while it is being written, so
# it is not hashed into itself.
(
	cd "$outdir"
	sha256sum -- *
) >"${outdir}.SHA256SUMS"
mv "${outdir}.SHA256SUMS" "${outdir}/SHA256SUMS"

echo "packages in ${outdir}:"
ls -1 "$outdir"
