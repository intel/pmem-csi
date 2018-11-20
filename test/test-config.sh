# This file is meant to be sourced into various scripts in this directory and provides
# some common settings.

# Prefix for network devices etc.
TEST_PREFIX=csipmem

# IPv4 base address. .1 is used for the host, which also acts
# as NAT gateway. Even numbers are for the guests (.2, .4, ...).
TEST_IP_ADDR=192.168.8

# Additional Clear Linux bundles.
TEST_CLEAR_LINUX_BUNDLES=""

# Post-install command for each virtual machine. Called with the
# current image number (0 to n-1) as parameter.
TEST_CLEAR_LINUX_POST_INSTALL=

# Called after Kubernetes has been configured and started on the master node.
TEST_CONFIGURE_POST_MASTER=

# Called after Kubernetes has been configured and started on all nodes.
TEST_CONFIGURE_POST_ALL=
