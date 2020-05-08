# Kubernetes v1.15 RBAC specific changes

These are the necessary RBAC rules for the sidecar containers that we
use for Kubernetes 1.15. They are (almost) verbatim copies of the
upstream files (because kustomize cannot download them), with just the
ServiceAccount definitions deleted. We could also keep those, they
simply wouldn't be used.

All other modifications are made with kustomize.
