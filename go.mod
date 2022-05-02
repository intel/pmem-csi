module github.com/intel/pmem-csi

go 1.16

require (
	github.com/aws/aws-sdk-go v1.38.71 // indirect
	github.com/container-storage-interface/spec v1.5.0
	github.com/emicklei/go-restful v2.15.0+incompatible // indirect
	github.com/evanphx/json-patch v5.6.0+incompatible // indirect
	github.com/felixge/httpsnoop v1.0.2 // indirect
	github.com/fsnotify/fsnotify v1.5.4 // indirect
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/go-logr/logr v1.2.3
	github.com/go-logr/zapr v1.2.3 // indirect
	github.com/go-openapi/jsonreference v0.20.0 // indirect
	github.com/go-openapi/swag v0.21.1 // indirect
	github.com/google/gnostic v0.6.9 // indirect
	github.com/google/go-cmp v0.5.8
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/uuid v1.3.0
	github.com/kubernetes-csi/csi-lib-utils v0.9.1
	github.com/kubernetes-csi/csi-test/v4 v4.2.0
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/miekg/dns v1.1.48 // indirect
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/ginkgo/v2 v2.1.4 // indirect
	github.com/onsi/gomega v1.19.0
	github.com/opencontainers/selinux v1.10.1 // indirect
	github.com/operator-framework/operator-lib v0.4.1
	github.com/prometheus/client_golang v1.12.1
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.33.0
	github.com/stretchr/testify v1.7.1
	golang.org/x/crypto v0.0.0-20220427172511-eb4f295cb31f // indirect
	golang.org/x/net v0.0.0-20220425223048-2871e0cb64e4
	golang.org/x/oauth2 v0.0.0-20220411215720-9780585627b5 // indirect
	golang.org/x/sys v0.0.0-20220502124256-b6088ccd6cba
	golang.org/x/term v0.0.0-20220411215600-e5f449aeb171 // indirect
	golang.org/x/time v0.0.0-20220411224347-583f2d630306 // indirect
	golang.org/x/xerrors v0.0.0-20220411194840-2f41105eb62f // indirect
	google.golang.org/genproto v0.0.0-20220429170224-98d788798c3e // indirect
	google.golang.org/grpc v1.45.0
	google.golang.org/protobuf v1.28.0
	gopkg.in/freddierice/go-losetup.v1 v1.0.0-20170407175016-fc9adea44124
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.24.0
	k8s.io/apiextensions-apiserver v0.24.0
	k8s.io/apimachinery v0.24.0
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/component-base v0.24.0
	k8s.io/klog/v2 v2.60.1
	k8s.io/kube-openapi v0.0.0-20220413171646-5e7f5fdc6da6 // indirect
	k8s.io/kube-scheduler v0.24.0
	k8s.io/kubectl v1.24.0
	k8s.io/kubernetes v1.24.0
	k8s.io/utils v0.0.0-20220210201930-3a6ce19ff2f9
	sigs.k8s.io/controller-runtime v0.11.2
	sigs.k8s.io/sig-storage-lib-external-provisioner/v6 v6.2.0
	sigs.k8s.io/yaml v1.3.0
)

replace (
	k8s.io/api => k8s.io/api v0.24.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.24.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.24.0
	k8s.io/apiserver => k8s.io/apiserver v0.24.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.24.0
	k8s.io/client-go => k8s.io/client-go v0.24.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.24.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.24.0
	k8s.io/code-generator => k8s.io/code-generator v0.24.0
	k8s.io/component-base => k8s.io/component-base v0.24.0
	k8s.io/component-helpers => k8s.io/component-helpers v0.24.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.24.0
	k8s.io/cri-api => k8s.io/cri-api v0.24.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.24.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.24.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.24.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.24.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.24.0
	k8s.io/kubectl => k8s.io/kubectl v0.24.0
	k8s.io/kubelet => k8s.io/kubelet v0.24.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.24.0
	k8s.io/metrics => k8s.io/metrics v0.24.0
	k8s.io/mount-utils => k8s.io/mount-utils v0.24.0
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.24.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.24.0
)
