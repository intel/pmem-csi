/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package validate contains code to check objects deployed by the operator
// as part of an E2E test.
package validate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/deployments"
	operatordeployment "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/metrics"
	"github.com/intel/pmem-csi/pkg/version"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	apierrs "k8s.io/apimachinery/pkg/api/errors"

	cm "github.com/prometheus/client_model/go"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DriverDeployment compares all objects as deployed by the operator against the expected
// objects for a certain deployment spec. deploymentSpec should only have those fields
// set which are not the defaults. This call will wait for the expected objects until
// the context times out.
func DriverDeploymentEventually(ctx context.Context, c *deploy.Cluster, client client.Client, k8sver version.Version, metricsURL, namespace string, deployment api.PmemCSIDeployment, lastCount float64) (float64, error) {
	if deployment.GetUID() == "" {
		return 0, errors.New("deployment not an object that was stored in the API server, no UID")
	}
	endCount, err := WaitForDeploymentReconciled(ctx, c, metricsURL, deployment, lastCount)
	if err != nil {
		return 0, err
	}

	// As the reconcile is done, check if the deployed driver is valid ...
	if err := DriverDeployment(ctx, client, k8sver, namespace, deployment); err != nil {
		return 0, err
	}
	return endCount, nil
}

// WaitForDeploymentReconciled waits and checks till the context timedout
// that if given deployment got reconciled by the operator.
// It checks in the operator metrics for a new 'pmem_csi_deployment_reconcile'
// metric count is greater that the lastCount. If found it returns the new reconcile count.
func WaitForDeploymentReconciled(ctx context.Context, c *deploy.Cluster, metricsURL string, deployment api.PmemCSIDeployment, lastCount float64) (float64, error) {
	deploymentMap := map[string]string{
		"name": deployment.Name,
		"uid":  string(deployment.UID),
	}

	lblPairToMap := func(lbls []*cm.LabelPair) map[string]string {
		res := map[string]string{}
		for _, lbl := range lbls {
			res[lbl.GetName()] = lbl.GetValue()
		}
		return res
	}

	ready := func() (float64, error) {
		name := "pmem_csi_deployment_reconcile"
		mf, err := deploy.GetMetrics(ctx, c, metricsURL)
		if err != nil {
			return 0, err
		}
		metric, ok := mf[name]
		if !ok {
			return 0, fmt.Errorf("expected '%s' metric not found:\n %v", name, mf)
		}

		for _, m := range metric.GetMetric() {
			if c := m.Counter.GetValue(); c > lastCount {
				if reflect.DeepEqual(deploymentMap, lblPairToMap(m.GetLabel())) {
					return c, nil
				}
			}
		}
		return 0, fmt.Errorf("'%s' metric not found with with count higher than %f", name, lastCount)
	}

	if endCount, err := ready(); err == nil {
		return endCount, nil
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		select {
		case <-ticker.C:
			endCount, err := ready()
			if err == nil {
				return endCount, nil
			}
			lastErr = err
		case <-ctx.Done():
			return 0, fmt.Errorf("timed out waiting for deployment metric, last error: %v", lastErr)
		}
	}
}

// CheckForObjectUpdates wait and checks for deployed driver components
// in consistent state. That means no unnecessary updates occurred and all
// expected object updates passed in expectedUpdates have occurred.
// It uses the 'pmem_csi_deployment_sub_resource_updated_at' metric.
//
// Beware that this check only works if a test calling it uses a new
// deployment with a unique UID. Otherwise object updates from a
// previous test may get picked up.
//
// When the CR just got created, the operator should
// immediately create objects with the right content and then
// not update them again unless it is an expected update from the
// caller.
func CheckForObjectUpdates(ctx context.Context, c *deploy.Cluster, metricsURL string, expectedUpdates []client.Object, deployment api.PmemCSIDeployment) error {
	type updateInfo struct {
		expectedObjectLabels []map[string]string
		isFound              []bool
	}

	info := updateInfo{}
	for _, o := range expectedUpdates {
		info.expectedObjectLabels = append(info.expectedObjectLabels, metrics.GetSubResourceLabels(o))
		info.isFound = append(info.isFound, false)
	}

	// isUpdateExpected check if the given object labels match with the
	// expectedObjectLabels,
	isUpdateExpected := func(labels map[string]string) bool {
		for i, ol := range info.expectedObjectLabels {
			if reflect.DeepEqual(ol, labels) {
				info.isFound[i] = true
				return true
			}
		}
		return false
	}

	checkObjectUpdates := func() error {
		mf, err := deploy.GetMetrics(ctx, c, metricsURL)
		if err != nil {
			return err
		}
		updates, ok := mf["pmem_csi_deployment_sub_resource_updated_at"]
		if !ok {
			return nil
		}

		unExpectedList := []string{}
		for _, m := range updates.GetMetric() {
			lblMap := labelPairToMap(m.GetLabel())
			if strings.Contains(lblMap["ownedBy"], string(deployment.UID)) && !isUpdateExpected(lblMap) {
				unExpectedList = append(unExpectedList, fmt.Sprintf("%+v", lblMap))
			}
		}

		if len(unExpectedList) != 0 {
			return fmt.Errorf("unexpected sub-object updates by the operator:\n%s", strings.Join(unExpectedList, "\n"))
		}
		return nil
	}

	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer cancel()
	for {
		select {
		case <-ticker.C:
			// check if any updates recorded after last end of reconcile.
			if err := checkObjectUpdates(); err != nil {
				return err
			}
		case <-deadline.Done():
			strList := []string{}
			for i, l := range info.expectedObjectLabels {
				if !info.isFound[i] {
					strList = append(strList, fmt.Sprintf("%+v", l))
				}
			}
			if len(strList) != 0 {
				return fmt.Errorf("expected sub-object were not updated by the operator: %s", strings.Join(strList, "\n"))
			}
			return nil
		}
	}
}

// DriverDeployment compares all objects as deployed by the operator against the expected
// objects for a certain deployment spec. deploymentSpec should only have those fields
// set which are not the defaults. The caller must ensure that the operator is done
// with creating objects.
//
// A final error is returned when observing a problem that is not going to go away,
// like an unexpected update of an object.
func DriverDeployment(ctx context.Context, c client.Client, k8sver version.Version, namespace string, deployment api.PmemCSIDeployment) error {
	if deployment.GetUID() == "" {
		return errors.New("deployment not an object that was stored in the API server, no UID")
	}

	// The operator currently always uses the production image. We
	// can only find that indirectly.
	driverImage := strings.Replace(os.Getenv("PMEM_CSI_IMAGE"), "-test", "", 1)
	if err := (&deployment).EnsureDefaults(driverImage); err != nil {
		return err
	}

	// Validate sub-objects. A sub-object is anything that has the deployment object as owner.
	objects, err := listAllDeployedObjects(ctx, c, deployment, namespace)
	if err != nil {
		return err
	}

	// Load secret if it exists. If it doesn't, we validate without it.
	var controllerCABundle []byte
	if deployment.Spec.ControllerTLSSecret != "" {
		secret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Secret",
			},
		}
		objKey := client.ObjectKey{
			Namespace: namespace,
			Name:      deployment.Spec.ControllerTLSSecret,
		}
		if err := c.Get(ctx, objKey, secret); err != nil {
			if !apierrs.IsNotFound(err) {
				return err
			}
		} else {
			if ca, ok := secret.Data[api.TLSSecretCA]; ok {
				controllerCABundle = ca
			}
		}
	}

	expectedObjects, err := deployments.LoadAndCustomizeObjects(k8sver, deployment.Spec.DeviceMode, namespace, deployment, controllerCABundle)
	if err != nil {
		return fmt.Errorf("customize expected objects: %v", err)
	}

	var diffs []string
	for _, actual := range objects {
		expected := findObject(expectedObjects, actual)
		if expected == nil {
			diffs = append(diffs, fmt.Sprintf("unexpected object was deployed: %s", prettyPrintObjectID(actual)))
			continue
		}
		// Of the meta data, we have already compared type,
		// name and namespace. In addition to that we only
		// care about labels.
		expectedLabels := expected.GetLabels()
		actualLabels := actual.GetLabels()
		for key, value := range expectedLabels {
			if key == "pmem-csi.intel.com/deployment" &&
				(deployment.Labels == nil || deployment.Labels["pmem-csi.intel.com/deployment"] == "") {
				// This particular label is part of
				// the reference YAMLs but not
				// currently added by the operator
				// unless specifically requested.
				// Ignore it...
				continue
			}

			actualValue, found := actualLabels[key]
			if !found {
				diffs = append(diffs, fmt.Sprintf("label %s missing for %s", key, prettyPrintObjectID(*expected)))
			} else if actualValue != value {
				diffs = append(diffs, fmt.Sprintf("label %s of %s is wrong: expected %q, got %q", key, prettyPrintObjectID(*expected), value, actualValue))
			}
		}

		// Certain top-level fields must be identical.
		fields := map[string]bool{}
		for field := range expected.Object {
			fields[field] = true
		}
		for field := range actual.Object {
			fields[field] = true
		}
		for field := range fields {
			switch field {
			case "metadata", "status":
				// Verified above or may vary.
				continue
			}
			expectedField := expected.Object[field]
			actualField := actual.Object[field]
			diff := compare(expected.GetKind(), field, expectedField, actualField)
			if diff != nil {
				diffs = append(diffs, fmt.Sprintf("%s content for %s does not match:\n   %s", field, prettyPrintObjectID(*expected), strings.Join(diff, "\n   ")))
			}
		}
	}
	for _, expected := range expectedObjects {
		if findObject(objects, expected) == nil {
			diffs = append(diffs, fmt.Sprintf("expected object was not deployed: %v", prettyPrintObjectID(expected)))
		}
	}
	if diffs != nil {
		return fmt.Errorf("deployed driver different from expected deployment:\n%s", strings.Join(diffs, "\n"))
	}
	return nil
}

// When we get an object back from the apiserver, some fields get populated with generated
// or fixed default values. defaultSpecValues contains a hierarchy of maps that stores those
// defaults:
//    Kind -> field -> field -> ... -> value
//
// Those defaults are used when the original object didn't have a field value.
// "ignore" is a special value which let's the comparison skip the field.
var defaultValues = parseDefaultValues()

func parseDefaultValues() map[string]interface{} {
	var defaults map[string]interface{}
	defaultsApps := `
  spec:
    revisionHistoryLimit: 10
    podManagementPolicy: OrderedReady
    selector:
      matchLabels:
        pmem-csi.intel.com/deployment: ignore # labels are tested separately
    template:
      metadata:
        labels:
          pmem-csi.intel.com/deployment: ignore # labels are tested separately
      spec:
        dnsPolicy: ClusterFirst
        restartPolicy: Always
        serviceAccount: ignore # redundant field, always returned by apiserver in addition to serviceAccountName
        schedulerName: default-scheduler
        terminationGracePeriodSeconds: 30
        containers:
          terminationMessagePath: /dev/termination-log
          terminationMessagePolicy: File
          imagePullPolicy: IfNotPresent
          ports:
            protocol: TCP
          env:
            valueFrom:
              fieldRef:
                apiVersion: v1
        volumes:
          secret:
            defaultMode: 420`

	defaultsYAML := `
Service:
  spec:
    clusterIP: ignore
    clusterIPs: ignore # since k8s v1.20
    externalTrafficPolicy: Cluster
    ipFamilies:
    - IPv4
    ipFamilyPolicy: SingleStack
    ports:
      protocol: TCP
      nodePort: ignore
    selector:
      pmem-csi.intel.com/deployment: ignore # labels are tested separately
    sessionAffinity: None
    type: ClusterIP
    internalTrafficPolicy: Cluster
ServiceAccount:
  secrets: ignore
  imagePullSecrets: ignore # injected on OpenShift
DaemonSet:` + defaultsApps + `
    updateStrategy: ignore
Deployment:` + defaultsApps + `
    progressDeadlineSeconds: ignore
    strategy: ignore
StatefulSet:` + defaultsApps + `
    updateStrategy: ignore
CSIDriver:
  spec:
    storageCapacity: false
    fsGroupPolicy: ignore # currently PMEM-CSI driver does not support fsGroupPolicy
    requiresRepublish: false
MutatingWebhookConfiguration:
  webhooks:
    clientConfig:
      caBundle: ignore # Can change, in particular when generated by OpenShift.
      service:
        port: 443
    admissionReviewVersions:
    - v1beta1
    matchPolicy: Equivalent # default policy in v1
    reinvocationPolicy: Never
    rules:
      scope: "*"
    sideEffects: Unknown
    timeoutSeconds: 10 # default timeout in v1
`

	err := yaml.UnmarshalStrict([]byte(defaultsYAML), &defaults)
	if err != nil {
		panic(err)
	}
	return defaults
}

// compare is like reflect.DeepEqual, except that it reports back all changes
// in a diff-like format, with one entry per added, removed or different field.
// In addition, it fills in default values in the expected spec if they are missing
// before comparing against the actual value. This makes it possible to
// compare the on-disk YAML files which are typically not complete against
// objects from the apiserver which have all defaults filled in.
func compare(kind string, field string, expected, actual interface{}) []string {
	defaults := defaultValues[kind]
	if defaultsMap, ok := defaults.(map[interface{}]interface{}); ok {
		defaults = defaultsMap[field]
	}
	return compareSpecRecursive(field, defaults, expected, actual)
}

func compareSpecRecursive(path string, defaults, expected, actual interface{}) (diffs []string) {
	if expected == nil && actual == nil {
		return nil
	}

	// Some paths do not need to be compared.
	if ignore, ok := defaults.(string); ok && ignore == "ignore" {
		return nil
	}

	// Inject defaults?
	if actual != nil && expected == nil {
		expected = defaults
	}

	// Missing value.
	if actual == nil && expected != nil {
		return []string{fmt.Sprintf("- %s = %v", path, expected)}
	}

	// Extra value.
	if actual != nil && expected == nil {
		// We cannot express an empty map in the YAML defaults, but do get
		// it from the apiserver for fields like securityContext or emptyDir.
		// Can be ignored.
		if value, ok := actual.(map[string]interface{}); ok && len(value) == 0 {
			return nil
		}
		return []string{fmt.Sprintf("+ %s = %v", path, actual)}
	}

	// Different types?
	expectedValue := reflect.ValueOf(expected)
	actualValue := reflect.ValueOf(actual)
	if expectedValue.Kind() != actualValue.Kind() {
		// This might just be a int vs. int64 mismatch, which
		// can happen because decoding doesn't know the size.
		if (expectedValue.Kind() == reflect.Int && actualValue.Kind() == reflect.Int64 ||
			expectedValue.Kind() == reflect.Int64 && actualValue.Kind() == reflect.Int) &&
			expectedValue.Int() == actualValue.Int() {
			return nil
		}
		return []string{fmt.Sprintf("! %s mismatched type, expected %v (%T), got %v (%T)", path, expected, expected, actual, actual)}
	}

	// For lists and maps we need to recurse, everything else
	// can be compare as-is.
	switch expectedValue.Kind() {
	case reflect.Map:
		expectedMap := toMap(expected)
		actualMap := toMap(actual)
		defaultsMap := map[string]interface{}{}
		if defaults != nil {
			defaultsMap = toMap(defaults)
		}
		// Gather and sort all keys before iterating over them to make
		// the result deterministic.
		keys := map[string]bool{}
		for key := range actualMap {
			keys[key] = true
		}
		for key := range expectedMap {
			keys[key] = true
		}
		var sortedKeys []string
		for key := range keys {
			sortedKeys = append(sortedKeys, key)
		}
		sort.Strings(sortedKeys)
		for _, key := range sortedKeys {
			diffs = append(diffs, compareSpecRecursive(path+"."+key, defaultsMap[key], expectedMap[key], actualMap[key])...)
		}
	case reflect.Slice:
		// The order in lists is expected to match. The defaults are the same for all entries.
		expectedList := expected.([]interface{})
		actualList := actual.([]interface{})
		i := 0
		for ; i < len(expectedList) || i < len(actualList); i++ {
			var expectedEntry, actualEntry interface{}
			if i < len(expectedList) {
				expectedEntry = expectedList[i]
			}
			if i < len(actualList) {
				actualEntry = actualList[i]
			}
			diffs = append(diffs, compareSpecRecursive(fmt.Sprintf("%s.#%d", path, i), defaults, expectedEntry, actualEntry)...)
		}
	default:
		if !reflect.DeepEqual(expected, actual) {
			return []string{fmt.Sprintf("! %s expected %v, got %v", path, expected, actual)}
		}
	}
	return
}

func toMap(value interface{}) map[string]interface{} {
	switch m := value.(type) {
	case map[string]interface{}:
		return m
	case map[interface{}]interface{}:
		// We get this after decoding YAML.
		m2 := map[string]interface{}{}
		for key, value := range m {
			m2[fmt.Sprintf("%v", key)] = value
		}
		return m2
	default:
		panic(fmt.Errorf("unexpected map type %T for %v", value, value))
	}
}

func findObject(hay []unstructured.Unstructured, needle unstructured.Unstructured) *unstructured.Unstructured {
	gvk := needle.GetObjectKind().GroupVersionKind()
	name := needle.GetName()
	namespace := needle.GetNamespace()
	for _, obj := range hay {
		if obj.GetObjectKind().GroupVersionKind() == gvk &&
			obj.GetName() == name &&
			obj.GetNamespace() == namespace {
			return &obj
		}
	}
	return nil
}

func prettyPrintObjectID(object unstructured.Unstructured) string {
	return fmt.Sprintf("%q of type %q in namespace %q",
		object.GetName(),
		object.GetObjectKind().GroupVersionKind(),
		object.GetNamespace())
}

func listAllDeployedObjects(ctx context.Context, c client.Client, deployment api.PmemCSIDeployment, namespace string) ([]unstructured.Unstructured, error) {
	objects := []unstructured.Unstructured{}

	for _, list := range operatordeployment.AllObjectLists() {
		opts := &client.ListOptions{
			Namespace: namespace,
		}
		// Test client does not support differentiating cluster-scoped objects
		// and the query fails when fetch those object by setting the namespace-
		switch list.GetKind() {
		case "CSIDriverList", "ClusterRoleList", "ClusterRoleBindingList", "MutatingWebhookConfigurationList":
			opts = &client.ListOptions{}
		}
		// Filtering by owner doesn't work, so we have to use brute-force and look at all
		// objects.
		if err := c.List(ctx, list, opts); err != nil {
			return objects, fmt.Errorf("list %s: %v", list.GetObjectKind(), err)
		}
	outer:
		for _, object := range list.Items {
			owners := object.GetOwnerReferences()
			for _, owner := range owners {
				if owner.UID == deployment.UID {
					objects = append(objects, object)
					continue outer
				}
			}
		}
	}
	return objects, nil
}

func labelPairToMap(pairs []*cm.LabelPair) map[string]string {
	labels := map[string]string{}
	for _, lbl := range pairs {
		labels[lbl.GetName()] = lbl.GetValue()
	}

	return labels
}
