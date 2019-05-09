FROM clearlinux:base AS build

ARG VERSION
ARG NDCTL_VERSION
ARG NDCTL_CONFIGFLAGS
ARG NDCTL_BUILD_DEPS

#pull dependencies required for downloading and building libndctl
ARG CACHEBUST
RUN swupd update && swupd bundle-add ${NDCTL_BUILD_DEPS} go-basic-dev && rm -rf /var/lib/swupd

WORKDIR /
RUN curl --location --remote-name https://github.com/pmem/ndctl/archive/v${NDCTL_VERSION}.tar.gz
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
RUN make VERSION=${VERSION} OUTPUT_DIR=/go/bin

# build clean container
FROM clearlinux:base
LABEL maintainers="Intel"
LABEL description="PMEM CSI Driver"

# update and install needed bundles:
# file - driver uses file utility to determine filesystem type
# xfsprogs - XFS filesystem utilities
# storge-utils - for lvm2 and ext4(e2fsprogs) utilities
ARG CACHEBUST
RUN swupd update && swupd bundle-add file xfsprogs storage-utils && rm -rf /var/lib/swupd

# move required binaries and libraries to clean container
COPY --from=build /usr/lib/libndctl* /usr/lib/
COPY --from=build /usr/lib/libdaxctl* /usr/lib/
RUN mkdir -p /go/bin
COPY --from=build /go/bin/ /go/bin/
# default lvm config uses lvmetad and throwing below warning for all lvm tools
# WARNING: Failed to connect to lvmetad. Falling back to device scanning.
# So, ask lvm not to use lvmetad
RUN mkdir -p /etc/lvm
RUN echo "global { use_lvmetad = 0 }" >> /etc/lvm/lvm.conf

ENV LD_LIBRARY_PATH=/usr/lib
ENTRYPOINT ["/go/bin/pmem-csi-driver"]
