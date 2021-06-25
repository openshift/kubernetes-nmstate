#!/bin/bash

# Make sure to set the IMAGE_REPO env variable to your quay.io username
# before running this script.

set -ex

export KUBEVIRT_PROVIDER=external
export IMAGE_BUILDER=podman
export DEV_IMAGE_REGISTRY=quay.io
export KUBEVIRTCI_RUNTIME=podman
export SSH=./hack/ssh.sh
export PRIMARY_NIC=enp2s0
export FIRST_SECONDARY_NIC=enp3s0
export SECOND_SECONDARY_NIC=enp4s0

make cluster-sync-operator
# Will fail on subsequent runs, this is fine.
oc create -f build/_output/manifests/scc.yaml || :
oc create -f test/e2e/nmstate.yaml
# On first deployment, it can take a while for all of the pods to come up
# First wait for the handler pods to be created
while ! oc get pods -n nmstate | grep handler; do sleep 1; done
# Then wait for them to be ready
while oc get pods -n nmstate | grep "0/1"; do sleep 1; done
make test-e2e-handler E2E_TEST_ARGS="--skip=user-guide" E2E_TEST_TIMEOUT=120m
