FROM golang:alpine AS build

ARG VERSION
ARG NDCTL_VERSION
ARG NDCTL_CONFIGFLAGS
ARG NDCTL_BUILD_DEPS
#pull dependencies required for downloading and building libndctl
RUN apk add --update ${NDCTL_BUILD_DEPS}

WORKDIR /
RUN wget https://github.com/pmem/ndctl/archive/v${NDCTL_VERSION}.tar.gz
RUN tar zxvf v${NDCTL_VERSION}.tar.gz && mv ndctl-${NDCTL_VERSION} ndctl
WORKDIR /ndctl
RUN ./autogen.sh
RUN ./configure ${NDCTL_CONFIGFLAGS}
RUN make install

# build pmem-csi-driver
ADD . /go/src/github.com/intel/pmem-csi
WORKDIR /go/src/github.com/intel/pmem-csi
RUN make pmem-csi-driver VERSION=${VERSION}
RUN mv ./_output/pmem-csi-driver /go/bin/

# build clean container
FROM golang:alpine
LABEL maintainers="Intel"
LABEL description="Pmem CSI Driver"
RUN apk add --update kmod eudev util-linux libuuid e2fsprogs xfsprogs lvm2 file
# move required binaries and libraries to clean container
COPY --from=build /usr/lib/libndctl* /usr/lib/
COPY --from=build /usr/lib/libdaxctl* /usr/lib/
RUN mkdir -p /go/bin
COPY --from=build /go/bin/pmem-csi-driver /go/bin/

ENTRYPOINT ["/go/bin/pmem-csi-driver"]
