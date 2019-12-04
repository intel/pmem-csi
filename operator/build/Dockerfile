FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

ENV OPERATOR=/usr/local/bin/pmem-csi-operator \
    USER_UID=1001 \
    USER_NAME=pmem-csi-operator

# install operator binary
COPY build/_output/bin/pmem-csi-operator ${OPERATOR}

COPY build/bin /usr/local/bin
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
