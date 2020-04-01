/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package operator

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/intel/pmem-csi/test/e2e/deploy"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/podlogs"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
)

// Operator contains some information about a deployed PMEM-CSI operator instance.
type Operator struct {
	// Name string that all objects from the same deployment must
	// have in the DeploymentLabel.
	Name string

	// Namespace where the namespaced objects of the deployment
	// were created.
	Namespace string
}

func newOperator(name, ns string) *Operator {
	return &Operator{
		Name:      name,
		Namespace: ns,
	}
}

// WaitForOperator ensures that the PMEM-CSI operator is ready for use, which is
// currently defined as the operator pod in Running phase.
func WaitForOperator(c *deploy.Cluster, operator *Operator) {
	pod := c.WaitForAppInstance(operator.Name, "", operator.Namespace)
	framework.Logf("Found operator pod '%s/%s'", pod.Namespace, pod.Name)

	// TODO(avalluri): At later point of time we should add readiness support
	// for the operator. Then we can query directoly the operator if its ready.
	// As interm solution we are just checking Pod.Status.
	gomega.Eventually(func() bool {
		pod, err := c.GetAppInstance(operator.Name, "", operator.Namespace)
		return err == nil && pod.Status.Phase == v1.PodRunning
	}, "5m", "2s").Should(gomega.BeTrue(), "%s operator not running", operator.Name)
	ginkgo.By("Operator is ready!")
}

// EnsureOperatorRemoved ensures that deletes everything that has been created for a
// PMEM-CSI operator installation (pods, daemonsets, statefulsets, driver info,
// storage classes, etc.).
func EnsureOperatorRemoved(c *deploy.Cluster, o *Operator) {
	cs := c.ClientSet()

	gomega.Eventually(func() bool {
		success := true // No failures so far.
		done := true    // Nothing left.
		failure := func(err error) bool {
			if err != nil && !apierrs.IsNotFound(err) {
				success = false
				return true
			}
			return false
		}
		getFailure := func(err error) bool {
			if err != nil {
				if !apierrs.IsNotFound(err) {
					success = false
				}
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

			failure(deletor())
		}

		if dep, err := cs.AppsV1().Deployments(o.Namespace).Get(o.Name, metav1.GetOptions{}); !getFailure(err) {
			del(dep.ObjectMeta, func() error {
				framework.Logf("Deleting deployment '%s/%s'", dep.Namespace, dep.Name)
				return cs.AppsV1().Deployments(dep.Namespace).Delete(dep.Name, nil)
			})
		}
		if role, err := cs.RbacV1().Roles(o.Namespace).Get(o.Name, metav1.GetOptions{}); !getFailure(err) {
			del(role.ObjectMeta, func() error {
				framework.Logf("Deleting role '%s/%s'", role.Namespace, role.Name)
				return cs.RbacV1().Roles(role.Namespace).Delete(role.Name, nil)
			})
		}

		if rb, err := cs.RbacV1().RoleBindings(o.Namespace).Get(o.Name, metav1.GetOptions{}); !getFailure(err) {
			del(rb.ObjectMeta, func() error {
				framework.Logf("Deleting role-bindings '%s/%s'", rb.Namespace, rb.Name)
				return cs.RbacV1().RoleBindings(rb.Namespace).Delete(rb.Name, nil)
			})
		}

		if cr, err := cs.RbacV1().ClusterRoles().Get(o.Name, metav1.GetOptions{}); !getFailure(err) {
			del(cr.ObjectMeta, func() error {
				framework.Logf("Deleting cluster role %q", cr.Name)
				return cs.RbacV1().ClusterRoles().Delete(cr.Name, nil)
			})
		}

		if crb, err := cs.RbacV1().ClusterRoleBindings().Get(o.Name, metav1.GetOptions{}); !getFailure(err) {
			del(crb.ObjectMeta, func() error {
				framework.Logf("Deleting cluster role-binding %q", crb.Name)
				return cs.RbacV1().ClusterRoleBindings().Delete(crb.Name, nil)
			})
		}

		if sa, err := cs.CoreV1().ServiceAccounts(o.Namespace).Get(o.Name, metav1.GetOptions{}); !getFailure(err) {
			del(sa.ObjectMeta, func() error {
				framework.Logf("Deleting service account '%s/%s'", sa.Namespace, sa.Name)
				return cs.CoreV1().ServiceAccounts(sa.Namespace).Delete(sa.Name, nil)
			})
		}
		return done && success
	}, "3m", "1s").Should(gomega.BeTrue(), "timed out while trying to delete the PMEM-CSI operator in namespace %q", o.Namespace)
}

// FindOperatorDeployment checks whether there is a PMEM-CSI operator
// installation in the cluster. An installation is found via its
// deployment name and namespace.
func FindOperatorDeployment(c *deploy.Cluster, o *Operator) (*appsv1.Deployment, error) {
	cs := c.ClientSet()
	framework.Logf("Checking if the operator '%s/%s' running", o.Namespace, o.Name)
	dep, err := cs.AppsV1().Deployments(o.Namespace).Get(o.Name, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	return dep, nil
}

// EnsureOperatorDeployed registers a BeforeEach function which will ensure that when
// a test runs, the desired deployment exists. Deployed drivers are intentionally
// kept running to speed up the execution of multiple tests that all want the
// same kind of deployment.
func EnsureOperatorDeployed(c *deploy.Cluster, o *Operator) {
	dep, err := FindOperatorDeployment(c, o)
	framework.ExpectNoError(err, "check for PMEM-CSI driver")
	if dep != nil {
		framework.Logf("delete existing operator deployment '%s/%s'", dep.Namespace, dep.Name)
		// Currently all deployments share the same driver name.
		EnsureOperatorRemoved(c, o)
	}

	// At the moment, the only supported deployment method is via test/start-operator.sh.
	cmd := exec.Command("test/start-operator.sh")
	cmd.Dir = os.Getenv("REPO_ROOT")
	cmd.Env = append(os.Environ(), "TEST_OPERATOR_NAMESPACE="+o.Namespace)
	cmd.Stdout = ginkgo.GinkgoWriter
	cmd.Stderr = ginkgo.GinkgoWriter
	err = cmd.Run()

	framework.ExpectNoError(err, "create operator deployment: %q", o.Name)

	WaitForOperator(c, o)
}

var tests = map[string]func(o *Operator, f *framework.Framework){}

// DescribeForAll registers tests like gomega.Describe does.
// The idea is borrowed from test/e2e/deploy/deploy.go
//
// This Descirbe + Define call comibnation might not required for now
// but the intention here is to support any future usecases with minum changes.
// Say running all defined operator tests for different environments like
// 'testing' and 'production'.
func DescribeForAll(what string, f func(o *Operator, f *framework.Framework)) bool {
	tests[what] = f

	return true
}

// DefineTests must be called to register all tests defined so far via Describe.
func DefineTests() {
	for name, testFunc := range tests {
		ginkgo.Describe(name, func() {
			var c *deploy.Cluster
			var f *framework.Framework

			o := newOperator("pmem-csi-operator", "")

			// We have to call this before creating new framework object
			// So that we get calls first before f.AfterEach(), where the
			// namespace auto created by the framework gets deleted
			ginkgo.AfterEach(func() {
				ginkgo.By(fmt.Sprintf("tearing down operator for test %q", ginkgo.CurrentGinkgoTestDescription().FullTestText))
				EnsureOperatorRemoved(c, o)
			})

			f = framework.NewDefaultFramework("cluster")

			ginkgo.BeforeEach(func() {
				var err error
				ginkgo.By(fmt.Sprintf("preparing operator for test %q", ginkgo.CurrentGinkgoTestDescription().FullTestText))

				// Use the namespace created by the framework
				o.Namespace = f.Namespace.Name

				c, err = deploy.NewCluster(f.ClientSet)
				gomega.Expect(err).ShouldNot(gomega.HaveOccurred(), "get cluster information")
				EnsureOperatorDeployed(c, o)

				ctx := context.Background()
				to := podlogs.LogOutput{
					StatusWriter: ginkgo.GinkgoWriter,
					LogWriter:    ginkgo.GinkgoWriter,
				}
				podlogs.CopyAllLogs(ctx, f.ClientSet, o.Namespace, to)
				//podlogs.WatchPods(ctx, f.ClientSet, operator.Namespace, ginkgo.GinkgoWriter)
			})

			testFunc(o, f)
		})
	}
}
