#!/bin/bash

set -euo pipefail

version=${HELM_VERSION:-${1:?helm version is required}}
destination=${HELM:-${2:?helm destination is required}}
os=$(env GOTOOLCHAIN=local go env GOOS)
arch=$(env GOTOOLCHAIN=local go env GOARCH)
archive=helm-${version}-${os}-${arch}.tar.gz
url=https://get.helm.sh/${archive}
tmpdir=

if [[ -x "${destination}" ]] && [[ "$("${destination}" version --template '{{.Version}}' 2>/dev/null)" == "${version}" ]]; then
    exit 0
fi

cleanup() {
    rm -rf "${tmpdir}"
}

mkdir -p "$(dirname "${destination}")"
tmpdir=$(mktemp -d "$(dirname "${destination}")/helm.tmp.XXXXXX")
trap cleanup EXIT

curl -fsSL "${url}" -o "${tmpdir}/helm.tar.gz"
curl -fsSL "${url}.sha256sum" -o "${tmpdir}/helm.tar.gz.sha256sum"
(
    cd "${tmpdir}"
    expected=$(awk '{print $1}' helm.tar.gz.sha256sum)
    if command -v sha256sum >/dev/null; then
        echo "${expected}  helm.tar.gz" | sha256sum -c -
    else
        [[ "$(shasum -a 256 helm.tar.gz | awk '{print $1}')" == "${expected}" ]]
    fi
)
tar -xzOf "${tmpdir}/helm.tar.gz" "${os}-${arch}/helm" > "${tmpdir}/helm"
chmod +x "${tmpdir}/helm"
mv "${tmpdir}/helm" "${destination}"
