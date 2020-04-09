/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"time"

	"github.com/prometheus/common/expfmt"
	v1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/test/e2e/framework"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/pmem-csi-driver"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
)

const (
	deploymentLabel = "pmem-csi.intel.com/deployment"
)

// InstallHook is the callback function for AddInstallHook.
type InstallHook func(Deployment *Deployment)

// UninstallHook is the callback function for AddUninstallHook.
type UninstallHook func(deploymentName string)

var (
	installHooks   []InstallHook
	uninstallHooks []UninstallHook
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
func WaitForOperator(c *Cluster, namespace string) {
	// TODO(avalluri): At later point of time we should add readiness support
	// for the operator. Then we can query directoly the operator if its ready.
	// As interm solution we are just checking Pod.Status.
	gomega.Eventually(func() bool {
		pod, err := c.GetAppInstance("pmem-csi-operator", "", namespace)
		return err == nil && pod.Status.Phase == v1.PodRunning
	}, "5m", "2s").Should(gomega.BeTrue(), "operator not running in namespace %s", namespace)
	ginkgo.By("Operator is ready!")
}

// WaitForPMEMDriver ensures that the PMEM-CSI driver is ready for use, which is
// defined as:
// - controller service is up and running
// - all nodes have registered
func WaitForPMEMDriver(c *Cluster, namespace string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline, cancel := context.WithTimeout(context.Background(), framework.TestContext.SystemDaemonsetStartupTimeout)
	defer cancel()

	tlsConfig := tls.Config{
		// We could load ca.pem with pmemgrpc.LoadClientTLS, but as we are not connecting to it
		// via the service name, that would be enough.
		InsecureSkipVerify: true,
	}
	tr := http.Transport{
		TLSClientConfig: &tlsConfig,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: &tr,
	}

	ready := func() (err error) {
		defer func() {
			if err != nil {
				framework.Logf("wait for PMEM-CSI: %v", err)
			}
		}()

		// The controller service must be defined.
		port, err := c.GetServicePort("pmem-csi-metrics", "default")
		if err != nil {
			return err
		}

		// We can connect to it and get metrics data.
		url := fmt.Sprintf("https://%s:%d/metrics", c.NodeIP(0), port)
		resp, err := client.Get(url)
		if err != nil {
			return fmt.Errorf("get controller metrics: %v", err)
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("HTTP GET %s failed: %d", url, resp.StatusCode)
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
			return fmt.Errorf("expected build_info to contain a version label, got: %s", label.Name)
		}
		framework.Logf("PMEM-CSI version: %s", *label.Value)

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

		return nil
	}

	if ready() == nil {
		return
	}
	for {
		select {
		case <-ticker.C:
			if ready() == nil {
				return
			}
		case <-deadline.Done():
			framework.Failf("giving up waiting for PMEM-CSI to start up, check the previous warnings and log output")
		}
	}
}

// RemoveObjects deletes everything that might have been created for a
// PMEM-CSI driver or operator installation (pods, daemonsets,
// statefulsets, driver info, storage classes, etc.).
func RemoveObjects(c *Cluster, deploymentName string) error {
	// Try repeatedly, in case that communication with the API server fails temporarily.
	deadline, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	framework.Logf("deleting the %s PMEM-CSI deployment", deploymentName)
	for _, h := range uninstallHooks {
		h(deploymentName)
	}

	filter := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s in (%s)", deploymentLabel, deploymentName),
	}
	for {
		success := true // No failures so far.
		done := true    // Nothing left.
		failure := func(err error) bool {
			if err != nil && !apierrs.IsNotFound(err) {
				framework.Logf("remove PMEM-CSI: %v", err)
				success = false
				return true
			}
			return false
		}
		del := func(object metav1.ObjectMeta, deletor func() error) {
			// We found something in this loop iteration. Let's do another one
			// to verify that it really is gone.
			done = false

			// Already getting deleted?
			if object.DeletionTimestamp != nil {
				return
			}

			framework.Logf("deleting %s", object.Name)
			err := deletor()
			failure(err)
		}

		// We intentionally delete statefulset last because that is
		// how FindDeployment will find it again if we don't manage to
		// delete the entire deployment. Here we just scale it down
		// to trigger pod deletion.
		if list, err := c.cs.AppsV1().StatefulSets("").List(filter); !failure(err) {
			for _, object := range list.Items {
				if *object.Spec.Replicas != 0 {
					*object.Spec.Replicas = 0
					_, err := c.cs.AppsV1().StatefulSets(object.Namespace).Update(&object)
					failure(err)
				}
			}
		}

		// Same for the operator's deployment.
		if list, err := c.cs.AppsV1().Deployments("").List(filter); !failure(err) {
			for _, object := range list.Items {
				if *object.Spec.Replicas != 0 {
					*object.Spec.Replicas = 0
					_, err := c.cs.AppsV1().Deployments(object.Namespace).Update(&object)
					failure(err)
				}
			}
		}

		if list, err := c.cs.AppsV1().DaemonSets("").List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.AppsV1().DaemonSets(object.Namespace).Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.CoreV1().Pods("").List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.CoreV1().Pods(object.Namespace).Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.RbacV1().Roles("").List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.RbacV1().Roles(object.Namespace).Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.RbacV1().RoleBindings("").List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.RbacV1().RoleBindings(object.Namespace).Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.RbacV1().ClusterRoles().List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.RbacV1().ClusterRoles().Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.RbacV1().ClusterRoleBindings().List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.RbacV1().ClusterRoleBindings().Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.CoreV1().Services("").List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.CoreV1().Services(object.Namespace).Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.CoreV1().ServiceAccounts("").List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.CoreV1().ServiceAccounts(object.Namespace).Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.CoreV1().Secrets("").List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, object, func() error {
					return c.cs.CoreV1().Secrets(object.Namespace).Delete(object.Name, nil)
				})
			}
		}

		if list, err := c.cs.StorageV1beta1().CSIDrivers().List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.StorageV1beta1().CSIDrivers().Delete(object.Name, nil)
				})
			}
		}

		if done {
			// Nothing else left, now delete the deployments and statefulsets.
			if list, err := c.cs.AppsV1().Deployments("").List(filter); !failure(err) {
				for _, object := range list.Items {
					del(object.ObjectMeta, func() error {
						return c.cs.AppsV1().Deployments(object.Namespace).Delete(object.Name, nil)
					})
				}
			}
			if list, err := c.cs.AppsV1().StatefulSets("").List(filter); !failure(err) {
				for _, object := range list.Items {
					del(object.ObjectMeta, func() error {
						return c.cs.AppsV1().StatefulSets(object.Namespace).Delete(object.Name, nil)
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
			return fmt.Errorf("timed out while trying to delete the %s PMEM-CSI deployment", deploymentName)
		case <-ticker.C:
		}
	}
}

// Deployment contains some information about a some deployed PMEM-CSI component(s).
// Those components can be a full driver installation and/or just the operator.
type Deployment struct {
	// Name string that all objects from the same deployment must
	// have in the DeploymentLabel.
	Name string

	// HasDriver is true if the driver itself is running. The
	// driver is reacting to the usual pmem-csi.intel.com driver
	// name.
	HasDriver bool

	// HasOperator is true if the operator is running.
	HasOperator bool

	// Mode is the driver mode of the deployment.
	Mode pmemcsidriver.DeviceMode

	// Namespace where the namespaced objects of the deployment
	// were created.
	Namespace string

	// Testing is true when socat pods are available.
	Testing bool
}

func (d Deployment) DeploymentMode() string {
	if d.Testing {
		return "testing"
	}
	return "production"
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
	if operator != nil && driver != nil && operator.Name != driver.Name {
		return nil, fmt.Errorf("found two different deployments: %s and %s", operator.Name, driver.Name)
	}
	if operator != nil {
		return operator, nil
	}
	if driver != nil {
		return driver, nil
	}
	return nil, nil
}

func findDriver(c *Cluster) (*Deployment, error) {
	list, err := c.cs.AppsV1().StatefulSets("").List(metav1.ListOptions{LabelSelector: deploymentLabel})
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

func findOperator(c *Cluster) (*Deployment, error) {
	list, err := c.cs.AppsV1().Deployments("").List(metav1.ListOptions{LabelSelector: deploymentLabel})
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
	"operator-direct-production",
}
var deploymentRE = regexp.MustCompile(`^(operator)?-?(\w*)?-?(testing|production)?$`)

// Parse the deployment name and sets fields accordingly.
func Parse(deploymentName string) (*Deployment, error) {
	deployment := &Deployment{
		Name:      deploymentName,
		Namespace: "default",
	}

	matches := deploymentRE.FindStringSubmatch(deploymentName)
	if matches == nil {
		return nil, fmt.Errorf("unsupported deployment %s", deploymentName)
	}
	if matches[1] == "operator" {
		deployment.HasOperator = true
	}
	if matches[2] != "" {
		deployment.HasDriver = true
		deployment.Testing = matches[3] == "testing"
		if err := deployment.Mode.Set(matches[2]); err != nil {
			return nil, fmt.Errorf("deployment name %s: %v", deploymentName, err)
		}
	}

	return deployment, nil
}

// EnsureDeployment registers a BeforeEach function which will ensure that when
// a test runs, the desired deployment exists. Deployed drivers are intentionally
// kept running to speed up the execution of multiple tests that all want the
// same kind of deployment.
func EnsureDeployment(deploymentName string) *Deployment {
	deployment, err := Parse(deploymentName)
	if err != nil {
		framework.Failf("internal error while parsing %s: %v", deploymentName, err)
	}

	f := framework.NewDefaultFramework("cluster")
	f.SkipNamespaceCreation = true

	ginkgo.BeforeEach(func() {
		ginkgo.By(fmt.Sprintf("preparing for test %q", ginkgo.CurrentGinkgoTestDescription().FullTestText))
		c, err := NewCluster(f.ClientSet)
		framework.ExpectNoError(err, "get cluster information")
		running, err := FindDeployment(c)
		framework.ExpectNoError(err, "check for PMEM-CSI components")
		if running != nil {
			if reflect.DeepEqual(deployment, running) {
				framework.Logf("reusing existing %s PMEM-CSI components", deployment.Name)
				// Do some sanity checks on the running deployment before the test.
				if deployment.HasDriver {
					WaitForPMEMDriver(c, deployment.Namespace)
				}
				if deployment.HasOperator {
					WaitForOperator(c, deployment.Namespace)
				}
				return
			}
			framework.Logf("have %s PMEM-CSI deployment, want %s -> delete existing deployment", running.Name, deployment.Name)
			err := RemoveObjects(c, running.Name)
			framework.ExpectNoError(err, "remove PMEM-CSI deployment")
		}

		if deployment.HasOperator {
			// At the moment, the only supported deployment method is via test/start-operator.sh.
			cmd := exec.Command("test/start-operator.sh")
			cmd.Dir = os.Getenv("REPO_ROOT")
			cmd.Env = append(os.Environ(),
				"TEST_OPERATOR_DEPLOYMENT="+deployment.Name)
			cmd.Stdout = ginkgo.GinkgoWriter
			cmd.Stderr = ginkgo.GinkgoWriter
			err = cmd.Run()
			framework.ExpectNoError(err, "create operator deployment: %q", deployment.Name)

			WaitForOperator(c, deployment.Namespace)
		}
		if deployment.HasDriver {
			if deployment.HasOperator {
				// Deploy driver through operator.
				dep := &api.Deployment{
					// TypeMeta is needed because
					// DefaultUnstructuredConverter does not add it for us. Is there a better way?
					TypeMeta: metav1.TypeMeta{
						APIVersion: api.SchemeGroupVersion.String(),
						Kind:       "Deployment",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "pmem-csi",
					},
					Spec: api.DeploymentSpec{
						Image: os.Getenv("PMEM_CSI_IMAGE"), // workaround for https://github.com/intel/pmem-csi/issues/578
						Labels: map[string]string{
							deploymentLabel: deployment.Name,
						},
						// TODO: replace pmemcsidriver.DeviceMode with api.DeviceMode everywhere
						// and remove this cast here.
						DeviceMode: api.DeviceMode(deployment.Mode),
						// As in setup-deployment.sh, only 50% of the available
						// PMEM must be used for LVM, otherwise other tests cannot
						// run after the LVM driver was deployed once.
						PMEMPercentage: 50,
					},
				}
				hash, err := runtime.DefaultUnstructuredConverter.ToUnstructured(dep)
				framework.ExpectNoError(err, "convert %v", dep)
				depUnstructured := &unstructured.Unstructured{
					Object: hash,
				}
				CreateDeploymentCR(f, depUnstructured)
			} else {
				// Deploy with script.
				cmd := exec.Command("test/setup-deployment.sh")
				cmd.Dir = os.Getenv("REPO_ROOT")
				cmd.Env = append(os.Environ(),
					"TEST_DEPLOYMENTMODE="+deployment.DeploymentMode(),
					"TEST_DEVICEMODE="+string(deployment.Mode))
				cmd.Stdout = ginkgo.GinkgoWriter
				cmd.Stderr = ginkgo.GinkgoWriter
				err = cmd.Run()
				framework.ExpectNoError(err, "create %s PMEM-CSI deployment", deployment.Name)
			}

			// We check for a running driver the same way at the moment, by directly
			// looking at the driver state. Long-term we want the operator to do that
			// checking itself.
			WaitForPMEMDriver(c, deployment.Namespace)
		}

		for _, h := range installHooks {
			h(deployment)
		}
	})

	return deployment
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
		deployment, err := Parse(deploymentName)
		if err != nil {
			framework.Failf("internal error while parsing %s: %v", deploymentName, err)
		}
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
		for describe, funcs := range group {
			ginkgo.Describe(describe, func() {
				deployment := EnsureDeployment(deploymentName)
				for _, f := range funcs {
					f(deployment)
				}
			})
		}
	}
}
