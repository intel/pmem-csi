/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"crypto/rsa"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"runtime"

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
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
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

	k8sVersion version.Version
}

// Reconcile reconciles the driver deployment
func (d *PmemCSIDriver) Reconcile(r *ReconcileDeployment) (bool, error) {
	changes := map[api.DeploymentChange]struct{}{}

	oldDeployment, foundInCache := r.deployments[d.Name]
	if !foundInCache {
		/* New deployment */
		r.evRecorder.Event(d, corev1.EventTypeNormal, api.EventReasonNew, "Processing new driver deployment")
	}

	if err := d.EnsureDefaults(r.containerImage); err != nil {
		d.Deployment.Status.Phase = api.DeploymentPhaseFailed
		r.evRecorder.Event(d, corev1.EventTypeWarning, api.EventReasonFailed, err.Error())
		return true, err
	}

	if foundInCache {
		changes = d.Compare(oldDeployment)
	}

	klog.Infof("Deployment: %q, state %q, changes %v, in cache %v", d.Name, d.Status.Phase, changes, foundInCache)

	requeue, err := d.reconcileDeploymentChanges(r, changes, foundInCache)
	if err == nil {
		// If everything ok, update local cache
		r.deployments[d.Name] = d.Deployment

		// TODO: wait for functional driver before entering "running" phase.
		// For now we go straight to it.
		d.Status.Phase = api.DeploymentPhaseRunning
		r.evRecorder.Event(d, corev1.EventTypeNormal, api.EventReasonRunning, "Driver deployment successful")
	} else if d.Status.Phase == api.DeploymentPhaseNew {
		d.Status.Phase = api.DeploymentPhaseFailed
		r.evRecorder.Event(d, corev1.EventTypeWarning, api.EventReasonFailed, err.Error())
	}

	return requeue, err
}

// reconcileDeploymentChanges examines the changes and updates the appropriate objects.
// In case of foundInCache is false, all the objects gets refreshed/updated.
func (d *PmemCSIDriver) reconcileDeploymentChanges(r *ReconcileDeployment, changes map[api.DeploymentChange]struct{}, foundInCache bool) (bool, error) {
	updateController := false
	updateNodeDriver := false
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
		case api.DriverMode:
			updateNodeDriver = true
		case api.NodeSelector:
			updateNodeDriver = true
		case api.PMEMPercentage:
			updateNodeDriver = true
		case api.Labels:
			updateAll = true
		case api.CACertificate, api.RegistryCertificate, api.NodeControllerCertificate:
			updateSecrets = true
		case api.KubeletDir:
			updateNodeDriver = true
		}

		if err != nil {
			klog.Warningf("Incompatible change of deployment occurred: %v", err)
		}
	}

	objects := []apiruntime.Object{}

	// Force update all deployment objects
	if updateAll {
		klog.Infof("Updating all objects for deployment %q", d.Name)
		objs, err := d.getDeploymentObjects()
		if err != nil {
			return true, err
		}
		objects = append(objects, objs...)
	} else {
		if updateSecrets {
			objs, err := d.getSecrets()
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
			if len(s.Labels) > 0 {
				if existingService.Labels == nil {
					existingService.Labels = map[string]string{}
				}
				for key, value := range s.Labels {
					existingService.Labels[key] = value
				}
			}
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
	objects, err := d.getDeploymentObjects()
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
func (d *PmemCSIDriver) getDeploymentObjects() ([]apiruntime.Object, error) {
	objects, err := d.getSecrets()
	if err != nil {
		return nil, err
	}

	objects = append(objects,
		d.getCSIDriver(),
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

func (d *PmemCSIDriver) getSecrets() ([]apiruntime.Object, error) {
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
			return nil, fmt.Errorf("validate CA certificates: %v", err)
		}
	}

	// Instead of waiting for next GC cycle, initiate garbage collector manually
	// so that the unneeded CA key, certificate get removed.
	defer runtime.GC()

	return []apiruntime.Object{
		d.getSecret("registry-secrets", caCert, registryPrKey, registryCert),
		d.getSecret("node-secrets", caCert, ncPrKey, ncCert),
	}, nil
}

// validateCertificates ensures that the given keys and certificates are valid
// to start PMEM-CSI driver by running a mutual-tls registry server and initiating
// a tls client connection to that sever using the provided keys and certificates.
// As we use mutual-tls, testing one server is enough to make sure that the provided
// certificates works
func validateCertificates(caCert, regKey, regCert, ncKey, ncCert []byte) error {
	tmp, err := ioutil.TempDir("", "pmem-csi-validate-certs-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

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
	path := path.Join(tmp, "socket")
	if err := server.Start("unix://"+path, regCfg); err != nil {
		return err
	}
	defer server.ForceStop()

	conn, err := tls.Dial("unix", path, clientCfg)
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

func (d *PmemCSIDriver) getCSIDriver() *storagev1beta1.CSIDriver {
	attachRequired := false
	podInfoOnMount := true

	csiDriver := &storagev1beta1.CSIDriver{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CSIDriver",
			APIVersion: "storage.k8s.io/v1beta1",
		},
		ObjectMeta: d.getObjectMeta(d.GetName(), true),
		Spec: storagev1beta1.CSIDriverSpec{
			AttachRequired: &attachRequired,
			PodInfoOnMount: &podInfoOnMount,
		},
	}

	// Volume lifecycle modes are supported only after k8s v1.16
	if d.k8sVersion.Compare(1, 16) >= 0 {
		csiDriver.Spec.VolumeLifecycleModes = []storagev1beta1.VolumeLifecycleMode{
			storagev1beta1.VolumeLifecyclePersistent,
			storagev1beta1.VolumeLifecycleEphemeral,
		}
	}

	return csiDriver
}

func (d *PmemCSIDriver) getSecret(cn string, ca, encodedKey, encodedCert []byte) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-"+cn, false),
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			// Same names as in the example secrets and in the v1 API.
			"ca.crt":  ca,          // no standard name for this one
			"tls.key": encodedKey,  // v1.TLSPrivateKeyKey
			"tls.crt": encodedCert, // v1.TLSCertKey
		},
	}
}

func (d *PmemCSIDriver) getControllerService() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-controller", false),
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Port: controllerServicePort,
					TargetPort: intstr.IntOrString{
						IntVal: controllerServicePort,
					},
				},
			},
			Selector: map[string]string{
				"app": d.GetHyphenedName() + "-controller",
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
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-metrics", false),
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Port: controllerMetricsPort,
					TargetPort: intstr.IntOrString{
						IntVal: controllerMetricsPort,
					},
				},
			},
			Selector: map[string]string{
				"app": d.GetHyphenedName() + "-controller",
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
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-controller", false),
	}
}

func (d *PmemCSIDriver) getControllerProvisionerRole() *rbacv1.Role {
	return &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-external-provisioner-cfg", false),
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
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-csi-provisioner-role-cfg", false),
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      d.GetHyphenedName() + "-controller",
				Namespace: d.namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     d.GetHyphenedName() + "-external-provisioner-cfg",
		},
	}
}

func (d *PmemCSIDriver) getControllerProvisionerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-external-provisioner-runner", true),
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
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-csi-provisioner-role", true),
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      d.GetHyphenedName() + "-controller",
				Namespace: d.namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     d.GetHyphenedName() + "-external-provisioner-runner",
		},
	}
}

func (d *PmemCSIDriver) getControllerStatefulSet() *appsv1.StatefulSet {
	replicas := int32(1)
	true := true
	pmemcsiUser := int64(1000)
	ss := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-controller", false),
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": d.GetHyphenedName() + "-controller",
				},
			},
			ServiceName: d.GetHyphenedName() + "-controller",
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: joinMaps(
						d.Spec.Labels,
						map[string]string{
							"app":                        d.GetHyphenedName() + "-controller",
							"pmem-csi.intel.com/webhook": "ignore",
						}),
				},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						// Controller pod must run as non-root user
						RunAsNonRoot: &true,
						RunAsUser:    &pmemcsiUser,
					},
					ServiceAccountName: d.GetHyphenedName() + "-controller",
					Containers: []corev1.Container{
						d.getControllerContainer(),
						d.getProvisionerContainer(),
					},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											// By default, the controller will run anywhere in the cluster.
											// If that isn't desired, the "pmem-csi.intel.com/controller" label
											// can be set to "no" or "false" for a node to prevent the controller
											// from running there.
											//
											// This is used during testing as a workaround for a particular issue
											// on Clear Linux where network configuration randomly fails such that
											// the driver which runs on the same node as the controller cannot
											// connect to the controller (https://github.com/intel/pmem-csi/issues/555).
											//
											// It may also be useful for other purposes, in particular for deployment
											// through the operator: it has the same rule and currently no other API for
											// setting affinity.
											{
												Key:      "pmem-csi.intel.com/controller",
												Operator: corev1.NodeSelectorOpNotIn,
												Values:   []string{"no", "false"},
											},
										},
									},
								},
							},
						},
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
									SecretName: d.GetHyphenedName() + "-registry-secrets",
								},
							},
						},
						{
							Name: "tmp-dir",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
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
		ObjectMeta: d.getObjectMeta(d.GetHyphenedName()+"-node", false),
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": d.GetHyphenedName() + "-node",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: joinMaps(
						d.Spec.Labels,
						map[string]string{
							"app":                        d.GetHyphenedName() + "-node",
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
							Name: "socket-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: d.Spec.KubeletDir + "/plugins/" + d.GetName(),
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "registration-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: d.Spec.KubeletDir + "/plugins_registry/",
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "mountpoint-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: d.Spec.KubeletDir + "/plugins/kubernetes.io/csi",
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "pods-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: d.Spec.KubeletDir + "/pods",
									Type: &directoryOrCreate,
								},
							},
						},
						{
							Name: "node-cert",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: d.GetHyphenedName() + "-node-secrets",
								},
							},
						},
						{
							Name: "pmem-state-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/" + d.GetName(),
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
							Name: "sys-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/sys",
									Type: &directoryOrCreate,
								},
							},
						},
					},
				},
			},
		},
	}

	return ds
}

func (d *PmemCSIDriver) getControllerCommand() []string {
	return []string{
		"/usr/local/bin/pmem-csi-driver",
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-mode=controller",
		"-endpoint=unix:///csi/csi-controller.sock",
		fmt.Sprintf("-registryEndpoint=tcp://0.0.0.0:%d", controllerServicePort),
		fmt.Sprintf("-metricsListen=:%d", controllerMetricsPort),
		"-nodeid=$(KUBE_NODE_NAME)",
		"-caFile=/certs/ca.crt",
		"-certFile=/certs/tls.crt",
		"-keyFile=/certs/tls.key",
		"-drivername=$(PMEM_CSI_DRIVER_NAME)",
	}
}

func (d *PmemCSIDriver) getNodeDriverCommand() []string {
	return []string{
		"/usr/local/bin/pmem-csi-driver",
		fmt.Sprintf("-deviceManager=%s", d.Spec.DeviceMode),
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-mode=node",
		"-endpoint=unix:///csi/csi.sock",
		"-nodeid=$(KUBE_NODE_NAME)",
		fmt.Sprintf("-controllerEndpoint=tcp://$(KUBE_POD_IP):%d", nodeControllerPort),
		// User controller service name(== deployment name) as registry endpoint.
		fmt.Sprintf("-registryEndpoint=tcp://%s-controller:%d", d.GetHyphenedName(), controllerServicePort),
		"-caFile=/certs/ca.crt",
		"-certFile=/certs/tls.crt",
		"-keyFile=/certs/tls.key",
		"-statePath=/var/lib/$(PMEM_CSI_DRIVER_NAME)",
		"-drivername=$(PMEM_CSI_DRIVER_NAME)",
		fmt.Sprintf("-pmemPercentage=%d", d.Spec.PMEMPercentage),
	}
}

func (d *PmemCSIDriver) getControllerContainer() corev1.Container {
	true := true
	return corev1.Container{
		Name:            "pmem-driver",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command:         d.getControllerCommand(),
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
			{
				Name:  "PMEM_CSI_DRIVER_NAME",
				Value: d.GetName(),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "registry-cert",
				MountPath: "/certs",
			},
			{
				Name:      "plugin-socket-dir",
				MountPath: "/csi",
			},
			{
				Name:      "tmp-dir",
				MountPath: "/tmp",
			},
		},
		Resources:              *d.Spec.ControllerResources,
		TerminationMessagePath: "/tmp/termination-log",
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
	}
}

func (d *PmemCSIDriver) getNodeDriverContainer() corev1.Container {
	bidirectional := corev1.MountPropagationBidirectional
	true := true
	root := int64(0)
	c := corev1.Container{
		Name:            "pmem-driver",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command:         d.getNodeDriverCommand(),
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
				Name:  "PMEM_CSI_DRIVER_NAME",
				Value: d.GetName(),
			},
			{
				Name:  "TERMINATION_LOG_PATH",
				Value: "/tmp/termination-log",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:             "mountpoint-dir",
				MountPath:        d.Spec.KubeletDir + "/plugins/kubernetes.io/csi",
				MountPropagation: &bidirectional,
			},
			{
				Name:             "pods-dir",
				MountPath:        d.Spec.KubeletDir + "/pods",
				MountPropagation: &bidirectional,
			},
			{
				Name:      "node-cert",
				MountPath: "/certs",
			},
			{
				Name:      "dev-dir",
				MountPath: "/dev",
			},
			{
				Name:      "sys-dir",
				MountPath: "/sys",
			},
			{
				Name:      "socket-dir",
				MountPath: "/csi",
			},
			{
				Name:             "pmem-state-dir",
				MountPath:        "/var/lib/" + d.GetName(),
				MountPropagation: &bidirectional,
			},
		},
		Resources: *d.Spec.NodeResources,
		SecurityContext: &corev1.SecurityContext{
			Privileged: &true,
			// Node driver must run as root user
			RunAsUser: &root,
		},
		TerminationMessagePath: "/tmp/termination-log",
	}

	return c
}

func (d *PmemCSIDriver) getProvisionerContainer() corev1.Container {
	true := true
	return corev1.Container{
		Name:            "external-provisioner",
		Image:           d.Spec.ProvisionerImage,
		ImagePullPolicy: d.Spec.PullPolicy,
		Args: []string{
			fmt.Sprintf("-v=%d", d.Spec.LogLevel),
			"--csi-address=/csi/csi-controller.sock",
			"--feature-gates=Topology=true",
			"--strict-topology=true",
			"--timeout=5m",
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "plugin-socket-dir",
				MountPath: "/csi",
			},
		},
		Resources: *d.Spec.ControllerResources,
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
	}
}

func (d *PmemCSIDriver) getNodeRegistrarContainer() corev1.Container {
	true := true
	return corev1.Container{
		Name:            "driver-registrar",
		Image:           d.Spec.NodeRegistrarImage,
		ImagePullPolicy: d.Spec.PullPolicy,
		Args: []string{
			fmt.Sprintf("-v=%d", d.Spec.LogLevel),
			"--kubelet-registration-path=" + d.Spec.KubeletDir + "/plugins/$(PMEM_CSI_DRIVER_NAME)/csi.sock",
			"--csi-address=/csi/csi.sock",
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "socket-dir",
				MountPath: "/csi",
			},
			{
				Name:      "registration-dir",
				MountPath: "/registration",
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "PMEM_CSI_DRIVER_NAME",
				Value: d.GetName(),
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
