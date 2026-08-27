#!/bin/bash

# Generic cloud runner for the kubernetes-nmstate handler e2e suite.
#
# Unlike hack/ocp-e2e-tests-handler.sh -- which targets bare-metal dev-scripts and requires
# ${SHARED_DIR}/packet-conf.sh, a bastion SSH jump host, and dedicated secondary NICs -- this
# runner works on any IPI cloud cluster (AWS/Azure/GCP), connected or disconnected:
#
#   * node access is via `oc debug node` (hack/ssh-via-kubectl.sh), not a bastion;
#   * it runs in single-NIC mode (cloud VMs have one usable NIC, enslaved to br-ex under
#     OVN-Kubernetes), so bond/secondary-NIC tests are not attempted;
#   * the primary NIC name is auto-detected (override with PRIMARY_NIC), since it differs per
#     cloud (Azure eth0, AWS ens5, GCP ens4);
#   * it runs the single-NIC / OVN-safe subset (DNS configuration), matching the behaviour of
#     the previous Azure-only runner.
#
# hack/ocp-e2e-tests-azure.sh delegates here with PRIMARY_NIC=eth0.

set -ex

export KUBEVIRT_PROVIDER=external
export IMAGE_BUILDER="${IMAGE_BUILDER:-podman}"
export DEV_IMAGE_REGISTRY="${DEV_IMAGE_REGISTRY:-quay.io}"
export KUBEVIRTCI_RUNTIME="${KUBEVIRTCI_RUNTIME:-podman}"
export FLAKE_ATTEMPTS="${FLAKE_ATTEMPTS:-3}"
export NAMESPACE="${HANDLER_NAMESPACE:-nmstate}"

# Deploy the operator (this consumes the operator/handler images -- on a disconnected cluster
# they are served from the mirror registry) and the NMState CR, then wait for the handler pods.
make cluster-sync-operator
oc create -f test/e2e/nmstate.yaml
# On first deployment, it can take a while for all of the pods to come up.
# First wait for the handler pods to be created, then wait for them to be ready.
while ! oc get pods -n "${NAMESPACE}" | grep handler; do sleep 1; done
while oc get pods -n "${NAMESPACE}" | grep "0/1"; do sleep 1; done

# Determine the physical uplink NIC. Cloud VMs have a single NIC that, under OVN-Kubernetes, is
# enslaved to br-ex, and its name differs per platform, so auto-detect it unless the caller pins
# PRIMARY_NIC. The executed subset (see below) does not reconfigure this interface, so this value
# is used only for read-only assertions; detection is therefore best-effort with a safe fallback.
function detect_primary_nic() {
  ( set +e +x
    local node
    node="$(oc get nodes -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
    [ -z "${node}" ] && exit 0
    # The single-quoted script below runs on the node via `oc debug`; its variables must expand
    # remotely, not locally.
    # shellcheck disable=SC2016
    oc -n default debug "node/${node}" -- chroot /host bash -c '
      # Prefer the physical port of br-ex (OVN); else the default-route device; else the first
      # physical ethernet device.
      if command -v ovs-vsctl >/dev/null 2>&1 && ovs-vsctl br-exists br-ex 2>/dev/null; then
        for p in $(ovs-vsctl list-ports br-ex 2>/dev/null); do
          [ -e "/sys/class/net/${p}/device" ] && { echo "${p}"; exit 0; }
        done
      fi
      dev="$(ip -o route get 1.1.1.1 2>/dev/null | sed -n "s/.* dev \([^ ]*\).*/\1/p" | head -1)"
      [ -n "${dev}" ] && [ "${dev}" != "br-ex" ] && [ -e "/sys/class/net/${dev}/device" ] && { echo "${dev}"; exit 0; }
      for d in /sys/class/net/*/device; do
        n="$(basename "$(dirname "${d}")")"
        case "${n}" in en*|eth*) echo "${n}"; exit 0;; esac
      done
    ' 2>/dev/null | tr -d "\r" | tail -1
  )
}

export PRIMARY_NIC="${PRIMARY_NIC:-$(detect_primary_nic)}"
export PRIMARY_NIC="${PRIMARY_NIC:-eth0}"
echo "Using PRIMARY_NIC=${PRIMARY_NIC}"

export ENV_WITH_ONLY_ONE_NIC=True
export SSH="./hack/ssh-via-kubectl.sh"

# Single-NIC / OVN-safe subset: the "Dns configuration" specs. The "with DHCP" contexts
# reconfigure the uplink NIC (not possible with a single OVN-managed NIC), so they are skipped --
# the remaining specs configure dns-resolver without touching the uplink.
FOCUS_TESTS="Dns configuration"
SKIPPED_TESTS="with DHCP"
make test-e2e-handler E2E_TEST_ARGS="--focus=\"${FOCUS_TESTS}\" --skip=\"${SKIPPED_TESTS}\" --flake-attempts=${FLAKE_ATTEMPTS}" E2E_TEST_TIMEOUT=4h
