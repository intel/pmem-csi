module github.com/intel/pmem-csi

go 1.13

require (
	github.com/Microsoft/go-winio v0.4.14 // indirect
	github.com/container-storage-interface/spec v1.2.0
	github.com/gogo/protobuf v1.3.1 // indirect
	github.com/golang/groupcache v0.0.0-20191027212112-611e8accdfc9 // indirect
	github.com/golang/protobuf v1.3.2
	github.com/google/go-cmp v0.3.1 // indirect
	github.com/google/uuid v1.1.1
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/json-iterator/go v1.1.8-0.20191012130704-03217c3e9766 // indirect
	github.com/kubernetes-csi/csi-lib-utils v0.6.1
	github.com/kubernetes-csi/csi-test v1.1.2-0.20191016154743-6931aedb3df0
	github.com/onsi/ginkgo v1.10.2
	github.com/onsi/gomega v1.7.0
	github.com/operator-framework/operator-sdk v0.12.1-0.20191107022206-36b6de4c479e
	github.com/pkg/errors v0.8.1
	github.com/stretchr/testify v1.4.0
	go.uber.org/multierr v1.2.0 // indirect
	go.uber.org/zap v1.11.0 // indirect
	golang.org/x/crypto v0.0.0-20191011191535-87dc89f01550 // indirect
	golang.org/x/net v0.0.0-20191027233614-53de4c7853b5
	golang.org/x/sys v0.0.0-20191105231009-c1f44814a5cd
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0 // indirect
	golang.org/x/xerrors v0.0.0-20191011141410-1b5146add898 // indirect
	google.golang.org/appengine v1.6.5 // indirect
	google.golang.org/genproto v0.0.0-20191009194640-548a555dbc03 // indirect
	google.golang.org/grpc v1.24.0
	gopkg.in/freddierice/go-losetup.v1 v1.0.0-20170407175016-fc9adea44124
	gopkg.in/square/go-jose.v2 v2.4.0 // indirect
	gopkg.in/yaml.v2 v2.2.7 // indirect
	k8s.io/api v0.0.0
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/component-base v0.0.0
	k8s.io/klog v1.0.0
	k8s.io/kubernetes v1.16.3
	k8s.io/utils v0.0.0-20191114200735-6ca3b61696b6
	sigs.k8s.io/controller-runtime v0.4.0
)

replace (
	k8s.io/api => k8s.io/api v0.0.0-20191016110408-35e52d86657a
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20191016113550-5357c4baaf65
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20191004115801-a2eda9f80ab8
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20191016112112-5190913f932d
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.0.0-20191016114015-74ad18325ed5
	k8s.io/client-go => k8s.io/client-go v0.0.0-20191016111102-bec269661e48
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.0.0-20191016115326-20453efc2458
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.0.0-20191016115129-c07a134afb42
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20191004115455-8e001e5d1894
	k8s.io/component-base => k8s.io/component-base v0.0.0-20191016111319-039242c015a9
	k8s.io/cri-api => k8s.io/cri-api v0.0.0-20190828162817-608eb1dad4ac
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.0.0-20191016115521-756ffa5af0bd
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20191016112429-9587704a8ad4
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.0.0-20191016114939-2b2b218dc1df
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.0.0-20191016114407-2e83b6f20229
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.0.0-20191016114748-65049c67a58b
	k8s.io/kubectl => k8s.io/kubectl v0.0.0-20191016120415-2ed914427d51
	k8s.io/kubelet => k8s.io/kubelet v0.0.0-20191016114556-7841ed97f1b2
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.0.0-20191016115753-cf0698c3a16b
	k8s.io/metrics => k8s.io/metrics v0.0.0-20191016113814-3b1a734dba6e
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.0.0-20191016112829-06bb3c9d77c9
)

//  Operator:-  To resolve below ambigutiy, pin Azure/go-autorest/autorest to v13.3.0
// 	github.com/Azure/go-autorest/autorest: ambiguous import: found github.com/Azure/go-autorest/autorest in multiple modules:
//	github.com/Azure/go-autorest v11.1.0+incompatible (/home/avalluri/work/go/pkg/mod/github.com/!azure/go-autorest@v11.1.0+incompatible/autorest)
//	github.com/Azure/go-autorest/autorest v0.9.0 (/home/avalluri/work/go/pkg/mod/github.com/!azure/go-autorest/autorest@v0.9.0)
replace github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.0+incompatible

// Pin client_golang version to that compatible with component-base@kubernetes-v1.16.2
// We with client_golang > v0.9.4 had backword incompatible chaanges and we hit with below compilation error:
// # k8s.io/component-base/metrics/legacyregistry
// vendor/k8s.io/component-base/metrics/legacyregistry/registry.go:44:9: undefined: prometheus.InstrumentHandler
replace github.com/prometheus/client_golang => github.com/prometheus/client_golang v0.9.3-0.20190127221311-3c4408c8b829

// Fix docker/docker version as needed by : https://github.com/deislabs/oras/blob/v0.7.0/go.mod
// Otherwise, we hit with:
// go: github.com/operator-framework/operator-sdk@v0.15.0 requires
//	helm.sh/helm/v3@v3.0.1 requires
//	github.com/deislabs/oras@v0.7.0 requires
//	github.com/docker/docker@v0.0.0-00010101000000-000000000000: invalid version: unknown revision 000000000000
replace github.com/docker/docker => github.com/moby/moby v0.7.3-0.20190826074503-38ab9da00309

// Temporary fork. Can be removed once https://github.com/kubernetes/kubernetes/pull/85540
// is merged and we update to a version >= 1.18.
replace k8s.io/kubernetes => github.com/pohly/kubernetes v1.10.0-alpha.3.0.20191122094604-c7faaae98a14
