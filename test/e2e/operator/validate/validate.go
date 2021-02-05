/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package validate contains code to check objects deployed by the operator
// as part of an E2E test.
package validate

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
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
	"github.com/intel/pmem-csi/pkg/version"

	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DriverDeployment compares all objects as deployed by the operator against the expected
// objects for a certain deployment spec. deploymentSpec should only have those fields
// set which are not the defaults. This call will wait for the expected objects until
// the context times out.
func DriverDeploymentEventually(ctx context.Context, client client.Client, k8sver version.Version, namespace string, deployment api.PmemCSIDeployment, initialCreation bool) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	if deployment.GetUID() == "" {
		return errors.New("deployment not an object that was stored in the API server, no UID")
	}

	// Track resource versions to detect modifications?
	var resourceVersions map[string]string
	if initialCreation {
		resourceVersions = map[string]string{}
	}

	// TODO: once there is a better way to detect that the
	// operator has finished updating the deployment, check for
	// that here instead of repeatedly checking the objects.
	// As it stands now, permanent differences will only be
	// reported when the test times out.
	ready := func() (final bool, err error) {
		return DriverDeployment(client, k8sver, namespace, deployment, resourceVersions)
	}
	if final, err := ready(); err != nil {
		if final {
			return err
		}
	loop:
		for {
			select {
			case <-ticker.C:
				final, err = ready()
				if err == nil {
					break loop
				}
				if final {
					return err
				}
			case <-ctx.Done():
				return fmt.Errorf("timed out waiting for deployment, last error: %v", err)
			}
		}
	}

	// Now wait a bit longer to see whether the objects change again - they shouldn't.
	// The longer we wait, the more certainty we have that we have reached a stable state.
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		select {
		case <-ticker.C:
			if _, err := ready(); err != nil {
				return fmt.Errorf("objects were changed after reaching expected state: %v", err)
			}
		case <-deadline.Done():
			return nil
		}
	}
}

// DriverDeployment compares all objects as deployed by the operator against the expected
// objects for a certain deployment spec. deploymentSpec should only have those fields
// set which are not the defaults. The caller must ensure that the operator is done
// with creating objects.
//
// resourceVersions is used to track which resource versions were encountered
// for generated objects. If not nil, the version must not change (i.e. the operator
// must not update the objects after creating them).
//
// A final error is returned when observing a problem that is not going to go away,
// like an unexpected update of an object.
func DriverDeployment(client client.Client, k8sver version.Version, namespace string, deployment api.PmemCSIDeployment, resourceVersions map[string]string) (final bool, finalErr error) {
	if deployment.GetUID() == "" {
		return true, errors.New("deployment not an object that was stored in the API server, no UID")
	}

	// The operator currently always uses the production image. We
	// can only find that indirectly.
	driverImage := strings.Replace(os.Getenv("PMEM_CSI_IMAGE"), "-test", "", 1)
	(&deployment).EnsureDefaults(driverImage)

	// Validate sub-objects. A sub-object is anything that has the deployment object as owner.
	objects, err := listAllDeployedObjects(client, deployment, namespace)
	if err != nil {
		return false, err
	}

	expectedObjects, err := deployments.LoadAndCustomizeObjects(k8sver, deployment.Spec.DeviceMode, namespace, deployment)
	if err != nil {
		return true, fmt.Errorf("customize expected objects: %v", err)
	}

	var diffs []string
	for _, actual := range objects {
		// When the CR just got created, the operator should
		// immediately create objects with the right content and then
		// not update them again.
		if resourceVersions != nil {
			uid := string(actual.GetUID())
			currentResourceVersion := actual.GetResourceVersion()
			oldResourceVersion, ok := resourceVersions[uid]
			if ok {
				if oldResourceVersion != currentResourceVersion {
					diffs = append(diffs, fmt.Sprintf("object was modified unnecessarily: %s", prettyPrintObjectID(actual)))
					final = true
				}
			} else {
				resourceVersions[uid] = currentResourceVersion
			}
		}
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
		finalErr = fmt.Errorf("deployed driver different from expected deployment:\n%s", strings.Join(diffs, "\n"))
	}
	return
}

func createObject(gvk schema.GroupVersionKind, name, namespace string) unstructured.Unstructured {
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)

	return obj
}

func compareSecrets(actual unstructured.Unstructured, ca, key, crt []byte) (diffs []string) {
	data := actual.Object["data"]
	if data == nil {
		return []string{fmt.Sprintf("%s: no data in secret", actual.GetName())}
	}
	fields := data.(map[string]interface{})
	diffs = append(diffs, compareSecretField(actual.GetName(), fields, "ca.crt", ca)...)
	diffs = append(diffs, compareSecretField(actual.GetName(), fields, "tls.key", key)...)
	diffs = append(diffs, compareSecretField(actual.GetName(), fields, "tls.crt", crt)...)
	return
}

func compareSecretField(name string, fields map[string]interface{}, fieldName string, expectedData []byte) []string {
	field := fields[fieldName]
	if field == nil {
		return []string{fmt.Sprintf("secret %s does not contain data field %s", name, fieldName)}
	}
	actualData, err := base64.StdEncoding.DecodeString(field.(string))
	if err != nil {
		return []string{fmt.Sprintf("decoding secret %s field %s: %v", name, fieldName, err)}
	}
	if expectedData == nil {
		// We only know that there should be some data, but not what it should be.
		if len(actualData) == 0 {
			return []string{fmt.Sprintf("secret %s contains empty data field %s", name, fieldName)}
		}
	} else {
		if bytes.Compare(actualData, expectedData) != 0 {
			return []string{fmt.Sprintf("secret %s, field %s: data mismatch: got %s, expected %s",
				name, fieldName, summarizeData(actualData), summarizeData(expectedData))}
		}
	}
	return nil
}

func summarizeData(data []byte) string {
	return fmt.Sprintf("len %d, hash %x", len(data), md5.Sum(data))
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
    ports:
      protocol: TCP
      nodePort: ignore
    selector:
      pmem-csi.intel.com/deployment: ignore # labels are tested separately
    sessionAffinity: None
    type: ClusterIP
ServiceAccount:
  secrets: ignore
DaemonSet:` + defaultsApps + `
    updateStrategy: ignore
StatefulSet:` + defaultsApps + `
    updateStrategy: ignore
CSIDriver:
  spec:
    storageCapacity: false
    fsGroupPolicy: ignore # currently PMEM-CSI driver does not support fsGroupPolicy
MutatingWebhookConfiguration:
  webhooks:
    clientConfig:
      caBundle: ignore # content varies, correctness is validated during E2E testing
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

func listAllDeployedObjects(c client.Client, deployment api.PmemCSIDeployment, namespace string) ([]unstructured.Unstructured, error) {
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
		if err := c.List(context.Background(), list, opts); err != nil {
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
