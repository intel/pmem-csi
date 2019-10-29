# Kubernetes v1.16 specific changes

There are no specific deployment changes needed for Kubernetes v1.16 except
that the CSIDriver supports new field of specifying volume life-cycle modes
supported by the driver. So we take Kubernetes-1.15 final
deployments (not base overlays) and patch with this new field.
