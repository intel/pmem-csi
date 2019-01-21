FROM golang:alpine AS build
#pull dependencies required for downloading and building libndctl
RUN apk add --update build-base autoconf automake bash-completion libtool libuuid json-c kmod asciidoc xmlto kmod-dev eudev-dev util-linux-dev json-c-dev linux-headers wget tar file

ARG NDCTLVER
ARG NDCTL_CONFIGFLAGS
WORKDIR /
RUN wget https://github.com/pmem/ndctl/archive/v${NDCTLVER}.tar.gz
RUN tar zxvf v${NDCTLVER}.tar.gz && mv ndctl-${NDCTLVER} ndctl
WORKDIR /ndctl
RUN ./autogen.sh
RUN ./configure ${NDCTL_CONFIGFLAGS}
RUN make install

# build pmem-ns-init
ADD . /go/src/github.com/intel/pmem-csi
WORKDIR /go/src/github.com/intel/pmem-csi
RUN make pmem-ns-init
RUN mv ./_output/pmem-ns-init /go/bin/

# build clean container
FROM golang:alpine
LABEL maintainers="Intel"
LABEL description="Pmem CSI Driver namesapce initializer"
RUN apk add --update kmod eudev util-linux libuuid e2fsprogs
# move required binaries and libraries to clean container
COPY --from=build /usr/lib/libndctl* /usr/lib/
COPY --from=build /usr/lib/libdaxctl* /usr/lib/
RUN mkdir -p /go/bin
COPY --from=build /go/bin/pmem-ns-init /go/bin/

ENTRYPOINT ["/go/bin/pmem-ns-init"]
