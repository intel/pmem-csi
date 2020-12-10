/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/common/expfmt"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/skipper"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	pmemexec "github.com/intel/pmem-csi/pkg/exec"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
)

const (
	deploymentLabel = "pmem-csi.intel.com/deployment"
	SocatPort       = 9735
)

// InstallHook is the callback function for AddInstallHook.
type InstallHook func(Deployment *Deployment)

// UninstallHook is the callback function for AddUninstallHook.
type UninstallHook func(deploymentName string)

var (
	installHooks   []InstallHook
	uninstallHooks []UninstallHook

	// If WaitForPMEMDriver timed out once, then it is likely to
	// time out again, which just makes overall testing very slow,
	// in particular in the CI where usually no-one is monitoring
	// progress. Therefore we only allow it to fail once and then
	// skip all future tests.
	waitForPMEMDriverTimedOut bool
)

// AddInstallHook registers a callback which is invoked after a successful driver installation.
func AddInstallHook(h InstallHook) {
	installHooks = append(installHooks, h)
}

// AddUninstallHook registers a callback which is invoked before a driver removal.
func AddUninstallHook(h UninstallHook) {
	uninstallHooks = append(uninstallHooks, h)
}

// WaitForOperator ensures that the PMEM-CSI operator is ready for use, which is
// currently defined as the operator pod in Running phase.
func WaitForOperator(c *Cluster, namespace string) *v1.Pod {
	// TODO(avalluri): At later point of time we should add readiness support
	// for the operator. Then we can query directly the operator if its ready.
	// As intrem solution we are just checking Pod.Status.
	operator := c.WaitForAppInstance("pmem-csi-operator", "", namespace)
	ginkgo.By("Operator is ready!")
	return operator
}

// WaitForOLM watis till the "olm-operator" Pod in given namspace
// is ready else fails with exception.
func WaitForOLM(c *Cluster, namespace string) *v1.Pod {
	ginkgo.By("Waiting if the OLM deployment is ready...")
	olm := c.WaitForAppInstance("olm-operator", "", namespace)
	ginkgo.By("OLM is ready!")
	return olm
}

// WaitForPMEMDriver ensures that the PMEM-CSI driver is ready for use, which is
// defined as:
// - controller service is up and running
// - all nodes have registered
// - for testing deployments: TCP CSI endpoints are ready
//
// "name" is the common prefix used for objects of the deployment.
func WaitForPMEMDriver(c *Cluster, name string, d *Deployment) (metricsURL string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	info := time.NewTicker(time.Minute)
	defer info.Stop()
	deadline, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if waitForPMEMDriverTimedOut {
		// Abort early.
		skipper.Skipf("installing PMEM-CSI driver during previous test was too slow")
	}

	framework.Logf("Waiting for PMEM-CSI driver.")

	tlsConfig := tls.Config{
		// We could load ca.pem with pmemgrpc.LoadClientTLS, but as we are not connecting to it
		// via the service name, that would be enough.
		InsecureSkipVerify: true,
	}
	tr := http.Transport{
		TLSClientConfig: &tlsConfig,
	}
	defer tr.CloseIdleConnections()

	var lastError error
	var version string
	check := func() error {
		// Do not linger too long here, we rather want to
		// abort and print the error instead of getting stuck.
		const timeout = time.Second
		deadline, cancel := context.WithTimeout(deadline, timeout)
		defer cancel()

		// The controller service must be defined.
		port, err := c.GetServicePort(deadline, name+"-metrics", d.Namespace)
		if err != nil {
			return fmt.Errorf("get port for service %s-metrics in namespace %s: %v", name, d.Namespace, err)
		}

		// We can connect to it and get metrics data.
		scheme := "http"
		if d.Version == "0.7" {
			scheme = "https"
		}
		metricsURL = fmt.Sprintf("%s://%s:%d/metrics", scheme, c.NodeIP(0), port)
		client := &http.Client{
			Transport: &tr,
			Timeout:   timeout,
		}
		resp, err := client.Get(metricsURL)
		if err != nil {
			return fmt.Errorf("get controller metrics: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := ioutil.ReadAll(resp.Body)
			suffix := ""
			if len(body) > 0 {
				suffix = "\n" + string(body)
			}
			return fmt.Errorf("HTTP GET %s failed: %d%s", metricsURL, resp.StatusCode, suffix)
		}

		// Parse and check number of connected nodes. Dump the
		// version number while we are at it.
		parser := expfmt.TextParser{}
		metrics, err := parser.TextToMetricFamilies(resp.Body)
		if err != nil {
			return fmt.Errorf("parse metrics response: %v", err)
		}
		buildInfo, ok := metrics["build_info"]
		if !ok {
			return fmt.Errorf("expected build_info not found in metrics: %v", metrics)
		}
		if len(buildInfo.Metric) != 1 {
			return fmt.Errorf("expected build_info to have one metric, got: %v", buildInfo.Metric)
		}
		buildMetric := buildInfo.Metric[0]
		if len(buildMetric.Label) != 1 {
			return fmt.Errorf("expected build_info to have one label, got: %v", buildMetric.Label)
		}
		label := buildMetric.Label[0]
		if *label.Name != "version" {
			return fmt.Errorf("expected build_info to contain a version label, got: %s", *label.Name)
		}
		version = *label.Value

		pmemNodes, ok := metrics["pmem_nodes"]
		if !ok {
			return fmt.Errorf("expected pmem_nodes not found in metrics: %v", metrics)
		}

		if len(pmemNodes.Metric) != 1 {
			return fmt.Errorf("expected pmem_nodes to have one metric, got: %v", pmemNodes.Metric)
		}
		nodesMetric := pmemNodes.Metric[0]
		actualNodes := int(*nodesMetric.Gauge.Value)
		if actualNodes != c.NumNodes()-1 {
			return fmt.Errorf("only %d of %d nodes have registered", actualNodes, c.NumNodes()-1)
		}

		// Done for normal deployments.
		if !d.Testing {
			return nil
		}

		// For testing deployments, also ensure that the CSI endpoints can be reached.
		nodeAddress, controllerAddress, err := LookupCSIAddresses(c, d.Namespace)
		if err != nil {
			return fmt.Errorf("look up CSI addresses: %v", err)
		}
		tryConnect := func(address string) error {
			prefix := "dns:///" // triple slash is used by gRPC, which makes the address unparsable with net/url
			if !strings.HasPrefix(address, prefix) {
				return fmt.Errorf("unexpected non-DNS URL: %s", address)
			}
			conn, err := net.Dial("tcp", address[len(prefix):])
			if err != nil {
				return fmt.Errorf("dial %s: %v", address, err)
			}
			defer conn.Close()
			return nil
		}
		if err := tryConnect(controllerAddress); err != nil {
			return fmt.Errorf("connect to controller: %v", err)
		}
		if err := tryConnect(nodeAddress); err != nil {
			return fmt.Errorf("connect to node: %v", err)
		}

		return nil
	}
	ready := func() error {
		newError := check()
		if newError == nil {
			framework.Logf("Done with waiting, PMEM-CSI driver %s is ready.", version)
		}
		// Only overwrite the last error if we haven't reached the deadline yet, because
		// in that case the new error is probably just "context deadline exceeded".
		if lastError == nil || deadline.Err() == nil {
			lastError = newError
		}
		return lastError
	}

	if ready() == nil {
		return
	}
	for {
		select {
		case <-info.C:
			framework.Logf("Still waiting for PMEM-CSI driver, last error: %v", lastError)
		case <-deadline.Done():
			waitForPMEMDriverTimedOut = true
			framework.Failf("Giving up waiting for PMEM-CSI to start up, check the previous warnings and log output. Last error: %v", lastError)
		case <-ticker.C:
			if ready() == nil {
				return
			}
		}
	}
}

// https://github.com/containerd/containerd/issues/4068
var containerdTaskError = regexp.MustCompile(`failed to (start|create) containerd task`)

// CheckPMEMDriver does some sanity checks for a running deployment.
func CheckPMEMDriver(c *Cluster, deployment *Deployment) {
	pods, err := c.cs.CoreV1().Pods(deployment.Namespace).List(context.Background(),
		metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s in (%s)", deploymentLabel, deployment.Label()),
		},
	)
	framework.ExpectNoError(err, "list PMEM-CSI pods")
	gomega.Expect(len(pods.Items)).Should(gomega.BeNumerically(">", 0), "should have PMEM-CSI pods")
	for _, pod := range pods.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.RestartCount > 0 {
				print := framework.Failf
				if containerdTaskError.MatchString(fmt.Sprintf("%v", containerStatus.LastTerminationState)) {
					// This is a known issue in containerd, only document it.
					print = framework.Logf
				}
				print("container %q in pod %q restarted %d times, last state: %+v",
					containerStatus.Name,
					pod.Name,
					containerStatus.RestartCount,
					containerStatus.LastTerminationState,
				)
			}
		}
	}
}

// RemoveObjects deletes everything that might have been created for a
// PMEM-CSI driver or operator installation (pods, daemonsets,
// statefulsets, driver info, storage classes, etc.).
func RemoveObjects(c *Cluster, deployment *Deployment) error {
	// Try repeatedly, in case that communication with the API server fails temporarily.
	deadline, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)

	name := deployment.Name()
	framework.Logf("deleting the %s PMEM-CSI deployment", name)
	for _, h := range uninstallHooks {
		h(name)
	}

	filter := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s in (%s)", deploymentLabel, deployment.Label()),
	}
	infoDelay := 5 * time.Second
	infoTimestamp := time.Now().Add(infoDelay)
	for {
		success := true // No failures so far.
		done := true    // Nothing left.
		now := time.Now()
		showInfo := infoTimestamp.Before(now)
		if showInfo {
			infoTimestamp = now.Add(infoDelay)
		}
		failure := func(err error) bool {
			if err != nil && !apierrs.IsNotFound(err) {
				framework.Logf("remove PMEM-CSI: %v", err)
				success = false
				return true
			}
			return false
		}
		del := func(objectMeta metav1.ObjectMeta, object interface{}, deletor func() error) {
			// We found something in this loop iteration. Let's do another one
			// to verify that it really is gone.
			done = false

			// Already getting deleted?
			if objectMeta.DeletionTimestamp != nil {
				if showInfo {
					framework.Logf("waiting for deletion of %s (%T, %s)", objectMeta.Name, object, objectMeta.UID)
				}
				return
			}

			// It would be nice if we could print the runtime group/kind information
			// here, but TypeMeta in the objects returned by the client-go interfaces
			// is empty. If there is a way to retrieve it, then it wasn't obvious...
			framework.Logf("deleting %s (%T, %s)", objectMeta.Name, object, objectMeta.UID)
			err := deletor()
			failure(err)
		}

		// Delete all PMEM-CSI deployment objects first to avoid races with the operator
		// restarting things that we want removed.
		if list, err := c.dc.Resource(DeploymentResource).List(context.Background(), filter); !failure(err) && list != nil {
			for _, object := range list.Items {
				deployment := api.Deployment{}
				err := Scheme.Convert(&object, &deployment, nil)
				framework.ExpectNoError(err, "convert %v to PMEM-CSI deployment", object)
				del(deployment.ObjectMeta, deployment, func() error {
					return c.dc.Resource(DeploymentResource).Delete(context.Background(), deployment.Name, metav1.DeleteOptions{})
				})
			}
		}

		// We intentionally delete statefulset last because that is
		// how FindDeployment will find it again if we don't manage to
		// delete the entire deployment. Here we just scale it down
		// to trigger pod deletion.
		if list, err := c.cs.AppsV1().StatefulSets("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				if *object.Spec.Replicas != 0 {
					*object.Spec.Replicas = 0
					_, err := c.cs.AppsV1().StatefulSets(object.Namespace).Update(context.Background(), &object, metav1.UpdateOptions{})
					failure(err)
				}
			}
		}

		// Same for the operator's deployment.
		if list, err := c.cs.AppsV1().Deployments("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				if *object.Spec.Replicas != 0 {
					*object.Spec.Replicas = 0
					_, err := c.cs.AppsV1().Deployments(object.Namespace).Update(context.Background(), &object, metav1.UpdateOptions{})
					failure(err)
				}
			}
		}

		if list, err := c.cs.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.AppsV1().DaemonSets("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.AppsV1().DaemonSets(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.CoreV1().Pods("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.CoreV1().Pods(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.RbacV1().Roles("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.RbacV1().Roles(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.RbacV1().RoleBindings("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.RbacV1().RoleBindings(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.RbacV1().ClusterRoles().List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.RbacV1().ClusterRoles().Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.RbacV1().ClusterRoleBindings().List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.RbacV1().ClusterRoleBindings().Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.CoreV1().Services("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.CoreV1().Services(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.CoreV1().Endpoints("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.CoreV1().Endpoints(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.CoreV1().ServiceAccounts("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.CoreV1().ServiceAccounts(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.CoreV1().Secrets("").List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.CoreV1().Secrets(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if list, err := c.cs.StorageV1beta1().CSIDrivers().List(context.Background(), filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.StorageV1beta1().CSIDrivers().Delete(context.Background(), object.Name, metav1.DeleteOptions{})
				})
			}
		}

		if done {
			// Nothing else left, now delete the deployments and statefulsets.
			if list, err := c.cs.AppsV1().Deployments("").List(context.Background(), filter); !failure(err) {
				for _, object := range list.Items {
					del(object.ObjectMeta, object, func() error {
						return c.cs.AppsV1().Deployments(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
					})
				}
			}
			if list, err := c.cs.AppsV1().StatefulSets("").List(context.Background(), filter); !failure(err) {
				for _, object := range list.Items {
					del(object.ObjectMeta, object, func() error {
						return c.cs.AppsV1().StatefulSets(object.Namespace).Delete(context.Background(), object.Name, metav1.DeleteOptions{})
					})
				}
			}
		}

		if done && success {
			return nil
		}

		// The actual API calls above are quick, actual deletion
		// is slower. Here we wait for a short while and then
		// check again whether all objects have been deleted.
		select {
		case <-deadline.Done():
			return fmt.Errorf("timed out while trying to delete the %s PMEM-CSI deployment", name)
		case <-ticker.C:
		}
	}
}

// Deployment contains some information about a some deployed PMEM-CSI component(s).
// Those components can be a full driver installation and/or just the operator.
type Deployment struct {
	// HasDriver is true if the driver itself is running. The
	// driver is reacting to the usual pmem-csi.intel.com driver
	// name.
	HasDriver bool

	// HasOperator is true if the operator is running.
	HasOperator bool

	// HasOLM is true if the OLM(OperatorLifecycleManager) is running.
	HasOLM bool

	// Mode is the driver mode of the deployment.
	Mode api.DeviceMode

	// Namespace where the namespaced objects of the deployment
	// were created.
	Namespace string

	// Testing is true when socat pods are available.
	Testing bool

	// A version of the format X.Y when installing an older
	// release from the release-X.Y branch.
	Version string
}

func (d Deployment) DeploymentMode() string {
	if d.Testing {
		return "testing"
	}
	return "production"
}

// Name returns a string that encodes all attributes in the format expected by Parse.
func (d Deployment) Name() string {
	var parts []string
	switch {
	case d.HasOLM:
		parts = append(parts, "olm")
	case d.HasOperator:
		parts = append(parts, "operator")
	}
	if d.HasDriver {
		parts = append(parts, string(d.Mode))
		parts = append(parts, d.DeploymentMode())
	}
	if d.Version != "" {
		parts = append(parts, d.Version)
	}
	return strings.Join(parts, "-")
}

// Label returns the label used for objects belonging to the deployment.
// It's the same as the name minus the version. The reason for not including
// the version in the label value is that previous releases did not
// have that either. We have to stay consistent with that for up- and downgrade
// testing.
func (d Deployment) Label() string {
	d.Version = ""
	return d.Name()
}

// FindDeployment checks whether there is a PMEM-CSI driver and/or
// operator deployment in the cluster. A deployment is found via its
// deployment resp. statefulset object, which must have a
// pmem-csi.intel.com/deployment label.
func FindDeployment(c *Cluster) (*Deployment, error) {
	driver, err := findDriver(c)
	if err != nil {
		return nil, err
	}
	operator, err := findOperator(c)
	if err != nil {
		return nil, err
	}
	if operator != nil && driver != nil && operator.Name() != driver.Name() {
		return nil, fmt.Errorf("found two different deployments: %s and %s", operator.Name(), driver.Name())
	}
	if operator != nil {
		return operator, nil
	}
	if driver != nil {
		return driver, nil
	}
	return nil, nil
}

var imageVersion = regexp.MustCompile(`pmem-csi-driver(?:-test)?:v(\d+\.\d+)`)

func findDriver(c *Cluster) (*Deployment, error) {
	list, err := c.cs.AppsV1().StatefulSets("").List(context.Background(), metav1.ListOptions{LabelSelector: deploymentLabel})
	if err != nil {
		return nil, err
	}

	if len(list.Items) == 0 {
		return nil, nil
	}
	name := list.Items[0].Labels[deploymentLabel]
	deployment, err := Parse(name)
	if err != nil {
		return nil, fmt.Errorf("parse label of deployment %s: %v", list.Items[0].Name, err)
	}
	deployment.Namespace = list.Items[0].Namespace

	// Derive the version from the image tag. The annotation doesn't include it.
	// If the version matches what we are currently testing, then we skip
	// the version (i.e. "current version" == "no explicit version").
	for _, container := range list.Items[0].Spec.Template.Spec.Containers {
		m := imageVersion.FindStringSubmatch(container.Image)
		if m != nil {
			m2 := imageVersion.FindStringSubmatch(os.Getenv("PMEM_CSI_IMAGE"))
			if m2 == nil || m2[1] != m[1] {
				deployment.Version = m[1]
			}
			break
		}
	}

	// Currently we don't support parallel installations, so all
	// objects must belong to each other.
	for _, item := range list.Items {
		if item.Labels[deploymentLabel] != name {
			return nil, fmt.Errorf("found at least two different deployments: %s and %s", item.Labels[deploymentLabel], name)
		}
	}

	return deployment, nil
}

func findOperator(c *Cluster) (*Deployment, error) {
	// In case of operator deployed by OLM the labels on the Deployment object
	// get overwritten by OLM reconcile loop.
	// But the ReplicaSet underneath holds the labels we set on Deployment.
	// So to cover all the cases we depend on ReplicaSet label.
	list, err := c.cs.AppsV1().ReplicaSets("").List(context.Background(), metav1.ListOptions{LabelSelector: deploymentLabel})
	if err != nil {
		return nil, err
	}

	if len(list.Items) == 0 {
		return nil, nil
	}
	name := list.Items[0].Labels[deploymentLabel]
	deployment, err := Parse(name)
	if err != nil {
		return nil, fmt.Errorf("parse label of deployment %s: %v", list.Items[0].Name, err)
	}
	deployment.Namespace = list.Items[0].Namespace

	// Currently we don't support parallel installations, so all
	// objects must belong to each other.
	for _, item := range list.Items {
		if item.Labels[deploymentLabel] != name {
			return nil, fmt.Errorf("found at least two different deployments: %s and %s", item.Labels[deploymentLabel], name)
		}
	}

	return deployment, nil
}

var allDeployments = []string{
	"lvm-testing",
	"lvm-production",
	"direct-testing",
	"direct-production",
	"operator",
	"operator-lvm-production",
	"operator-direct-production", // Uses kube-system, to ensure that deployment in a namespace also works.
	"olm",                        // operator installed by OLM
}
var deploymentRE = regexp.MustCompile(`^(operator|olm)?-?(\w*)?-?(testing|production)?-?([0-9\.]*)$`)

// Parse the deployment name and sets fields accordingly.
func Parse(deploymentName string) (*Deployment, error) {
	deployment := &Deployment{
		Namespace: "default",
	}
	if deploymentName == "operator-direct-production" {
		deployment.Namespace = "kube-system"
	}

	matches := deploymentRE.FindStringSubmatch(deploymentName)
	if matches == nil {
		return nil, fmt.Errorf("unsupported deployment %s", deploymentName)
	}
	switch matches[1] {
	case "olm":
		deployment.HasOLM = true
		deployment.HasOperator = true
	case "operator":
		deployment.HasOperator = true
	}
	if matches[2] != "" {
		deployment.HasDriver = true
		deployment.Testing = matches[3] == "testing"
		if err := deployment.Mode.Set(matches[2]); err != nil {
			return nil, fmt.Errorf("deployment name %s: %v", deploymentName, err)
		}
	}
	deployment.Version = matches[4]

	return deployment, nil
}

// MustParse calls Parse and panics when the name is not valid.
func MustParse(deploymentName string) *Deployment {
	deployment, err := Parse(deploymentName)
	if err != nil {
		framework.Failf("internal error while parsing %s: %v", deploymentName, err)
	}
	return deployment
}

// EnsureDeployment registers a BeforeEach function which will ensure that when
// a test runs, the desired deployment exists. Deployed drivers are intentionally
// kept running to speed up the execution of multiple tests that all want the
// same kind of deployment.
//
// The driver should never restart. A restart would indicate some
// (potentially intermittent) issue.
func EnsureDeployment(deploymentName string) *Deployment {
	deployment := MustParse(deploymentName)

	f := framework.NewDefaultFramework("cluster")
	f.SkipNamespaceCreation = true
	var prevVol map[string][]string

	ginkgo.BeforeEach(func() {
		ginkgo.By(fmt.Sprintf("preparing for test %q in namespace %s",
			ginkgo.CurrentGinkgoTestDescription().FullTestText,
			deployment.Namespace,
		))

		// Remember list of volumes before test, using out-of-band host commands (i.e. not CSI API).
		prevVol = GetHostVolumes(deployment)

		EnsureDeploymentNow(f, deployment)

		for _, h := range installHooks {
			h(deployment)
		}
	})

	ginkgo.AfterEach(func() {
		state := "success"
		if ginkgo.CurrentGinkgoTestDescription().Failed {
			state = "failure"
		}
		ginkgo.By(fmt.Sprintf("checking for test %q in namespace %s, test %s",
			ginkgo.CurrentGinkgoTestDescription().FullTestText,
			deployment.Namespace,
			state,
		))

		// Check list of volumes after test to detect left-overs
		CheckForLeftoverVolumes(deployment, prevVol)

		// And check that PMEM is in a sane state.
		CheckPMEM()
	})

	return deployment
}

// EnsureDeploymentNow checks the currently running driver and replaces it if necessary.
func EnsureDeploymentNow(f *framework.Framework, deployment *Deployment) {
	c, err := NewCluster(f.ClientSet, f.DynamicClient)
	framework.ExpectNoError(err, "get cluster information")
	running, err := FindDeployment(c)
	framework.ExpectNoError(err, "check for PMEM-CSI components")
	framework.Logf("want %s PMEM-CSI deployment = %+v", deployment.Name(), *deployment)
	if running != nil {
		if reflect.DeepEqual(deployment, running) {
			framework.Logf("reusing existing %s PMEM-CSI components", deployment.Name())
			// Do some sanity checks on the running deployment before the test.
			if deployment.HasDriver {
				WaitForPMEMDriver(c, "pmem-csi", deployment)
				CheckPMEMDriver(c, deployment)
			}
			if deployment.HasOperator {
				WaitForOperator(c, deployment.Namespace)
			}
			if deployment.HasOLM {
				WaitForOLM(c, "olm")
			}
			return
		}
		framework.Logf("have %s PMEM-CSI deployment, want %s -> delete existing deployment", running.Name(), deployment.Name())

		if running.HasOLM {
			cmd := exec.Command("test/stop-operator.sh", "-olm")
			cmd.Dir = os.Getenv("REPO_ROOT")
			cmd.Env = append(os.Environ(),
				"TEST_OPERATOR_NAMESPACE="+running.Namespace,
				"TEST_OPERATOR_DEPLOYMENT_LABEL="+running.Label())
			_, err := pmemexec.Run(cmd)
			framework.ExpectNoError(err, "delete operator deployment: %q", deployment.Name())
		}
		err := RemoveObjects(c, running)
		framework.ExpectNoError(err, "remove PMEM-CSI deployment")
	}
	if deployment.HasOLM {
		cmd := exec.Command("test/start-stop-olm.sh", "start")
		cmd.Dir = os.Getenv("REPO_ROOT")
		_, err := pmemexec.Run(cmd)
		framework.ExpectNoError(err, "create operator deployment: %q", deployment.Name())
		WaitForOLM(c, "olm")
	}

	if deployment.HasOperator {
		// At the moment, the only supported deployment method is via test/start-operator.sh.
		cmdArgs := []string{}
		if deployment.HasOLM {
			cmdArgs = append(cmdArgs, "-olm")
		}
		cmd := exec.Command("test/start-operator.sh", cmdArgs...)
		cmd.Dir = os.Getenv("REPO_ROOT")
		cmd.Env = append(os.Environ(),
			"TEST_OPERATOR_NAMESPACE="+deployment.Namespace,
			"TEST_OPERATOR_DEPLOYMENT_LABEL="+deployment.Label())
		_, err := pmemexec.Run(cmd)
		framework.ExpectNoError(err, "create operator deployment: %q", deployment.Name())
		WaitForOperator(c, deployment.Namespace)
	}
	if deployment.HasDriver {
		if deployment.HasOperator {
			// Deploy driver through operator.
			dep := deployment.GetDriverDeployment()
			EnsureDeploymentCR(f, dep)
		} else {
			// Deploy with script.
			root := os.Getenv("REPO_ROOT")
			env := os.Environ()
			if deployment.Version != "" {
				// Find the latest dot release on the branch for which images are public.
				// Most recent tag is listed first. We better avoid pulling over and over again
				// to avoid throttling.
				tags, err := pmemexec.RunCommand("git", "tag", "--sort=-version:refname")
				scanner := bufio.NewScanner(strings.NewReader(tags))
				var tag string
				for scanner.Scan() {
					tag = scanner.Text()
					if strings.HasPrefix(tag, "v"+deployment.Version) {
						if _, err := pmemexec.RunCommand("docker", "image", "inspect", "--format='exists'", "intel/pmem-csi-driver:"+tag); err == nil {
							break
						}
						if _, err := pmemexec.RunCommand("docker", "image", "pull", "intel/pmem-csi-driver:"+tag); err == nil {
							break
						}
					}
				}
				framework.Logf("using %s images for release-%s", tag, deployment.Version)

				// Clean check out in _work/pmem-csi-release-<version>.
				// Pulling from remote must be done before running the test.
				workRoot := root + "/_work/pmem-csi-release-" + deployment.Version
				err = os.RemoveAll(workRoot)
				framework.ExpectNoError(err, "remove PMEM-CSI source code")
				_, err = pmemexec.RunCommand("git", "clone", "--shared", root, workRoot)
				framework.ExpectNoError(err, "clone repo", deployment.Version)
				_, err = pmemexec.RunCommand("git", "-C", workRoot, "checkout", tag)
				framework.ExpectNoError(err, "check out release-%s = %s of PMEM-CSI", deployment.Version, tag)
				root = workRoot

				// The release branch does not pull from Docker Hub by default,
				// we have to select that explicitly.
				env = append(env, "TEST_PMEM_REGISTRY=intel")

				// The setup script expects to have
				// the same _work as in the normal
				// root.
				err = os.Symlink("../../_work", workRoot+"/_work")
				framework.ExpectNoError(err, "symlink the _work directory")
			}
			cmd := exec.Command("test/setup-deployment.sh")
			cmd.Dir = root
			env = append(env,
				"REPO_ROOT="+root,
				"TEST_DEPLOYMENT_QUIET=quiet",
				"TEST_DEPLOYMENTMODE="+deployment.DeploymentMode(),
				"TEST_DEVICEMODE="+string(deployment.Mode))
			cmd.Env = env
			_, err = pmemexec.Run(cmd)
			framework.ExpectNoError(err, "create %s PMEM-CSI deployment", deployment.Name())
		}

		// We check for a running driver the same way at the moment, by directly
		// looking at the driver state. Long-term we want the operator to do that
		// checking itself.
		WaitForPMEMDriver(c, "pmem-csi", deployment)
		CheckPMEMDriver(c, deployment)
	}
}

// GetDriverDeployment returns the spec for the driver deployment that is used
// for deployments like operator-lvm-production.
func (d *Deployment) GetDriverDeployment() api.Deployment {
	return api.Deployment{
		// TypeMeta is needed because
		// DefaultUnstructuredConverter does not add it for us. Is there a better way?
		TypeMeta: metav1.TypeMeta{
			APIVersion: api.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "pmem-csi",
			Labels: map[string]string{
				deploymentLabel: d.Label(),
			},
		},
		Spec: api.DeploymentSpec{
			Labels: map[string]string{
				deploymentLabel: d.Label(),
			},
			DeviceMode: d.Mode,
			// As in setup-deployment.sh, only 50% of the available
			// PMEM must be used for LVM, otherwise other tests cannot
			// run after the LVM driver was deployed once.
			PMEMPercentage: 50,
			NodeSelector: map[string]string{
				// Provided by NFD.
				"feature.node.kubernetes.io/memory-nv.dax": "true",
			},
		},
	}
}

// DeleteAllPods deletes all currently running pods that belong to the deployment.
func (d Deployment) DeleteAllPods(c *Cluster) error {
	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s in (%s)", deploymentLabel, d.Label()),
	}
	pods, err := c.cs.CoreV1().Pods(d.Namespace).List(context.Background(), listOptions)
	if err != nil {
		return fmt.Errorf("list all PMEM-CSI pods: %v", err)
	}
	// Kick of deletion of several pods at once.
	if err := c.cs.CoreV1().Pods(d.Namespace).DeleteCollection(context.Background(),
		metav1.DeleteOptions{},
		listOptions,
	); err != nil {
		return fmt.Errorf("delete all PMEM-CSI pods: %v", err)
	}
	// But still wait for every single one to be gone...
	for _, pod := range pods.Items {
		if err := waitForPodDeletion(c, pod); err != nil {
			return fmt.Errorf("wait for pod deletion: %v", err)
		}
	}
	return nil
}

// LookupCSIAddresses returns controller and node addresses for gRPC dial.
// Only works for testing deployments.
func LookupCSIAddresses(c *Cluster, namespace string) (nodeAddress, controllerAddress string, err error) {
	// Node #1 is expected to have a PMEM-CSI node driver
	// instance. If it doesn't, connecting to the PMEM-CSI
	// node service will fail.
	nodeAddress = c.NodeServiceAddress(1, SocatPort)

	// The cluster controller service can be reached via
	// any node, what matters is the service port.
	port, err := c.GetServicePort(context.Background(), "pmem-csi-controller-testing", namespace)
	if err != nil {
		return "", "", fmt.Errorf("get PMEM-CSI controller service port: %v", err)
	}
	controllerAddress = c.NodeServiceAddress(0, port)

	return
}

// DescribeForAll registers tests like gomega.Describe does, except that
// each test will then be invoked for each supported PMEM-CSI deployment
// which has a functional PMEM-CSI driver.
func DescribeForAll(what string, f func(d *Deployment)) bool {
	DescribeForSome(what, RunAllTests, f)
	return true
}

// HasDriver is a filter function for DescribeForSome.
func HasDriver(d *Deployment) bool {
	return d.HasDriver
}

// HasOperator is a filter function for DescribeForSome.
func HasOperator(d *Deployment) bool {
	return d.HasOperator
}

// RunAllTests is a filter function for DescribeForSome which decides
// against what we run the full Kubernetes storage test
// suite. Currently do this for deployments created via .yaml files
// whereas testing with the operator is excluded. This is meant to
// keep overall test suite runtime reasonable and avoid duplication.
func RunAllTests(d *Deployment) bool {
	return d.HasDriver && !d.HasOperator
}

// DescribeForSome registers tests like gomega.Describe does, except that
// each test will then be invoked for those PMEM-CSI deployments which
// pass the filter function.
func DescribeForSome(what string, enabled func(d *Deployment) bool, f func(d *Deployment)) bool {
	for _, deploymentName := range allDeployments {
		deployment := MustParse(deploymentName)
		if enabled(deployment) {
			Describe(deploymentName, deploymentName, what, f)
		}
	}

	return true
}

// deployment name -> top level describe string -> list of test functions for that combination
var tests = map[string]map[string][]func(d *Deployment){}

// Describe remembers a certain test. The actual registration in
// Ginkgo happens in DefineTests, ordered such that all tests with the
// same "deployment" string are defined on after the after with the
// given "describe" string.
//
// When "describe" is already unique, "what" can be left empty.
func Describe(deployment, describe, what string, f func(d *Deployment)) bool {
	group := tests[deployment]
	if group == nil {
		group = map[string][]func(d *Deployment){}
	}
	group[describe] = append(group[describe], func(d *Deployment) {
		if what == "" {
			// Skip one nesting layer.
			f(d)
			return
		}
		ginkgo.Describe(what, func() {
			f(d)
		})
	})
	tests[deployment] = group

	return true
}

// DefineTests must be called to register all tests defined so far via Describe.
func DefineTests() {
	for deploymentName, group := range tests {
		deploymentName := deploymentName
		for describe, funcs := range group {
			funcs := funcs
			ginkgo.Describe(describe, func() {
				deployment := EnsureDeployment(deploymentName)
				for _, f := range funcs {
					f(deployment)
				}
			})
		}
	}
}

// waitForPodDeletion returns an error if it takes too long for the pod to fully terminate.
func waitForPodDeletion(c *Cluster, pod v1.Pod) error {
	return wait.PollImmediate(2*time.Second, time.Minute, func() (bool, error) {
		existingPod, err := c.cs.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil // done
		}
		if err != nil {
			return true, err // stop wait with error
		}
		if pod.UID != existingPod.UID {
			return true, nil // also done (pod was restarted)
		}
		return false, nil
	})
}
