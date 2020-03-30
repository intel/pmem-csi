# Build a suitable https://github.com/govm-project/govm/releases/tag/latest version.
GOVM_VERSION=0.9-alpha
_work/govm_$(GOVM_VERSION)_Linux_amd64.tar.gz:
	curl -L https://github.com/govm-project/govm/releases/download/$(GOVM_VERSION)/govm_$(GOVM_VERSION)_Linux_x86_64.tar.gz -o $(abspath $@)

_work/bin/govm: _work/govm_$(GOVM_VERSION)_Linux_amd64.tar.gz
	tar zxf $< -C _work/bin/
	touch $@

# Brings up the emulator environment:
# - starts a Kubernetes cluster with NVDIMMs as described in https://github.com/qemu/qemu/blob/bd54b11062c4baa7d2e4efadcf71b8cfd55311fd/docs/nvdimm.txt
# - generate pmem secrets if necessary
start: _work/pmem-ca/.ca-stamp _work/bin/govm
	PATH="$(PWD)/_work/bin:$$PATH" test/start-kubernetes.sh

# Stops the VMs and removes all files.
stop: _work/bin/govm
	@ if [ -f _work/$(CLUSTER)/stop.sh ]; then \
		PATH="$(PWD)/_work/bin:$$PATH" _work/$(CLUSTER)/stop.sh && \
		rm -rf _work/$(CLUSTER); \
	else \
		echo "Cluster $(CLUSTER) was already removed."; \
	fi

# Clean shutdown of all VMs, then waits for reboot of cluster.
restart:
	@ if [ -f _work/$(CLUSTER)/stop.sh ]; then \
		_work/$(CLUSTER)/restart.sh; \
	else \
		echo "Cluster $(CLUSTER) is not running, it cannot be restarted."; \
		exit 1; \
	fi

start_test_vm: _work/bin/govm
	PATH="$(PWD)/_work/bin:$$PATH" NODES=$(NODE) INIT_CLUSTER=false test/start-kubernetes.sh
