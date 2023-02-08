#!/bin/bash -xe

# This script publish kubernetes-nmstate-handler by default at quay.io/nmstate
# organization to publish elsewhere export the following env vars
# IMAGE_REGISTRY
# IMAGE_REPO
# To run it just do proper docker login and automation/publish.sh

IMAGE_BUILDER=${IMAGE_BUILDER:-$(./hack/detect_cri.sh)}

image_registry=${IMAGE_REGISTRY:-quay.io}
image_repo=${IMAGE_REPO:-nmstate}

source automation/check-patch.setup.sh
cd ${TMP_PROJECT_PATH}

push_knmstate_containers() {
    cd ${TMP_PROJECT_PATH}
    make \
        IMAGE_REGISTRY=${image_registry}  \
        IMAGE_REPO=${image_repo} \
        push-handler \
        push-operator
}

publish_docs() {
    # Update gh-pages branch with the generated documentation
    ${IMAGE_BUILDER} run -v $(pwd)/docs:/docs/ docker.io/library/ruby:3.1 make -C /docs install build
    rm -rf /tmp/gh-pages
    git clone --single-branch http://github.com/nmstate/kubernetes-nmstate -b gh-pages /tmp/gh-pages
    rsync -rt --links --cvs-exclude docs/build/kubernetes-nmstate/* /tmp/gh-pages
    (
        cd /tmp/gh-pages
        git add -A
        git config user.name $GITHUB_USER
        git config user.email $GITHUB_EMAIL
        git commit -m "updated $(date +"%d.%m.%Y %H:%M:%S")"
        git push https://${GITHUB_USER}@github.com/nmstate/kubernetes-nmstate gh-pages
    )
    echo -e "\033[0;32mdemo updated $(date +"%d.%m.%Y %H:%M:%S")\033[0m"
}

push_knmstate_containers
if [ "$PULL_BASE_REF" == main ]; then
    publish_docs
fi
