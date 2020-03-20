OPERATOR_SDK_VERSION=v0.12.0

# download operator-sdk binary
_work/bin/operator-sdk-$(OPERATOR_SDK_VERSION):
	mkdir -p _work/bin/ 2> /dev/null
	curl -L https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk-$(OPERATOR_SDK_VERSION)-x86_64-linux-gnu -o $(abspath $@)
	chmod a+x $(abspath $@)

# Re-generates the K8S source. This target is supposed to run
# upon any changes made to operator api.
#
# GOROOT is needed because of https://github.com/operator-framework/operator-sdk/issues/1854#issuecomment-525132306
operator-generate-k8s: _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION)
	GOROOT=$(shell $(GO) env GOROOT) _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION) generate k8s
