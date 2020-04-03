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
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/intel/pmem-csi/pkg/pmem-csi-driver"

	"github.com/onsi/ginkgo"
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

// RemovePMEMDriver deletes everything that has been created for a
// PMEM-CSI installation (pods, daemonsets, statefulsets, driver info,
// storage classes, etc.).
func RemovePMEMDriver(c *Cluster, deploymentName string) error {
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
		// how FindPMEMDriver will find it again if we don't manage to
		// delete the entire deployment. Here we just scale it down
		// to trigger pod deletion.
		if list, err := c.cs.AppsV1().StatefulSets("").List(filter); !failure(err) {
			for _, set := range list.Items {
				if *set.Spec.Replicas != 0 {
					*set.Spec.Replicas = 0
					_, err := c.cs.AppsV1().StatefulSets(set.Namespace).Update(&set)
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

		if list, err := c.cs.StorageV1beta1().CSIDrivers().List(filter); !failure(err) {
			for _, object := range list.Items {
				del(object.ObjectMeta, func() error {
					return c.cs.StorageV1beta1().CSIDrivers().Delete(object.Name, nil)
				})
			}
		}

		if done {
			// Nothing else left, now delete the statefulset.
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

// Deployment contains some information about a deployed PMEM-CSI instance.
type Deployment struct {
	// Name string that all objects from the same deployment must
	// have in the DeploymentLabel.
	Name string

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

// FindPMEMDriver checks whether there is a PMEM-CSI driver
// installation in the cluster. An installation is found via its
// statefulset, which must have a pmem-csi.intel.com/deployment label.
func FindPMEMDriver(c *Cluster) (*Deployment, error) {
	statefulsets, err := c.cs.AppsV1().StatefulSets("").List(metav1.ListOptions{LabelSelector: deploymentLabel})
	if err != nil {
		return nil, err
	}

	// Currently we don't support parallel installations.
	switch len(statefulsets.Items) {
	case 0:
		return nil, nil
	case 1:
		name := statefulsets.Items[0].Labels[deploymentLabel]
		deployment, err := Parse(name)
		if err != nil {
			return nil, fmt.Errorf("parse label of statefulset %s: %v", statefulsets.Items[0].Name, err)
		}
		deployment.Namespace = statefulsets.Items[0].Namespace
		return deployment, nil
	default:
		return nil, fmt.Errorf("found %d deployments", len(statefulsets.Items))
	}
}

var deploymentRE = regexp.MustCompile(`^(\w*)-(testing|production)$`)

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
	deployment.Testing = matches[2] == "testing"
	if err := deployment.Mode.Set(matches[1]); err != nil {
		return nil, fmt.Errorf("deployment name %s: %v", err)
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
		running, err := FindPMEMDriver(c)
		framework.ExpectNoError(err, "check for PMEM-CSI driver")
		if running != nil {
			if reflect.DeepEqual(deployment, running) {
				framework.Logf("reusing existing %s PMEM-CSI deployment", deployment.Name)
				return
			}
			framework.Logf("have %s PMEM-CSI deployment, want %s -> delete existing deployment", running.Name, deployment.Name)
			// Currently all deployments share the same driver name.
			err := RemovePMEMDriver(c, running.Name)
			framework.ExpectNoError(err, "remove PMEM-CSI deployment")
		}

		// At the moment, the only supported deployment method is via test/setup-deployment.sh.
		cmd := exec.Command("test/setup-deployment.sh")
		cmd.Dir = os.Getenv("REPO_ROOT")
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env,
			"TEST_DEPLOYMENTMODE="+deployment.DeploymentMode(),
			"TEST_DEVICEMODE="+string(deployment.Mode))
		cmd.Stdout = ginkgo.GinkgoWriter
		cmd.Stderr = ginkgo.GinkgoWriter
		err = cmd.Run()
		framework.ExpectNoError(err, "create %s PMEM-CSI deployment", deployment.Name)

		WaitForPMEMDriver(c, deployment.Namespace)
		for _, h := range installHooks {
			h(deployment)
		}
	})

	return deployment
}

var allDeployments = []string{
	"lvm-testing",
	"lvm-production",
	"direct-testing",
	"direct-production",
}

// DescribeForAll registers tests like gomega.Describe does, except that
// each test will then be invoked for each supported PMEM-CSI deployment.
func DescribeForAll(what string, f func(d *Deployment)) bool {
	for _, deploymentName := range allDeployments {
		Describe(deploymentName, deploymentName, what, f)
	}

	return true
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
