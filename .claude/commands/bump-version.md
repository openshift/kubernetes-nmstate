---
description: Bump OpenShift version references (e.g., from 4.21 to 4.22)
---

Bump the OpenShift version from $ARGUMENTS across all relevant files in the repository.

## Arguments

This command expects the version change in format: `OLD_VERSION NEW_VERSION`
Example: `/bump-version 4.21 4.22`

## Files to Update

The following files contain OpenShift version references that need to be updated:

### Build Configuration
1. `.ci-operator.yaml` - build root image tag (e.g., `rhel-9-release-golang-1.24-openshift-4.XX`)
2. `build/Dockerfile.openshift` - builder and base images
3. `build/Dockerfile.operator.openshift` - builder and base images

### OLM Manifests
4. `manifests/bases/kubernetes-nmstate-operator.clusterserviceversion.yaml`:
   - `containerImage` annotation
   - `olm.skipRange` annotation
   - `name` (operator version)
   - `olm-status-descriptors` label

5. `manifests/kubernetes-nmstate-operator.package.yaml`:
   - `currentCSV` version

6. `manifests/stable/manifests/image-references`:
   - All image tags (operator, handler, console-plugin, kube-rbac-proxy)

7. `manifests/stable/manifests/kubernetes-nmstate-operator.clusterserviceversion.yaml`:
   - `containerImage` annotation
   - `olm.skipRange` annotation
   - `name` (operator version)
   - `HANDLER_IMAGE` env var
   - `PLUGIN_IMAGE` env var
   - `KUBE_RBAC_PROXY_IMAGE` env var
   - `image` for operator container
   - `olm-status-descriptors` label
   - `version` field

## Files to EXCLUDE

Do NOT modify these files even if they contain matching version patterns:
- `logo/*.svg` - graphic assets with unrelated version numbers
- `docs/Gemfile.lock` - Ruby gem dependencies (unrelated)
- `vendor/` - vendored dependencies (contains code comments referencing HTML spec sections)
- `api/vendor/` - API vendored dependencies

## Execution Steps

1. Parse the OLD_VERSION and NEW_VERSION from arguments
2. For each file listed above, replace `OLD_VERSION` with `NEW_VERSION` in version-specific contexts
3. Show a summary of all changes made
4. Remind user to run `make ocp-update-bundle-manifests` if needed and verify changes before committing

## Version Pattern Examples

The version appears in these patterns:
- Image tags: `origin-kubernetes-nmstate-operator:4.XX`
- Operator versions: `kubernetes-nmstate-operator.v4.XX.0`
- OLM skip ranges: `>=4.3.0 <4.XX.0`
- CI operator tags: `openshift-4.XX`
- Base images: `ocp/4.XX:base-rhel9`
- Builder images: `ocp/builder:rhel-9-golang-1.XX-openshift-4.XX`
