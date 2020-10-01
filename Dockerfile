# Image builds are not reproducible because the base layer is changing over time.
ARG LINUX_BASE=debian:buster-slim

# Common base image for building PMEM-CSI and running CI tests.
FROM ${LINUX_BASE} AS build
ARG APT_GET="env DEBIAN_FRONTEND=noninteractive apt-get"

ARG GO_VERSION="1.15.2"

# CACHEBUST is set by the CI when building releases to ensure that apt-get really gets
# run instead of just using some older, cached result.
ARG CACHEBUST

# In contrast to the runtime image below, here we can afford to install additional
# tools and recommended packages. But this image gets pushed to a registry by the CI as a cache,
# so it still makes sense to keep this layer small by removing /var/cache.
RUN ${APT_GET} update && \
    ${APT_GET} install -y gcc libndctl-dev make git curl iproute2 pkg-config xfsprogs e2fsprogs parted openssh-client python3 python3-venv && \
    rm -rf /var/cache/*
RUN curl -L https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz | tar -zxf - -C / && \
    mkdir -p /usr/local/bin/ && \
    for i in /go/bin/*; do ln -s $i /usr/local/bin/; done

# Clean image for deploying PMEM-CSI.
FROM ${LINUX_BASE} as runtime
ARG APT_GET="env DEBIAN_FRONTEND=noninteractive apt-get"
ARG CACHEBUST
ARG BIN_SUFFIX
LABEL maintainers="Intel"
LABEL description="PMEM CSI Driver"

# Update and install the minimal amount of additional packages that
# are needed at runtime:
# file - driver uses file utility to determine filesystem type
# xfsprogs, e2fsprogs - formating filesystems
# lvm2 - volume management
# ndctl - pulls in the necessary library, useful by itself
# fio - only included in testing images
RUN ${APT_GET} update && \
    ${APT_GET} upgrade -y --no-install-recommends && \
    ${APT_GET} install -y --no-install-recommends file xfsprogs e2fsprogs lvm2 ndctl \
       $(if [ "$BIN_SUFFIX" = "-test" ]; then echo fio; fi) && \
    rm -rf /var/cache/*

# Image in which PMEM-CSI binaries get built.
FROM build as binaries

# build pmem-csi-driver
ARG VERSION="unknown"
ADD . /src/pmem-csi
ENV PKG_CONFIG_PATH=/usr/lib/pkgconfig/
WORKDIR /src/pmem-csi
ARG BIN_SUFFIX

# If "docker build" is invoked with the "vendor" directory correctly
# populated, then this argument can be set to -mod=vendor. "make
# build-images" does both automatically.
ARG GOFLAGS=

# Here we choose explicitly which binaries we want in the image and in
# which flavor (production or testing). The actual binary name in the
# image is going to be the same, to avoid unnecessary deployment
# differences.
RUN set -x && \
    make VERSION=${VERSION} pmem-csi-driver${BIN_SUFFIX} pmem-csi-operator${BIN_SUFFIX} && \
    mkdir -p /usr/local/bin && \
    mv _output/pmem-csi-driver${BIN_SUFFIX} /usr/local/bin/pmem-csi-driver && \
    mv _output/pmem-csi-operator${BIN_SUFFIX} /usr/local/bin/pmem-csi-operator && \
    if [ "$BIN_SUFFIX" = "-test" ]; then GOOS=linux GO111MODULE=on \
        go build -o /usr/local/bin/pmem-dax-check ./test/cmd/pmem-dax-check; fi && \
    mkdir -p /usr/local/share/package-licenses && \
    hack/copy-modules-license.sh /usr/local/share/package-licenses ./cmd/pmem-csi-driver ./cmd/pmem-csi-operator && \
    cp /go/LICENSE /usr/local/share/package-licenses/go.LICENSE && \
    cp LICENSE /usr/local/share/package-licenses/PMEM-CSI.LICENSE

# Some of the licenses might require us to distribute source code.
# We cannot just point to the upstream repos because those might
# disappear. We could host a copy at a location under our control,
# but keeping that in sync with the published container images
# would be tricky. So what we do instead is copy the (small!)
# source code which has this requirement into the image.
RUN set -x && \
    mkdir -p /usr/local/share/package-sources && \
    for license in $(grep -l -r -w -e MPL -e GPL -e LGPL /usr/local/share/package-licenses | sed -e 's;^/usr/local/share/package-licenses/;;'); do \
        if ! (dir=$(dirname $license) && \
              tar -Jvcf /usr/local/share/package-sources/$(echo $dir | tr / _).tar.xz vendor/$dir ); then \
              exit 1; \
        fi; \
    done; \
    ls -l /usr/local/share/package-sources

# The actual pmem-csi-driver image.
FROM runtime as pmem

# Move required binaries and libraries to clean container.
COPY --from=binaries /usr/local/bin/pmem-* /usr/local/bin/
COPY --from=binaries /usr/local/share/package-licenses /usr/local/share/package-licenses
COPY --from=binaries /usr/local/share/package-sources /usr/local/share/package-sources

# Don't rely on udevd, it isn't available (https://unix.stackexchange.com/questions/591724/how-to-add-a-block-to-udev-database-that-works-after-reboot).
# Same with D-Bus.
# Backup and archival of metadata inside the container is useless.
RUN sed -i \
        -e 's/udev_sync = 1/udev_sync = 0/' \
        -e 's/udev_rules = 1/udev_rules = 0/' \
        -e 's/obtain_device_list_from_udev = 1/obtain_device_list_from_udev = 0/' \
        -e 's/multipath_component_detection = 1/multipath_component_detection = 0/' \
        -e 's/md_component_detection = 1/md_component_detection = 0/' \
        -e 's/notify_dbus = 1/notify_dbus = 0/' \
        -e 's/backup = 1/backup = 0/' \
        -e 's/archive = 1/archive = 0/' \
        /etc/lvm/lvm.conf

ENV LD_LIBRARY_PATH=/usr/lib
# By default container runs with non-root user
# Choose root user explicitly only where needed, like - node driver
RUN useradd --uid 1000 --user-group --shell /bin/bash pmem-csi
USER 1000
