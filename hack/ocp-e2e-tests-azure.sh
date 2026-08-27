#!/bin/bash

# Azure has a single eth0 NIC. This runner is now a thin wrapper around the generic cloud runner
# (hack/ocp-e2e-tests-cloud.sh), which works on any cloud; pin PRIMARY_NIC=eth0 for Azure.

set -ex

export PRIMARY_NIC="${PRIMARY_NIC:-eth0}"
exec "$(dirname "$0")/ocp-e2e-tests-cloud.sh"
