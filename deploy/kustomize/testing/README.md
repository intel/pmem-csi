This mixin for a regular production deployment of PMEM-CSI adds port
forwarding to the outside world:

The pmem-csi-controller-testing Service exposes the PMEM-CSI controller's
csi.sock as a TCP service with a dynamically allocated port, on any
node of the cluster. For this to work, the pmem-csi-controller has
to be patched with the controller-socat-patch.yaml.

The pmem-csi-node-testing DaemonSet forwards
/var/lib/kubelet/plugins/pmem-csi.intel.com/csi.sock on all nodes,
using the fixed port 9735 (arbitrarily chosen). The advantage of this
approach is that:
- all nodes can be checked
- simple deployment (no dynamic creation of services)
- normal TCP connections from outside clients (compared to a solution
  like "kubectl exec" with stdin/out forwarding into a socat container
  on a node)
The fixed port of course is the disadvantage.
