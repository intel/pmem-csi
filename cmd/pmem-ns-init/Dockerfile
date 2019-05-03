FROM clearlinux:base AS build

ARG VERSION
ARG NDCTL_VERSION
ARG NDCTL_CONFIGFLAGS
ARG NDCTL_BUILD_DEPS

#pull dependencies required for downloading and building libndctl
RUN swupd update && swupd bundle-add ${NDCTL_BUILD_DEPS} go-basic-dev && rm -rf /var/lib/swupd

WORKDIR /
RUN curl --location --remote-name https://github.com/pmem/ndctl/archive/v${NDCTL_VERSION}.tar.gz
RUN tar zxvf v${NDCTL_VERSION}.tar.gz && mv ndctl-${NDCTL_VERSION} ndctl
WORKDIR /ndctl
RUN ./autogen.sh
RUN ./configure ${NDCTL_CONFIGFLAGS}
RUN make install

# build pmem-ns-init
ADD . /go/src/github.com/intel/pmem-csi
WORKDIR /go/src/github.com/intel/pmem-csi
ENV GOPATH=/go
ENV PKG_CONFIG_PATH=/usr/lib/pkgconfig
RUN make pmem-ns-init VERSION=${VERSION}
RUN mkdir -p /go/bin
RUN mv ./_output/pmem-ns-init /go/bin/

# build clean container
FROM clearlinux:base
RUN swupd update && rm -rf /var/lib/swupd

LABEL maintainers="Intel"
LABEL description="PMEM CSI Driver namesapce initializer"
# move required binaries and libraries to clean container
COPY --from=build /usr/lib/libndctl* /usr/lib/
COPY --from=build /usr/lib/libdaxctl* /usr/lib/
RUN mkdir -p /go/bin
COPY --from=build /go/bin/pmem-ns-init /go/bin/

ENV LD_LIBRARY_PATH=/usr/lib
ENTRYPOINT ["/go/bin/pmem-ns-init"]
