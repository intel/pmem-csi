# CLEARLINUX_BASE and SWUPD_UPDATE_ARG can be used to make the build reproducible
# by choosing an image by its hash and updating to a certain version with -V:
# CLEAR_LINUX_BASE=clearlinux@sha256:b8e5d3b2576eb6d868f8d52e401f678c873264d349e469637f98ee2adf7b33d4
# SWUPD_UPDATE_ARG=-V 29970
#
# This is used on release branches before tagging a stable version. The master and devel
# branches default to using the latest Clear Linux.
ARG CLEAR_LINUX_BASE=clearlinux@sha256:879d7e557640035178284798d78ca283b16c5e51de30a31b635b13626087f80f
ARG SWUPD_UPDATE_ARG="--version=31440"

# Common base image for building PMEM-CSI:
# - up-to-date Clear Linux
# - ndctl installed
FROM ${CLEAR_LINUX_BASE} AS build
ARG CLEAR_LINUX_BASE
ARG SWUPD_UPDATE_ARG

ARG NDCTL_VERSION="66"
ARG NDCTL_CONFIGFLAGS="--disable-docs --without-systemd --without-bash"
ARG NDCTL_BUILD_DEPS="os-core-dev devpkg-util-linux devpkg-kmod devpkg-json-c"
ARG GO_VERSION="1.12.9"

#pull dependencies required for downloading and building libndctl
ARG CACHEBUST
RUN echo "Updating build image from ${CLEAR_LINUX_BASE} to ${SWUPD_UPDATE_ARG:-the latest release}."
RUN swupd update ${SWUPD_UPDATE_ARG} && swupd bundle-add ${NDCTL_BUILD_DEPS} c-basic && rm -rf /var/lib/swupd /var/tmp/swupd
# Workaround for "pkg-config: error while loading shared libraries" when using older Docker
# (see https://github.com/clearlinux/distribution/issues/831)
RUN ldconfig
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

# Workaround for "error while loading shared libraries: libndctl.so.6" when using older Docker (?)
# and running "make test" inside this container.
# - same as https://github.com/clearlinux/distribution/issues/831?
RUN ldconfig

# Clean image for deploying PMEM-CSI.
FROM ${CLEAR_LINUX_BASE} as runtime
ARG CLEAR_LINUX_BASE
ARG SWUPD_UPDATE_ARG
LABEL maintainers="Intel"
LABEL description="PMEM CSI Driver"

# update and install needed bundles:
# file - driver uses file utility to determine filesystem type
# xfsprogs - XFS filesystem utilities
# storge-utils - for lvm2 and ext4(e2fsprogs) utilities
ARG CACHEBUST
RUN echo "Updating runtime image from ${CLEAR_LINUX_BASE} to ${SWUPD_UPDATE_ARG:-the latest release}."
RUN swupd update ${SWUPD_UPDATE_ARG} && swupd bundle-add file xfsprogs storage-utils && rm -rf /var/lib/swupd /var/tmp/swupd
# Workaround for "pkg-config: error while loading shared libraries" when using older Docker
# (see https://github.com/clearlinux/distribution/issues/831)
RUN ldconfig

# Image in which PMEM-CSI binaries get built.
FROM build as binaries

# build pmem-csi-driver
ARG VERSION="unknown"
ADD . /go/src/github.com/intel/pmem-csi
ENV GOPATH=/go
ENV PKG_CONFIG_PATH=/usr/lib/pkgconfig/
WORKDIR /go/src/github.com/intel/pmem-csi
ARG BIN_SUFFIX
# Here we choose explicitly which binaries we want in the image and in
# which flavor (production or testing). The actual binary name in the
# image is going to be the same, to avoid unnecessary deployment
# differences.
RUN make VERSION=${VERSION} pmem-csi-driver${BIN_SUFFIX} pmem-vgm${BIN_SUFFIX} pmem-ns-init${BIN_SUFFIX} && \
    mkdir -p /go/bin/ && \
    mv _output/pmem-csi-driver${BIN_SUFFIX} /go/bin/pmem-csi-driver && \
    mv _output/pmem-vgm${BIN_SUFFIX} /go/bin/pmem-vgm && \
    mv _output/pmem-ns-init${BIN_SUFFIX} /go/bin/pmem-ns-init && \
    cp LICENSE /go/bin/PMEM-CSI.LICENSE

# The actual pmem-csi-driver image.
FROM runtime as pmem

# Move required binaries and libraries to clean container.
# All of our custom content is in /usr/local.
COPY --from=binaries /usr/local/lib/libndctl.so.* /usr/local/lib/
COPY --from=binaries /usr/local/lib/libdaxctl.so.* /usr/local/lib/
COPY --from=binaries /usr/local/lib/NDCTL.COPYING /usr/local/lib/
# We need to overwrite the system libs, hence -f here.
RUN for i in /usr/local/lib/lib*.so.*; do ln -fs $i /usr/lib64; done
COPY --from=binaries /go/bin/ /usr/local/bin/
# default lvm config uses lvmetad and throwing below warning for all lvm tools
# WARNING: Failed to connect to lvmetad. Falling back to device scanning.
# So, ask lvm not to use lvmetad
RUN mkdir -p /etc/lvm
RUN echo "global { use_lvmetad = 0 }" >> /etc/lvm/lvm.conf && \
    echo "activation { udev_sync = 0 udev_rules = 0 }" >> /etc/lvm/lvm.conf

ENV LD_LIBRARY_PATH=/usr/lib
ENTRYPOINT ["/usr/local/bin/pmem-csi-driver"]
