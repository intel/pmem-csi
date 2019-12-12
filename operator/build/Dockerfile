# CLEARLINUX_BASE and SWUPD_UPDATE_ARG can be used to make the build reproducible
# by choosing an image by its hash and updating to a certain version with -V:
# CLEAR_LINUX_BASE=clearlinux@sha256:b8e5d3b2576eb6d868f8d52e401f678c873264d349e469637f98ee2adf7b33d4
# SWUPD_UPDATE_ARG=-V 29970
#
# This is used on release branches before tagging a stable version. The master and devel
# branches default to using the latest Clear Linux.
ARG CLEAR_LINUX_BASE=clearlinux:latest
ARG SWUPD_UPDATE_ARG=

# image used to build the PMEM-CSI Operator 
FROM ${CLEAR_LINUX_BASE} AS build
ARG CLEAR_LINUX_BASE
ARG SWUPD_UPDATE_ARG
ARG GO_VERSION="1.13.4"
ARG CACHEBUST
ARG VERSION="unknown"

RUN echo "Updating runtime image from ${CLEAR_LINUX_BASE} to ${SWUPD_UPDATE_ARG:-the latest release}."
RUN swupd update ${SWUPD_UPDATE_ARG} && swupd bundle-add curl c-basic && rm -rf /var/lib/swupd /var/tmp/swupd
RUN curl -L https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz | tar -zxf - -C / && \
    mkdir -p /usr/local/bin/ && \
    for i in /go/bin/*; do ln -s $i /usr/local/bin/; done


ADD . /src/pmem-csi
WORKDIR /src/pmem-csi
RUN make VERSION=${VERSION} pmem-csi-operator && \
    mkdir -p /usr/local/bin && \
    mkdir -p /usr/local/share/package-licenses && \
    mv _output/pmem-csi-operator /usr/local/bin/pmem-csi-operator && \
    hack/copy-modules-license.sh /usr/local/share/package-licenses ./operator/cmd/manager && \
    cp /go/LICENSE /usr/local/share/package-licenses/go.LICENSE && \
    cp LICENSE /usr/local/share/package-licenses/PMEM-CSI.LICENSE && \
    cp /src/pmem-csi/operator/build/bin/user_setup  /usr/local/bin/ && \
    cp /src/pmem-csi/operator/build/bin/entrypoint  /usr/local/bin/

# The actual pmem-csi-operator image.
FROM build as operator
LABEL maintainers="Intel"
LABEL description="Kubernetes operator for PMEM CSI Driver"
COPY --from=build /usr/local/bin/pmem-* /usr/local/bin/
COPY --from=build /bin /usr/local/bin
COPY --from=build /usr/local/share/package-licenses /usr/local/share/package-licenses

ENV OPERATOR=/usr/local/bin/pmem-csi-operator \
    USER_UID=1001 \
    USER_NAME=pmem-csi-operator

RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
