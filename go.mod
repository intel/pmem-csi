module github.com/intel/pmem-csi

go 1.13

require (
	github.com/aws/aws-sdk-go v1.35.0 // indirect
	github.com/blang/semver v3.5.1+incompatible // indirect
	github.com/container-storage-interface/spec v1.3.0
	github.com/coreos/go-systemd v0.0.0-20190620071333-e64a0ec8b42a // indirect
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/emicklei/go-restful v2.9.6+incompatible // indirect
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/go-logr/logr v0.2.1
	github.com/go-logr/zapr v0.2.0 // indirect
	github.com/golang/groupcache v0.0.0-20200121045136-8c9f03a8e57e // indirect
	github.com/golang/protobuf v1.4.2
	github.com/google/go-cmp v0.5.2 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/uuid v1.1.2
	github.com/googleapis/gnostic v0.5.1 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware v1.1.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.12.1 // indirect
	github.com/imdario/mergo v0.3.11 // indirect
	github.com/kubernetes-csi/csi-lib-utils v0.9.0
	github.com/kubernetes-csi/csi-test/v3 v3.1.1
	github.com/nxadm/tail v1.4.5 // indirect
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/operator-framework/operator-lib v0.1.0
	github.com/prometheus/client_golang v1.7.1
	github.com/prometheus/common v0.14.0
	github.com/prometheus/procfs v0.2.0 // indirect
	github.com/stretchr/testify v1.6.1
	go.uber.org/multierr v1.6.0 // indirect
	go.uber.org/zap v1.16.0 // indirect
	golang.org/x/crypto v0.0.0-20200930160638-afb6bcd081ae // indirect
	golang.org/x/lint v0.0.0-20200302205851-738671d3881b // indirect
	golang.org/x/net v0.0.0-20200930145003-4acb6c075d10
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d // indirect
	golang.org/x/sys v0.0.0-20200930185726-fdedc70b468f
	golang.org/x/time v0.0.0-20200630173020-3af7569d3a1e // indirect
	golang.org/x/tools v0.0.0-20200825202427-b303f430e36d // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	gomodules.xyz/jsonpatch/v2 v2.1.0 // indirect
	google.golang.org/appengine v1.6.6 // indirect
	google.golang.org/genproto v0.0.0-20200930140634-01fc692af84b // indirect
	google.golang.org/grpc v1.29.1
	google.golang.org/protobuf v1.25.0 // indirect
	gopkg.in/freddierice/go-losetup.v1 v1.0.0-20170407175016-fc9adea44124
	gopkg.in/yaml.v2 v2.3.0
	honnef.co/go/tools v0.0.1-2020.1.4 // indirect
	k8s.io/api v0.19.2
	k8s.io/apiextensions-apiserver v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/apiserver v0.19.2 // indirect
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/cloud-provider v0.19.2 // indirect
	k8s.io/component-base v0.19.2
	k8s.io/csi-translation-lib v0.19.2 // indirect
	k8s.io/klog/v2 v2.3.0
	k8s.io/kube-openapi v0.0.0-20200923155610-8b5066479488 // indirect
	k8s.io/kube-scheduler v0.19.2
	k8s.io/kubectl v0.19.2
	k8s.io/kubernetes v1.19.2
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800
	sigs.k8s.io/apiserver-network-proxy/konnectivity-client v0.0.12 // indirect
	sigs.k8s.io/controller-runtime v0.6.3
)

replace (
	k8s.io/api => k8s.io/api v0.19.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.19.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.19.1-rc.0
	k8s.io/apiserver => k8s.io/apiserver v0.19.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.19.0
	k8s.io/client-go => k8s.io/client-go v0.19.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.19.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.19.0
	k8s.io/code-generator => k8s.io/code-generator v0.19.1-rc.0
	k8s.io/component-base => k8s.io/component-base v0.19.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.19.1-rc.0
	k8s.io/cri-api => k8s.io/cri-api v0.19.1-rc.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.19.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.19.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.19.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.19.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.19.0
	k8s.io/kubectl => k8s.io/kubectl v0.19.0
	k8s.io/kubelet => k8s.io/kubelet v0.19.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.19.0
	k8s.io/metrics => k8s.io/metrics v0.19.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.19.0
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.19.0
	k8s.io/sample-controller => k8s.io/sample-controller v0.19.0
)

// Temporary fork based on release-1.19 with additional PRs:
// - https://github.com/kubernetes/kubernetes/pull/94647
// - https://github.com/kubernetes/kubernetes/pull/93930
replace k8s.io/kubernetes => github.com/pohly/kubernetes v1.10.0-alpha.3.0.20200909111025-b4fda25e0e77
