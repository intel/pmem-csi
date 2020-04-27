/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"crypto/rsa"
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	grpcserver "github.com/intel/pmem-csi/pkg/grpc-server"
	pmemtls "github.com/intel/pmem-csi/pkg/pmem-csi-operator/pmem-tls"
	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	"github.com/intel/pmem-csi/pkg/version"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

const (
	controllerServicePort = 10000
	controllerMetricsPort = 10010
	nodeControllerPort    = 10001
)

type PmemCSIDriver struct {
	*api.Deployment
	// operators namespace used for creating sub-resources
	namespace string
}

// checkIfNameClash check if d.Spec.DriverName is not clashing with any of the
// known deploments by the reconciler. If clash found returns the reference of
// that deployment else nil.
func (d *PmemCSIDriver) checkIfNameClash(r *ReconcileDeployment) *api.Deployment {
	for _, dep := range r.deployments {
		if dep.Spec.DriverName == d.Spec.DriverName &&
			dep.Name != d.Name {
			return dep
		}
	}

	return nil
}

// Reconcile reconciles the driver deployment
func (d *PmemCSIDriver) Reconcile(r *ReconcileDeployment) (bool, error) {
	changes := map[api.DeploymentChange]struct{}{}

	oldDeployment, foundInCache := r.deployments[d.Name]
	if foundInCache {
		changes = d.Compare(oldDeployment)
		// We should not trust the deployment status, as it might be tampered.
		// Use existing objects Status instead.
		oldDeployment.Status.DeepCopyInto(&d.Status)
	} else {
		var err error
		// Possibly that operator restarted, check if any conflicting changes
		// to deployment
		changes, err = d.changesFromPreDeployed(r)
		if err != nil {
			return true, err
		}

		// This is probably not the right solution for https://github.com/intel/pmem-csi/issues/617.
		// Instead of getting a list of changes, d.changesFromPreDeployed should tell us what
		// the old deployment looks like. The change list just gets ignored anyway.
		oldDeployment = d.Deployment
	}

	klog.Infof("Deployment: %q, state: %q", d.Name, d.Status.Phase)

	switch d.Status.Phase {
	// We treat same both new and failed deployments
	case api.DeploymentPhaseNew, api.DeploymentPhaseFailed:
		if dep := d.checkIfNameClash(r); dep != nil {
			d.Status.Phase = api.DeploymentPhaseFailed
			return true, fmt.Errorf("driver name %q is already taken by deployment %q",
				dep.Spec.DriverName, dep.Name)
		}
		// Update local cache so that we can catch if any change in deployment
		r.deployments[d.Name] = d.Deployment
	}

	// Deployment successfull, so no more reconcile needed for this deployment
	requeue, err := d.reconcileDeploymentChanges(r, oldDeployment, changes, foundInCache)
	if err == nil {
		// If everything ok, update local cache
		r.deployments[d.Name] = d.Deployment

		// TODO: wait for functional driver before entering "running" phase.
		// For now we go straight to it.
		d.Status.Phase = api.DeploymentPhaseRunning
	} else if d.Status.Phase == api.DeploymentPhaseNew {
		d.Status.Phase = api.DeploymentPhaseFailed
	}

	return requeue, err
}

// reconcileDeploymentChanges examines the changes and updates the appropriate objects.
// Reverts deployment if and incompatible found compareted to existing deployment, such as deviceMode, driverName, pmemSpace.
// In case of foundInCache is false, all the objects gets refreshed/updated.
func (d *PmemCSIDriver) reconcileDeploymentChanges(r *ReconcileDeployment, existing *api.Deployment, changes map[api.DeploymentChange]struct{}, foundInCache bool) (bool, error) {

	klog.Infof("Changes detected: %v", changes)

	updateController := false
	updateNodeDriver := false
	updateDeployment := false
	updateSecrets := false
	updateAll := false // Update all objects of the deployment

	if !foundInCache {
		// Running deployment not found in cache, possibly result of operator
		// restart, refresh all objects.
		updateAll = true
	}

	for c := range changes {
		var err error
		switch c {
		case api.ControllerResources, api.ProvisionerImage:
			updateController = true
		case api.NodeResources, api.NodeRegistrarImage:
			updateNodeDriver = true
		case api.DriverImage, api.LogLevel, api.PullPolicy:
			updateController = true
			updateNodeDriver = true
		case api.DriverName:
			if dep := d.checkIfNameClash(r); dep != nil {
				err = fmt.Errorf("cannot update deployment(%q) driver name as name %q is already taken by deployment %q",
					d.Name, d.Spec.DriverName, dep.Name)

				// DriverName cannot be updated, revert from deployment spec
				d.Spec.DriverName = existing.Spec.DriverName
				updateDeployment = true
			} else {
				updateController = true
				updateNodeDriver = true
			}
		case api.DriverMode:
			d.Spec.DeviceMode = existing.Spec.DeviceMode
			updateDeployment = true
			err = fmt.Errorf("changing %q of a running deployment %q is not allowed", c, d.Name)
		case api.NodeSelector:
			updateNodeDriver = true
		case api.PMEMPercentage:
			d.Spec.PMEMPercentage = existing.Spec.PMEMPercentage
			updateDeployment = true
			err = fmt.Errorf("changing %q of a running deployment %q is not allowed", c, d.Name)
		case api.Labels:
			d.Spec.Labels = existing.Spec.Labels
			updateDeployment = true
			err = fmt.Errorf("changing %q of a running deployment %q is not allowed", c, d.Name)
		case api.CACertificate, api.RegistryCertificate, api.NodeControllerCertificate:
			updateSecrets = true
		}

		if err != nil {
			klog.Warningf("Incompatible change of deployment occurred: %v", err)
		}
	}

	// Reject changes which cannot be applied
	// by updating the deployment object
	if updateDeployment {
		klog.Infof("Updating deployment %q", d.Name)
		if err := r.Update(d.Deployment); err != nil {
			return true, err
		}
	}

	objects := []runtime.Object{}

	// Force update all deployment objects
	if updateAll {
		klog.Infof("Updating all objects for deployment %q", d.Name)
		objs, err := d.getDeploymentObjects(r)
		if err != nil {
			return true, err
		}
		objects = append(objects, objs...)
	} else {
		if updateSecrets {
			objs, err := d.getSecrets(r)
			if err != nil {
				return true, err
			}
			objects = append(objects, objs...)
		}
		if updateController {
			klog.Infof("Updating controller driver for deployment %q", d.Name)
			objects = append(objects, d.getControllerStatefulSet())
		}
		if updateNodeDriver {
			klog.Infof("Updating node driver for deployment %q", d.Name)
			objects = append(objects, d.getNodeDaemonSet())
		}
	}

	for _, obj := range objects {
		// Services needs special treatment as they have some immutable field(s)
		// So, we cannot refresh the existing one with new service object.
		if s, ok := obj.(*corev1.Service); ok {
			existingService := &corev1.Service{
				TypeMeta:   s.TypeMeta,
				ObjectMeta: s.ObjectMeta,
			}
			err := r.Get(existingService)
			if err != nil {
				if !errors.IsNotFound(err) {
					return true, err
				}
				// Create the missing service now
				klog.Infof("'%s' service not found, creating new one", s.GetName())
				if err := r.Create(s); err != nil {
					return true, err
				}
				continue
			}

			existingService.Spec.Ports = []corev1.ServicePort{}
			for _, p := range s.Spec.Ports {
				existingService.Spec.Ports = append(existingService.Spec.Ports, p)
			}
			existingService.Spec.Selector = s.Spec.Selector
			klog.Infof("updating service '%s' service ports and selector", s.GetName())
			if err := r.Update(existingService); err != nil {
				return true, err
			}
		} else {
			if err := r.UpdateOrCreate(obj); err != nil {
				return true, err
			}
		}
	}

	return false, nil
}

func (d *PmemCSIDriver) deployObjects(r *ReconcileDeployment) error {
	objects, err := d.getDeploymentObjects(r)
	if err != nil {
		return err
	}
	for _, obj := range objects {
		if err := r.Create(obj); err != nil {
			return err
		}
	}
	return nil
}

// getDeploymentObjects returns all objects that are part of a driver deployment.
func (d *PmemCSIDriver) getDeploymentObjects(r *ReconcileDeployment) ([]runtime.Object, error) {
	objects, err := d.getSecrets(r)
	if err != nil {
		return nil, err
	}

	objects = append(objects,
		d.getCSIDriver(r.k8sVersion),
		d.getControllerServiceAccount(),
		d.getControllerProvisionerRole(),
		d.getControllerProvisionerRoleBinding(),
		d.getControllerProvisionerClusterRole(),
		d.getControllerProvisionerClusterRoleBinding(),
		d.getControllerService(),
		d.getMetricsService(),
		d.getControllerStatefulSet(),
		d.getNodeDaemonSet(),
	)

	return objects, nil
}

func (d *PmemCSIDriver) getSecrets(r *ReconcileDeployment) ([]runtime.Object, error) {
	// Encoded private keys and certificates
	caCert := d.Spec.CACert
	registryPrKey := d.Spec.RegistryPrivateKey
	ncPrKey := d.Spec.NodeControllerPrivateKey
	registryCert := d.Spec.RegistryCert
	ncCert := d.Spec.NodeControllerCert

	// sanity check
	if caCert == nil {
		if registryCert != nil || ncCert != nil {
			return nil, fmt.Errorf("incomplete deployment configuration: missing root CA certificate by which the provided certificates are signed")
		}
	} else if registryCert == nil || registryPrKey == nil || ncCert == nil || ncPrKey == nil {
		return nil, fmt.Errorf("incomplete deployment configuration: certificates and corresponding private keys must be provided")
	}

	if caCert == nil {
		var prKey *rsa.PrivateKey

		ca, err := pmemtls.NewCA(nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize CA: %v", err)
		}
		caCert = ca.EncodedCertificate()

		if registryPrKey != nil {
			prKey, err = pmemtls.DecodeKey(registryPrKey)
		} else {
			prKey, err = pmemtls.NewPrivateKey()
			registryPrKey = pmemtls.EncodeKey(prKey)
		}
		if err != nil {
			return nil, err
		}

		cert, err := ca.GenerateCertificate("pmem-registry", prKey.Public())
		if err != nil {
			return nil, fmt.Errorf("failed to generate registry certificate: %v", err)
		}
		registryCert = pmemtls.EncodeCert(cert)

		if ncPrKey == nil {
			prKey, err = pmemtls.NewPrivateKey()
			ncPrKey = pmemtls.EncodeKey(prKey)
		} else {
			prKey, err = pmemtls.DecodeKey(ncPrKey)
		}
		if err != nil {
			return nil, err
		}

		cert, err = ca.GenerateCertificate("pmem-node-controller", prKey.Public())
		if err != nil {
			return nil, err
		}
		ncCert = pmemtls.EncodeCert(cert)
	} else {
		// check if the provided certificates are valid
		if err := validateCertificates(caCert, registryPrKey, registryCert, ncPrKey, ncCert); err != nil {
			return nil, err
		}
	}

	return []runtime.Object{
		d.getSecret("pmem-ca", nil, caCert),
		d.getSecret("pmem-registry", registryPrKey, registryCert),
		d.getSecret("pmem-node-controller", ncPrKey, ncCert),
	}, nil
}

// validateCertificates ensures that the given keys and certificates are valid
// to start PMEM-CSI driver by running a mutual-tls registry server and initiating
// a tls client connection to that sever using the provided keys and certificates.
// As we use mutual-tls, testing one server is enough to make sure that the provided
// certificates works
func validateCertificates(caCert, regKey, regCert, ncKey, ncCert []byte) error {
	const endpoint = "0.0.0.0:10000"

	// Registry server config
	regCfg, err := pmemgrpc.ServerTLS(caCert, regCert, regKey, "pmem-node-controller")
	if err != nil {
		return err
	}

	clientCfg, err := pmemgrpc.ClientTLS(caCert, ncCert, ncKey, "pmem-registry")
	if err != nil {
		return err
	}

	// start a registry server
	server := grpcserver.NewNonBlockingGRPCServer()
	if err := server.Start("tcp://"+endpoint, regCfg); err != nil {
		return err
	}
	defer server.ForceStop()

	conn, err := tls.Dial("tcp", endpoint, clientCfg)
	if err != nil {
		return err
	}

	conn.Close()

	return nil
}

func (d *PmemCSIDriver) getOwnerReference() metav1.OwnerReference {
	blockOwnerDeletion := true
	isController := true
	return metav1.OwnerReference{
		APIVersion:         d.APIVersion,
		Kind:               d.Kind,
		Name:               d.GetName(),
		UID:                d.GetUID(),
		BlockOwnerDeletion: &blockOwnerDeletion,
		Controller:         &isController,
	}
}

func (d *PmemCSIDriver) getCSIDriver(k8sVersion version.Version) *storagev1beta1.CSIDriver {
	attachRequired := false
	podInfoOnMount := true

	csiDriver := &storagev1beta1.CSIDriver{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CSIDriver",
			APIVersion: "v1beta1",
		},
		ObjectMeta: d.getObjectMeta(d.Spec.DriverName, true),
		Spec: storagev1beta1.CSIDriverSpec{
			AttachRequired: &attachRequired,
			PodInfoOnMount: &podInfoOnMount,
		},
	}

	// Volume lifecycle modes are supported only after k8s v1.16
	if k8sVersion.Compare(1, 16) >= 0 {
		csiDriver.Spec.VolumeLifecycleModes = []storagev1beta1.VolumeLifecycleMode{
			storagev1beta1.VolumeLifecyclePersistent,
			storagev1beta1.VolumeLifecycleEphemeral,
		}
	}

	return csiDriver
}

func (d *PmemCSIDriver) getSecret(cn string, ecodedKey []byte, ecnodedCert []byte) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name+"-"+cn, false),
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSPrivateKeyKey: ecodedKey,
			corev1.TLSCertKey:       ecnodedCert,
		},
	}
}

func (d *PmemCSIDriver) getControllerService() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name+"-controller", false),
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Port: controllerServicePort,
				},
			},
			Selector: map[string]string{
				"app": d.Name + "-controller",
			},
		},
	}
}

func (d *PmemCSIDriver) getMetricsService() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name+"-metrics", false),
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Port: controllerMetricsPort,
				},
			},
			Selector: map[string]string{
				"app": d.Name + "-controller",
			},
		},
	}
}

func (d *PmemCSIDriver) getControllerServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name, false),
	}
}

func (d *PmemCSIDriver) getControllerProvisionerRole() *rbacv1.Role {
	return &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name, false),
		Rules: []rbacv1.PolicyRule{
			rbacv1.PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"endpoints"},
				Verbs: []string{
					"get", "watch", "list", "delete", "update", "create",
				},
			},
			rbacv1.PolicyRule{
				APIGroups: []string{"coordination.k8s.io"},
				Resources: []string{"leases"},
				Verbs: []string{
					"get", "watch", "list", "delete", "update", "create",
				},
			},
		},
	}
}

func (d *PmemCSIDriver) getControllerProvisionerRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name, false),
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      d.Name,
				Namespace: d.namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     d.Name,
		},
	}
}

func (d *PmemCSIDriver) getControllerProvisionerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name, true),
		Rules: []rbacv1.PolicyRule{
			rbacv1.PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumes"},
				Verbs: []string{
					"get", "watch", "list", "delete", "create",
				},
			},
			rbacv1.PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims"},
				Verbs: []string{
					"get", "watch", "list", "update",
				},
			},
			rbacv1.PolicyRule{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"storageclasses"},
				Verbs: []string{
					"get", "watch", "list",
				},
			},
			rbacv1.PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs: []string{
					"watch", "list", "create", "update", "patch",
				},
			},
			rbacv1.PolicyRule{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshots", "volumesnapshotcontents"},
				Verbs: []string{
					"get", "list",
				},
			},
			rbacv1.PolicyRule{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"csinodes"},
				Verbs: []string{
					"get", "list", "watch",
				},
			},
			rbacv1.PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"nodes"},
				Verbs: []string{
					"get", "list", "watch",
				},
			},
		},
	}
}

func (d *PmemCSIDriver) getControllerProvisionerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name, true),
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      d.Name,
				Namespace: d.namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     d.Name,
		},
	}
}

func (d *PmemCSIDriver) getControllerStatefulSet() *appsv1.StatefulSet {
	replicas := int32(1)
	ss := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name+"-controller", false),
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": d.Name + "-controller",
				},
			},
			ServiceName: d.Name + "-controller",
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: joinMaps(
						d.Spec.Labels,
						map[string]string{
							"app":                        d.Name + "-controller",
							"pmem-csi.intel.com/webhook": "ignore",
						}),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: d.Name,
					Containers: []corev1.Container{
						d.getControllerContainer(),
						d.getProvisionerContainer(),
					},
					Volumes: []corev1.Volume{
						{
							Name: "plugin-socket-dir",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "registry-cert",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: d.Name + "-pmem-registry",
									Items: []corev1.KeyToPath{
										{
											Key:  "tls.crt",
											Path: "pmem-csi-registry.crt",
										},
										{
											Key:  "tls.key",
											Path: "pmem-csi-registry.key",
										},
									},
								},
							},
						},
						{
							Name: "ca-cert",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: d.Name + "-pmem-ca",
									Items: []corev1.KeyToPath{
										{
											Key:  "tls.crt",
											Path: "ca.crt",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return ss
}

func (d *PmemCSIDriver) getNodeDaemonSet() *appsv1.DaemonSet {
	directoryOrCreate := corev1.HostPathDirectoryOrCreate
	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: d.getObjectMeta(d.Name+"-node", false),
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": d.Name + "-node",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: joinMaps(
						d.Spec.Labels,
						map[string]string{
							"app":                        d.Name + "-node",
							"pmem-csi.intel.com/webhook": "ignore",
						}),
				},
				Spec: corev1.PodSpec{
					NodeSelector: d.Spec.NodeSelector,
					Containers: []corev1.Container{
						d.getNodeDriverContainer(),
						d.getNodeRegistrarContainer(),
					},
					Volumes: []corev1.Volume{
						{
							Name: "registration-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/kubelet/plugins_registry/",
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "mountpoint-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/kubelet/plugins/kubernetes.io/csi",
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "pods-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/kubelet/pods",
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "pmem-state-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/" + d.Spec.DriverName,
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "sys-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/sys",
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "dev-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/dev",
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "controller-cert",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: d.Name + "-pmem-node-controller",
									Items: []corev1.KeyToPath{
										{
											Key:  "tls.crt",
											Path: "pmem-csi-node-controller.crt",
										},
										{
											Key:  "tls.key",
											Path: "pmem-csi-node-controller.key",
										},
									},
								},
							},
						},
						{
							Name: "ca-cert",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: d.Name + "-pmem-ca",
									Items: []corev1.KeyToPath{
										{
											Key:  "tls.crt",
											Path: "ca.crt",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if d.Spec.DeviceMode == "lvm" {
		ds.Spec.Template.Spec.InitContainers = []corev1.Container{
			d.getNamespaceInitContainer(),
			d.getVolumeGroupInitContainer(),
		}
	}

	return ds
}

func (d *PmemCSIDriver) getControllerArgs() []string {
	args := []string{
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-mode=controller",
		"-drivername=" + d.Spec.DriverName,
		"-endpoint=unix:///csi/csi-controller.sock",
		fmt.Sprintf("-registryEndpoint=tcp://0.0.0.0:%d", controllerServicePort),
		fmt.Sprintf("-metricsListen=:%d", controllerMetricsPort),
		"-nodeid=$(KUBE_NODE_NAME)",
		"-caFile=/ca-certs/ca.crt",
		"-certFile=/certs/pmem-csi-registry.crt",
		"-keyFile=/certs/pmem-csi-registry.key",
	}

	return args
}

func (d *PmemCSIDriver) getNodeDriverArgs() []string {
	args := []string{
		fmt.Sprintf("-deviceManager=%s", d.Spec.DeviceMode),
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-drivername=" + d.Spec.DriverName,
		"-mode=node",
		"-endpoint=unix:///var/lib/" + d.Spec.DriverName + "/csi.sock",
		"-nodeid=$(KUBE_NODE_NAME)",
		fmt.Sprintf("-controllerEndpoint=tcp://$(KUBE_POD_IP):%d", nodeControllerPort),
		// User controller service name(== deployment name) as registry endpoint.
		fmt.Sprintf("-registryEndpoint=tcp://%s-controller:%d", d.Name, controllerServicePort),
		"-statePath=/var/lib/" + d.Spec.DriverName,
		"-caFile=/ca-certs/ca.crt",
		"-certFile=/certs/pmem-csi-node-controller.crt",
		"-keyFile=/certs/pmem-csi-node-controller.key",
	}

	return args
}

func (d *PmemCSIDriver) getControllerContainer() corev1.Container {
	return corev1.Container{
		Name:            "pmem-driver",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command:         []string{"/usr/local/bin/pmem-csi-driver"},
		Args:            d.getControllerArgs(),
		Env: []corev1.EnvVar{
			{
				Name: "KUBE_NODE_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "spec.nodeName",
					},
				},
			},
			{
				Name:  "TERMINATION_LOG_PATH",
				Value: "/tmp/termination-log",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "registry-cert",
				MountPath: "/certs",
			},
			{
				Name:      "ca-cert",
				MountPath: "/ca-certs",
			},
			{
				Name:      "plugin-socket-dir",
				MountPath: "/csi",
			},
		},
		Resources:              *d.Spec.ControllerResources,
		TerminationMessagePath: "/tmp/termination-log",
	}
}

func (d *PmemCSIDriver) getNodeDriverContainer() corev1.Container {
	bidirectional := corev1.MountPropagationBidirectional
	true := true
	c := corev1.Container{
		Name:            "pmem-driver",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command:         []string{"/usr/local/bin/pmem-csi-driver"},
		Args:            d.getNodeDriverArgs(),
		Env: []corev1.EnvVar{
			{
				Name: "KUBE_NODE_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "spec.nodeName",
					},
				},
			},
			{
				Name: "KUBE_POD_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "status.podIP",
					},
				},
			},
			{
				Name:  "TERMINATION_LOG_PATH",
				Value: "/tmp/termination-log",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:             "mountpoint-dir",
				MountPath:        "/var/lib/kubelet/plugins/kubernetes.io/csi",
				MountPropagation: &bidirectional,
			},
			{
				Name:             "pods-dir",
				MountPath:        "/var/lib/kubelet/pods",
				MountPropagation: &bidirectional,
			},
			{
				Name:      "controller-cert",
				MountPath: "/certs",
			},
			{
				Name:      "ca-cert",
				MountPath: "/ca-certs",
			},
			{
				Name:      "pmem-state-dir",
				MountPath: "/var/lib/" + d.Spec.DriverName,
			},
			{
				Name:      "dev-dir",
				MountPath: "/dev",
			},
		},
		Resources: *d.Spec.NodeResources,
		SecurityContext: &corev1.SecurityContext{
			Privileged: &true,
		},
		TerminationMessagePath: "/tmp/termination-log",
	}

	// Driver in 'direct' mode requires /sys mounting
	if d.Spec.DeviceMode == api.DeviceModeDirect {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      "sys-dir",
			MountPath: "/sys",
		})
	}

	return c
}

func (d *PmemCSIDriver) getProvisionerContainer() corev1.Container {
	return corev1.Container{
		Name:            "provisioner",
		Image:           d.Spec.ProvisionerImage,
		ImagePullPolicy: d.Spec.PullPolicy,
		Args: []string{
			"--timeout=5m",
			fmt.Sprintf("--v=%d", d.Spec.LogLevel),
			"--csi-address=/csi/csi-controller.sock",
			"--feature-gates=Topology=true",
			"--strict-topology=true",
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "plugin-socket-dir",
				MountPath: "/csi",
			},
		},
		Resources: *d.Spec.ControllerResources,
	}
}

func (d *PmemCSIDriver) getNamespaceInitContainer() corev1.Container {
	true := true
	return corev1.Container{
		Name:            "pmem-ns-init",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command: []string{
			"/usr/local/bin/pmem-ns-init",
		},
		Args: []string{
			fmt.Sprintf("--v=%d", d.Spec.LogLevel),
			fmt.Sprintf("--useforfsdax=%d", d.Spec.PMEMPercentage),
		},
		Env: []corev1.EnvVar{
			{
				Name:  "TERMINATION_LOG_PATH",
				Value: "/tmp/pmem-ns-init-termination-log",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "sys-dir",
				MountPath: "/sys",
			},
		},
		Resources: *d.Spec.NodeResources,
		SecurityContext: &corev1.SecurityContext{
			Privileged: &true,
		},
		TerminationMessagePath: "/tmp/pmem-ns-init-termination-log",
	}
}

func (d *PmemCSIDriver) getVolumeGroupInitContainer() corev1.Container {
	true := true
	return corev1.Container{
		Name:            "pmem-vgm",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command: []string{
			"/usr/local/bin/pmem-vgm",
		},
		Args: []string{
			fmt.Sprintf("--v=%d", d.Spec.LogLevel),
		},
		Env: []corev1.EnvVar{
			{
				Name:  "TERMINATION_LOG_PATH",
				Value: "/tmp/pmem-vgm-termination-log",
			},
		},
		Resources: *d.Spec.NodeResources,
		SecurityContext: &corev1.SecurityContext{
			Privileged: &true,
		},
		TerminationMessagePath: "/tmp/pmem-vgm-termination-log",
	}
}

func (d *PmemCSIDriver) getNodeRegistrarContainer() corev1.Container {
	return corev1.Container{
		Name:            "driver-registrar",
		Image:           d.Spec.NodeRegistrarImage,
		ImagePullPolicy: d.Spec.PullPolicy,
		Args: []string{
			fmt.Sprintf("--v=%d", d.Spec.LogLevel),
			fmt.Sprintf("--kubelet-registration-path=/var/lib/%s/csi.sock", d.Spec.DriverName),
			"--csi-address=/pmem-csi/csi.sock",
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "pmem-state-dir",
				MountPath: "/pmem-csi",
			},
			{
				Name:      "registration-dir",
				MountPath: "/registration",
			},
		},
		Resources: *d.Spec.NodeResources,
	}
}

func (d *PmemCSIDriver) getObjectMeta(name string, isClusterResource bool) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{
		Name: name,
		OwnerReferences: []metav1.OwnerReference{
			d.getOwnerReference(),
		},
		Labels: d.Spec.Labels,
	}
	if !isClusterResource {
		meta.Namespace = d.namespace
	}
	return meta
}

func joinMaps(left, right map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}

// changesFromPreDeployed checks if any conflicting changes observed
// in deployment compared to pre-deployed driver for that deployment if any.
func (d *PmemCSIDriver) changesFromPreDeployed(r *ReconcileDeployment) (map[api.DeploymentChange]struct{}, error) {
	changes := map[api.DeploymentChange]struct{}{}

	ds := &appsv1.DaemonSet{
		ObjectMeta: d.getObjectMeta(d.Name+"-node", false),
	}
	if err := r.Get(ds); err != nil {
		if errors.IsNotFound(err) {
			return changes, nil
		}
		return nil, err
	}

	for _, c := range ds.Spec.Template.Spec.Containers {
		if c.Name == "pmem-driver" {
			args := argsToMap(c.Args)

			if args["-driverName"] != d.Spec.DriverName {
				changes[api.DriverName] = struct{}{}
			}
			if api.DeviceMode(args["-deviceManager"]) != d.Spec.DeviceMode {
				changes[api.DriverMode] = struct{}{}
			}
			break
		}
	}

	for _, c := range ds.Spec.Template.Spec.InitContainers {
		if c.Name == "pmem-ns-init" {
			args := argsToMap(c.Args)
			if fsdaxStr, ok := args["--useforfsdax"]; ok {
				useOfFsdax, err := strconv.ParseUint(fsdaxStr, 10, 16)
				if err != nil {
					klog.Warningf("failed to parse use-of-fsdax(%s) argument: %v", fsdaxStr, err)
				} else if d.Spec.PMEMPercentage != uint16(useOfFsdax) {
					changes[api.PMEMPercentage] = struct{}{}
				}
			}
			break
		}
	}

	return changes, nil
}

func argsToMap(args []string) map[string]string {
	argsMap := map[string]string{}
	for _, arg := range args {
		vals := strings.SplitN(arg, "=", 2)
		argsMap[vals[0]] = vals[1]
	}
	return argsMap
}
