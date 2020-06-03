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

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/deployments"
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
func DriverDeploymentEventually(ctx context.Context, client client.Client, k8sver version.Version, namespace string, deployment api.Deployment) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	if deployment.GetUID() == "" {
		return errors.New("deployment not an object that was stored in the API server, no UID")
	}

	// TODO: once there is a better way to detect that the
	// operator has finished updating the deployment, check for
	// that here instead of repeatedly checking the objects.
	// As it stands now, permanent differences will only be
	// reported when the test times out.
	ready := func() (err error) {
		return DriverDeployment(client, k8sver, namespace, deployment)
	}
	if err := ready(); err != nil {
		for {
			select {
			case <-ticker.C:
				err = ready()
				if err == nil {
					return nil
				}
			case <-ctx.Done():
				return fmt.Errorf("timed out waiting for deployment, last error: %v", err)
			}
		}
	}
	return nil
}

// DriverDeployment compares all objects as deployed by the operator against the expected
// objects for a certain deployment spec. deploymentSpec should only have those fields
// set which are not the defaults. The caller must ensure that the operator is done
// with creating objects.
func DriverDeployment(client client.Client, k8sver version.Version, namespace string, deployment api.Deployment) error {
	if deployment.GetUID() == "" {
		return errors.New("deployment not an object that was stored in the API server, no UID")
	}

	// The operator currently always uses the production image. We
	// can only find that indirectly.
	driverImage := strings.Replace(os.Getenv("PMEM_CSI_IMAGE"), "-test", "", 1)
	(&deployment).EnsureDefaults(driverImage)

	// Validate sub-objects. A sub-object is anything that has the deployment object as owner.
	objects, err := listAllDeployedObjects(client, deployment)
	if err != nil {
		return err
	}

	expectedObjects, err := deployments.LoadAndCustomizeObjects(k8sver, deployment.Spec.DeviceMode, namespace, deployment)
	if err != nil {
		return fmt.Errorf("customize expected objects: %v", err)
	}

	var diffs []string
	for _, actual := range objects {
		expected := findObject(expectedObjects, actual)
		if expected == nil {
			if actual.GetKind() == "Secret" {
				// Custom comparison against expected
				// content of secrets, which aren't
				// part of the reference objects.
				switch actual.GetName() {
				case deployment.GetHyphenedName() + "-registry-secrets":
					diffs = append(diffs, compareSecrets(actual,
						deployment.Spec.CACert,
						deployment.Spec.RegistryPrivateKey,
						deployment.Spec.RegistryCert)...)
					continue
				case deployment.GetHyphenedName() + "-node-secrets":
					diffs = append(diffs, compareSecrets(actual,
						deployment.Spec.CACert,
						deployment.Spec.NodeControllerPrivateKey,
						deployment.Spec.NodeControllerCert)...)
					continue
				}
			}
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

		// The spec needs to be identical.
		expectedSpec := expected.Object["spec"]
		actualSpec := actual.Object["spec"]
		specDiff := compareSpec(expected.GetKind(), expectedSpec, actualSpec)
		if specDiff != nil {
			diffs = append(diffs, fmt.Sprintf("spec content for %s does not match:\n   %s", prettyPrintObjectID(*expected), strings.Join(specDiff, "\n   ")))
		}
	}
	gvk := schema.GroupVersionKind{
		Kind:    "Secret",
		Version: "v1",
	}
	for _, expected := range append(expectedObjects,
		// Content doesn't matter, we just want to be sure they exist.
		createObject(gvk, deployment.GetHyphenedName()+"-registry-secrets", namespace),
		createObject(gvk, deployment.GetHyphenedName()+"-node-secrets", namespace)) {
		if findObject(objects, expected) == nil {
			diffs = append(diffs, fmt.Sprintf("expected object was not deployed: %v", prettyPrintObjectID(expected)))
		}
	}
	if diffs != nil {
		return fmt.Errorf("deployed driver different from expected deployment:\n%s", strings.Join(diffs, "\n"))
	}

	return nil
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
var defaultSpecValues = parseDefaultSpecValues()

func parseDefaultSpecValues() map[string]interface{} {
	var defaults map[string]interface{}
	defaultsApps := `
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
      initContainers:
        terminationMessagePath: /dev/termination-log
        terminationMessagePolicy: File
        imagePullPolicy: IfNotPresent
      volumes:
        secret:
          defaultMode: 420`

	defaultsYAML := `
Service:
  clusterIP: ignore
  externalTrafficPolicy: Cluster
  ports:
    protocol: TCP
    nodePort: ignore
  selector:
    pmem-csi.intel.com/deployment: ignore # labels are tested separately
  sessionAffinity: None
  type: ClusterIP
DaemonSet:` + defaultsApps + `
  updateStrategy: ignore
StatefulSet:` + defaultsApps + `
  updateStrategy: ignore
`

	err := yaml.UnmarshalStrict([]byte(defaultsYAML), &defaults)
	if err != nil {
		panic(err)
	}
	return defaults
}

// compareSpec is like reflect.DeepEqual, except that it reports back all changes
// in a diff-like format, with one entry per added, removed or different field.
// In addition, it fills in default values in the expected spec if they are missing
// before comparing against the actual value. This makes it possible to
// compare the on-disk YAML files which are typically not complete against
// objects frome the apiserver which have all defaults filled in.
func compareSpec(kind string, expected, actual interface{}) []string {
	defaults := defaultSpecValues[kind]
	path := "spec"
	return compareSpecRecursive(path, defaults, expected, actual)
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
		for key, _ := range actualMap {
			keys[key] = true
		}
		for key, _ := range expectedMap {
			keys[key] = true
		}
		var sortedKeys []string
		for key, _ := range keys {
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

// A list of all object types potentially created by the
// operator. It's okay and desirable to list more than actually used
// at the moment, to catch new objects.
var allObjectTypes = []schema.GroupVersionKind{
	schema.GroupVersionKind{"", "v1", "SecretList"},
	schema.GroupVersionKind{"", "v1", "ServiceList"},
	schema.GroupVersionKind{"", "v1", "ServiceAccountList"},
	schema.GroupVersionKind{"admissionregistration.k8s.io", "v1beta1", "MutatingWebhookConfigurationList"},
	schema.GroupVersionKind{"apps", "v1", "DaemonSetList"},
	schema.GroupVersionKind{"apps", "v1", "DeploymentList"},
	schema.GroupVersionKind{"apps", "v1", "ReplicaSetList"},
	schema.GroupVersionKind{"apps", "v1", "StatefulSetList"},
	schema.GroupVersionKind{"rbac.authorization.k8s.io", "v1", "ClusterRoleList"},
	schema.GroupVersionKind{"rbac.authorization.k8s.io", "v1", "ClusterRoleBindingList"},
	schema.GroupVersionKind{"rbac.authorization.k8s.io", "v1", "RoleList"},
	schema.GroupVersionKind{"rbac.authorization.k8s.io", "v1", "RoleBindingList"},
	schema.GroupVersionKind{"storage.k8s.io", "v1beta1", "CSIDriverList"},
}

func listAllDeployedObjects(client client.Client, deployment api.Deployment) ([]unstructured.Unstructured, error) {
	objects := []unstructured.Unstructured{}

	for _, gvk := range allObjectTypes {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)
		// Filtering by owner doesn't work, so we have to use brute-force and look at all
		// objects.
		// TODO (?): filter at least by namespace, where applicable.
		if err := client.List(context.Background(), list); err != nil {
			return objects, fmt.Errorf("list %s: %v", gvk, err)
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
