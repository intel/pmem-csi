/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"crypto/rsa"
	"fmt"
	"strings"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/utils"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

const (
	controllerServicePort = 10000
	nodeControllerPort    = 10001
)

type PmemCSIDriver struct {
	*api.Deployment
	// operators namespace used for creating sub-resources
	namespace string
}

// checkIfNameClash check if d.Spec.DriverName is not clashing with any of the
// known deploments by the reconciler. If clash found reutrns the reference of
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
	oldDeployment, ok := r.deployments[d.Name]
	if ok {
		changes = d.Compare(oldDeployment)
		// We should not trust the deployment status, as it might be tampered.
		// Use existing objects Status instead.
		oldDeployment.Status.DeepCopyInto(&d.Status)
	}

	klog.Infof("Deployment: %q, state: %q", d.Name, d.Status.Phase)

	switch d.Status.Phase {
	// We treat same both new and failed deployments
	case api.DeploymentPhaseNew, api.DeploymentPhaseFailed:
		if dep := d.checkIfNameClash(r); dep != nil {
			d.Status.Phase = api.DeploymentPhaseFailed
			return true, fmt.Errorf("driver name %q is already taken by deployment %q",
				d.Spec.DriverName, d.Name)
		}
		r.deployments[d.Name] = d.Deployment

		if err := d.initDeploymentSecrests(r); err != nil {
			d.Status.Phase = api.DeploymentPhaseFailed
			return true, err
		}
		d.Status.Phase = api.DeploymentPhaseInitializing
	case api.DeploymentPhaseInitializing:
		// There might be change in deployment name
		// Reject if any incompatible name change
		if _, ok := changes[api.DriverName]; ok {
			if dep := d.checkIfNameClash(r); dep != nil {
				// Revert Deployment object
				d.Spec.DriverName = oldDeployment.Spec.DriverName
				if err := r.Update(d.Deployment); err != nil {
					return true, err
				}
				return true, fmt.Errorf("driver name %q is already taken by deployment %q",
					d.Spec.DriverName, d.Name)
			}
		}

		// Update local cache so that we can catch if any change in deployment
		r.deployments[d.Name] = d.Deployment

		if err := d.deployObjects(r); err != nil {
			return true, err
		}

		d.Status.Phase = api.DeploymentPhaseRunning
		// Deployment successfull, so no more reconcile needed for this deployment
		return false, nil
	case api.DeploymentPhaseRunning:
		requeue, err := d.reconcileDeploymentChanges(r, oldDeployment, changes)
		if err == nil {
			// If everything ok, update local cache
			r.deployments[d.Name] = d.Deployment
		}
		return requeue, err
	}
	return true, nil
}

func (d *PmemCSIDriver) reconcileDeploymentChanges(r *ReconcileDeployment, existing *api.Deployment, changes map[api.DeploymentChange]struct{}) (bool, error) {
	if len(changes) == 0 {
		klog.Infof("No changes detected in deployment")
		return false, nil
	}

	klog.Infof("Changes detected: %v", changes)

	updateController := false
	updateNodeDriver := false
	updateDeployment := false
	requeue := false
	var err error

	for c := range changes {
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
		}
	}

	// Reject changes which cannot be applied
	// by updating the deployment object
	if updateDeployment {
		klog.Infof("Updating deployment %q", d.Name)
		if e := r.Update(d.Deployment); e != nil {
			requeue = true
			err = e
		}
	}

	if updateController {
		klog.Infof("Updating controller driver for deployment %q", d.Name)
		if e := r.Update(d.getControllerStatefulSet()); e != nil {
			requeue = true
			err = e
		}
	}
	if updateNodeDriver {
		klog.Infof("Updating node driver for deployment %q", d.Name)
		if e := r.Update(d.getNodeDaemonSet()); err != nil {
			requeue = true
			err = e
		}
	}

	return requeue, err
}

func (d *PmemCSIDriver) initDeploymentSecrests(r *ReconcileDeployment) error {

	// Encoded private keys and certificates
	caCert := d.Spec.CACert
	registryPrKey := d.Spec.RegistryPrivateKey
	ncPrKey := d.Spec.NodeControllerPrivateKey
	registryCert := d.Spec.RegistryCert
	ncCert := d.Spec.NodeControllerCert

	// sanity check
	if caCert == nil {
		if registryCert != nil || ncCert != nil {
			return fmt.Errorf("incomplete deployment configuration: missing root CA certificate by which the provided certificates are signed")
		}
	} else if registryCert == nil || registryPrKey == nil || ncCert == nil || ncPrKey == nil {
		return fmt.Errorf("incomplete deployment configuration: certificates and corresponding private keys must be provided")
	}

	if caCert == nil {
		var prKey *rsa.PrivateKey

		ca, err := utils.NewCA(nil, nil)
		if err != nil {
			return fmt.Errorf("failed to initialize CA: %v", err)
		}
		caCert = ca.Certificate()

		if registryPrKey != nil {
			prKey, err = utils.DecodeKey(registryPrKey)
		} else {
			prKey, err = utils.NewPrivateKey()
			registryPrKey = utils.EncodeKey(prKey)
		}
		if err != nil {
			return err
		}

		cert, err := ca.GenerateCertificate("pmem-registry", prKey)
		if err != nil {
			return fmt.Errorf("failed to generate registry certificate: %v", err)
		}
		registryCert = utils.EncodeCert(cert)

		if ncPrKey == nil {
			prKey, err = utils.NewPrivateKey()
			ncPrKey = utils.EncodeKey(prKey)
		} else {
			prKey, err = utils.DecodeKey(ncPrKey)
		}
		if err != nil {
			return err
		}

		cert, err = ca.GenerateCertificate("pmem-node-controller", prKey)
		if err != nil {
			return err
		}
		ncCert = utils.EncodeCert(cert)
	}

	secrets := []runtime.Object{
		d.getSecret("pmem-ca", nil, caCert),
		d.getSecret("pmem-registry", registryPrKey, registryCert),
		d.getSecret("pmem-node-controller", ncPrKey, ncCert),
	}

	for _, obj := range secrets {
		if err := r.Create(obj); err != nil {
			return err
		}
	}

	return nil
}

func (d *PmemCSIDriver) deployObjects(r *ReconcileDeployment) error {
	for _, obj := range d.getDeploymentObjects() {
		if err := r.Create(obj); err != nil {
			return err
		}
	}
	return nil
}

func (d *PmemCSIDriver) getDeploymentObjects() []runtime.Object {
	return []runtime.Object{
		d.getControllerServiceAccount(),
		d.getControllerProvisionerRole(),
		d.getControllerProvisionerRoleBinding(),
		d.getControllerProvisionerClusterRole(),
		d.getControllerProvisionerClusterRoleBinding(),
		d.getControllerService(),
		d.getControllerStatefulSet(),
		d.getNodeDaemonSet(),
	}
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

func (d *PmemCSIDriver) getSecret(cn string, ecodedKey []byte, ecnodedCert []byte) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name + "-" + cn,
			Namespace: d.namespace,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
		Type: corev1.SecretTypeTLS,
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name,
			Namespace: d.namespace,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Port: controllerServicePort,
				},
			},
			Selector: map[string]string{
				"app": "pmem-csi-controller",
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name,
			Namespace: d.namespace,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
	}
}

func (d *PmemCSIDriver) getControllerProvisionerRole() *rbacv1.Role {
	return &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name,
			Namespace: d.namespace,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name,
			Namespace: d.namespace,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
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
		ObjectMeta: metav1.ObjectMeta{
			Name: d.Name,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
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
		ObjectMeta: metav1.ObjectMeta{
			Name: d.Name,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name + "-controller",
			Namespace: d.namespace,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "pmem-csi-controller",
				},
			},
			ServiceName: d.Name,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "pmem-csi-controller",
					},
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name + "-node",
			Namespace: d.namespace,
			OwnerReferences: []metav1.OwnerReference{
				d.getOwnerReference(),
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "pmem-csi-node",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "pmem-csi-node",
					},
				},
				Spec: corev1.PodSpec{
					NodeSelector: d.Spec.NodeSelector,
					HostNetwork:  true,
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
		"-nodeid=$(KUBE_NODE_NAME)",
		"-caFile=/ca-certs/ca.crt",
		"-certFile=/certs/pmem-csi-registry.crt",
		"-keyFile=/certs/pmem-csi-registry.key",
	}

	return args
}

func (d *PmemCSIDriver) getNodeDriverArgs() []string {
	// Form service port environment variable from Service name
	// In our case Service name is deployment name
	// Ref :- k8s.io/kubernetes/pkg/kubelet/envvars/envvars.go
	pmemServiceEndpointEnv := fmt.Sprintf(strings.ToUpper(strings.Replace(d.Name, "-", "_", -1))+"_PORT_%d_TCP", controllerServicePort)
	args := []string{
		fmt.Sprintf("-deviceManager=%s", d.Spec.DeviceMode),
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-drivername=" + d.Spec.DriverName,
		"-mode=node",
		"-endpoint=unix:///var/lib/" + d.Spec.DriverName + "/csi.sock",
		"-nodeid=$(KUBE_NODE_NAME)",
		fmt.Sprintf("-controllerEndpoint=tcp://$(KUBE_POD_IP):%d", nodeControllerPort),
		fmt.Sprintf("-registryEndpoint=" + "$(" + pmemServiceEndpointEnv + ")"),
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
		Resources: *d.Spec.ControllerResources,
	}
}

func (d *PmemCSIDriver) getNodeDriverContainer() corev1.Container {
	bidirectional := corev1.MountPropagationBidirectional
	true := true
	return corev1.Container{
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
	}
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
