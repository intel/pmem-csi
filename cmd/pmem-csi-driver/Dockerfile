FROM golang:alpine AS build
#pull dependencies required for downloading and building libndctl
RUN apk add --update build-base autoconf automake libtool libuuid json-c kmod asciidoc xmlto kmod-dev eudev-dev util-linux-dev json-c-dev linux-headers wget tar file

WORKDIR /
RUN wget https://github.com/pmem/ndctl/archive/v62.tar.gz
RUN tar zxvf v62.tar.gz
WORKDIR /ndctl-62
RUN ./autogen.sh
RUN ./configure CFLAGS='-g -O2' --prefix=/usr --sysconfdir=/etc --libdir=/usr/lib --disable-docs with_systemd_unit_dir=no
RUN make install

# build csi-pmem-driver
ADD . /go/src/github.com/intel/csi-pmem
WORKDIR /go/src/github.com/intel/csi-pmem
RUN make pmem-csi-driver
RUN mv ./_output/pmem-csi-driver /go/bin/

# build clean container
FROM golang:alpine
LABEL maintainers="Intel"
LABEL description="Pmem CSI Driver"
RUN apk add --update kmod eudev util-linux libuuid e2fsprogs lvm2 file
# move required binaries and libraries to clean container
COPY --from=build /usr/lib/libndctl* /usr/lib/
COPY --from=build /usr/lib/libdaxctl* /usr/lib/
RUN mkdir -p /go/bin
COPY --from=build /go/bin/pmem-csi-driver /go/bin/

ENTRYPOINT ["/go/bin/pmem-csi-driver"]
