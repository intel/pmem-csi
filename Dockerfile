FROM clearlinux:base AS build

ARG VERSION="unknown"
ARG NDCTL_VERSION="65"
ARG NDCTL_CONFIGFLAGS="--libdir=/usr/lib --disable-docs --without-systemd --without-bash"
ARG NDCTL_BUILD_DEPS="os-core-dev devpkg-util-linux devpkg-kmod devpkg-json-c"

#pull dependencies required for downloading and building libndctl
ARG CACHEBUST
RUN swupd update && swupd bundle-add ${NDCTL_BUILD_DEPS} go-basic-dev && rm -rf /var/lib/swupd

WORKDIR /
RUN curl --fail --location --remote-name https://github.com/pmem/ndctl/archive/v${NDCTL_VERSION}.tar.gz
RUN tar zxvf v${NDCTL_VERSION}.tar.gz && mv ndctl-${NDCTL_VERSION} ndctl
WORKDIR /ndctl
RUN ./autogen.sh
RUN ./configure ${NDCTL_CONFIGFLAGS}
RUN make install

# build pmem-csi-driver
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
    mv _output/pmem-ns-init${BIN_SUFFIX} /go/bin/pmem-ns-init

# build clean container
FROM clearlinux:base
LABEL maintainers="Intel"
LABEL description="PMEM CSI Driver"

# update and install needed bundles/packages:
# file - driver uses file utility to determine filesystem type
# xfsprogs - XFS filesystem utilities
# e2fsprogs- Ext filesystem utilities
# LVM2-bin - for lvm2 and ext4(e2fsprogs) utilities
ARG CACHEBUST
ENV PATH="/bin:/usr/bin"
#NOTE: This below RUN command is intentionally combined all commands, to flatten the docker layers
RUN swupd update && swupd bundle-add mixer && \
# Create custom 'pmem-csi' bundle that holds all the needed packages
    mixin package add file --bundle pmem-csi && \
    mixin package add xfsprogs --bundle pmem-csi && \
    mixin package add e2fsprogs-extras --bundle pmem-csi && \
    mixin package add LVM2-bin --bundle pmem-csi && \
    mixin build && \
# Remove unneeded bundes. swupd is fail to remove child bundles, so we had to list all the bundles mixer pulled.
# NOTE: Do not change the order of below remove bundle list, Otherwise swupd fails to remove them saying failed dependency.
    swupd bundle-remove mixer sysadmin-basic os-installer linux-firmware-extras acpica-unix2 containers-basic git curl diffutils dnf glibc-locale gzip htop iperf iproute2 kbd kernel-install less man-pages minicom openssl parallel patch perl-basic powertop procps-ng pygobject  python3-basic strace sudo tmux tzdata unzip wpa_supplicant linux-firmware-wifi xz zstd libX11client lib-opengl lib-openssl which && \
    swupd update --migrate && \
    swupd bundle-add pmem-csi && \
    swupd clean && \
    rm -rf /var/lib/swupd && \
# default lvm config uses lvmetad and throwing below warning for all lvm tools
# WARNING: Failed to connect to lvmetad. Falling back to device scanning.
# So, ask lvm not to use lvmetad
    mkdir -p /etc/lvm && echo "global { use_lvmetad = 0 }" >> /etc/lvm/lvm.conf

# move required binaries and libraries to clean container
COPY --from=build /usr/lib/libndctl* /usr/lib/
COPY --from=build /usr/lib/libdaxctl* /usr/lib/
RUN mkdir -p /go/bin
COPY --from=build /go/bin/ /go/bin/

ENV LD_LIBRARY_PATH=/usr/lib
ENTRYPOINT ["/go/bin/pmem-csi-driver"]
