FROM scratch

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=kubernetes-nmstate-operator
LABEL operators.operatorframework.io.bundle.channels.v1=4.12,alpha
LABEL operators.operatorframework.io.bundle.channel.default.v1=4.12
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.22.2
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v3

# Copy files to locations specified by labels.
COPY manifests/4.12/manifests /manifests/
COPY manifests/4.12/metadata /metadata/