# CLEARLINUX_BASE and SWUPD_UPDATE_ARG can be used to make the build reproducible
# by choosing an image by its hash and updating to a certain version with -V:
# CLEAR_LINUX_BASE=clearlinux@sha256:b8e5d3b2576eb6d868f8d52e401f678c873264d349e469637f98ee2adf7b33d4
# SWUPD_UPDATE_ARG=-V 29970
#
# This is used on release branches before tagging a stable version. The master and devel
# branches default to using the latest Clear Linux.
ARG CLEAR_LINUX_BASE=clearlinux@sha256:53b5ef691f487f04c1749fefdeedb7daa4bb034e445ddb26f13220f12dde3b99
ARG SWUPD_UPDATE_ARG="--version=33700"

# Common base image for building PMEM-CSI:
# - up-to-date Clear Linux
# - ndctl installed
FROM ${CLEAR_LINUX_BASE} AS build
ARG CLEAR_LINUX_BASE
ARG SWUPD_UPDATE_ARG

ARG NDCTL_VERSION="68"
ARG NDCTL_CONFIGFLAGS="--disable-docs --without-systemd --without-bash"
ARG NDCTL_BUILD_DEPS="os-core-dev devpkg-util-linux devpkg-kmod devpkg-json-c"
ARG GO_VERSION="1.13.4"

#pull dependencies required for downloading and building libndctl
ARG CACHEBUST
RUN echo "Updating build image from ${CLEAR_LINUX_BASE} to ${SWUPD_UPDATE_ARG:-the latest release}."
RUN swupd update ${SWUPD_UPDATE_ARG} && swupd bundle-add ${NDCTL_BUILD_DEPS} c-basic && rm -rf /var/lib/swupd /var/tmp/swupd
RUN curl -L https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz | tar -zxf - -C / && \
    mkdir -p /usr/local/bin/ && \
    for i in /go/bin/*; do ln -s $i /usr/local/bin/; done

WORKDIR /
RUN curl --fail --location --remote-name https://github.com/pmem/ndctl/archive/v${NDCTL_VERSION}.tar.gz
RUN tar zxvf v${NDCTL_VERSION}.tar.gz && mv ndctl-${NDCTL_VERSION} ndctl
WORKDIR /ndctl
RUN ./autogen.sh
# We install into /usr/local (keeps content separate from OS) but
# then symlink the .pc files to ensure that they are found without
# having to set PKG_CONFIG_PATH. The .pc file doesn't contain an -rpath
# and thus linked binaries do not find the shared libs unless we
# also symlink those.
RUN ./configure --prefix=/usr/local ${NDCTL_CONFIGFLAGS}
RUN make install && \
    ln -s /usr/local/lib/pkgconfig/libndctl.pc /usr/lib64/pkgconfig/ && \
    ln -s /usr/local/lib/pkgconfig/libdaxctl.pc /usr/lib64/pkgconfig/ && \
    for i in /usr/local/lib/lib*.so.*; do ln -s $i /usr/lib64; done

# The source archive has no license file. We link to the copy in GitHub instead.
RUN echo "For source code and licensing of ndctl, see https://github.com/pmem/ndctl/blob/v${NDCTL_VERSION}/COPYING" >/usr/local/lib/NDCTL.COPYING

# Clean image for deploying PMEM-CSI.
FROM ${CLEAR_LINUX_BASE} as runtime
ARG CLEAR_LINUX_BASE
ARG SWUPD_UPDATE_ARG
ARG BIN_SUFFIX
LABEL maintainers="Intel"
LABEL description="PMEM CSI Driver"

# update and install needed bundles:
# file - driver uses file utility to determine filesystem type
# xfsprogs - XFS filesystem utilities
# storge-utils - for lvm2 and ext4(e2fsprogs) utilities
ARG CACHEBUST
RUN echo "Updating runtime image from ${CLEAR_LINUX_BASE} to ${SWUPD_UPDATE_ARG:-the latest release}."
RUN swupd update ${SWUPD_UPDATE_ARG} && swupd bundle-add file xfsprogs storage-utils \
    $(if [ "$BIN_SUFFIX" = "-test" ]; then echo fio; fi) && \
    rm -rf /var/lib/swupd /var/tmp/swupd

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
    make VERSION=${VERSION} pmem-csi-driver${BIN_SUFFIX} pmem-vgm${BIN_SUFFIX} pmem-ns-init${BIN_SUFFIX} pmem-csi-operator${BIN_SUFFIX} && \
    mkdir -p /usr/local/bin && \
    mv _output/pmem-csi-driver${BIN_SUFFIX} /usr/local/bin/pmem-csi-driver && \
    mv _output/pmem-vgm${BIN_SUFFIX} /usr/local/bin/pmem-vgm && \
    mv _output/pmem-ns-init${BIN_SUFFIX} /usr/local/bin/pmem-ns-init && \
    mv _output/pmem-csi-operator${BIN_SUFFIX} /usr/local/bin/pmem-csi-operator && \
    if [ "$BIN_SUFFIX" = "-test" ]; then GOOS=linux GO111MODULE=on \
        go build -o /usr/local/bin/pmem-dax-check ./test/cmd/pmem-dax-check; fi && \
    mkdir -p /usr/local/share/package-licenses && \
    hack/copy-modules-license.sh /usr/local/share/package-licenses ./cmd/pmem-csi-driver ./cmd/pmem-vgm ./cmd/pmem-ns-init ./cmd/pmem-csi-operator && \
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
# All of our custom content is in /usr/local.
COPY --from=binaries /usr/local/lib/libndctl.so.* /usr/local/lib/
COPY --from=binaries /usr/local/lib/libdaxctl.so.* /usr/local/lib/
# We need to overwrite the system libs, hence -f here.
RUN for i in /usr/local/lib/lib*.so.*; do ln -fs $i /usr/lib64; done
COPY --from=binaries /usr/local/bin/pmem-* /usr/local/bin/
COPY --from=binaries /usr/local/share/package-licenses /usr/local/share/package-licenses
COPY --from=binaries /usr/local/share/package-sources /usr/local/share/package-sources
COPY --from=binaries /usr/local/lib/NDCTL.COPYING /usr/local/share/package-licenses/
# default lvm config uses lvmetad and throwing below warning for all lvm tools
# WARNING: Failed to connect to lvmetad. Falling back to device scanning.
# So, ask lvm not to use lvmetad
RUN mkdir -p /etc/lvm
RUN echo "global { use_lvmetad = 0 }" >> /etc/lvm/lvm.conf && \
    echo "activation { udev_sync = 0 udev_rules = 0 }" >> /etc/lvm/lvm.conf

ENV LD_LIBRARY_PATH=/usr/lib
# By default container runs with non-root user
# Choose root user explicitly only where needed, like - node driver
RUN useradd --uid 1000 --user-group --shell /bin/bash pmem-csi
USER 1000
