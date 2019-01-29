# Brings up the emulator environment:
# - creates pmem NVDIMM files if necessary
# - starts QEMU virtual machines with pmem NVDIMMs as described in https://github.com/qemu/qemu/blob/bd54b11062c4baa7d2e4efadcf71b8cfd55311fd/docs/nvdimm.txt
# - starts a Kubernetes cluster
# - generate pmem secrets if necessary
# - installs pmem-csi driver
start: _work/clear-kvm.img _work/kube-clear-kvm _work/start-clear-kvm _work/ssh-clear-kvm test/setup-ca-kubernetes.sh _work/.setupcfssl-stamp
	. test/test-config.sh && \
	for i in $$(seq 0 $$(($(NUM_NODES) - 1))); do \
		if ! [ -e _work/clear-kvm.$$i.pid ] || ! kill -0 $$(cat _work/clear-kvm.$$i.pid) 2>/dev/null; then \
			memargs="-m $${TEST_NORMAL_MEM_SIZE}M"; \
			if [ $$i -ne 0 ]; then \
				pmemfile=_work/clear-kvm.$$i.pmem.raw; \
				truncate -s $${TEST_PMEM_MEM_SIZE}M $$pmemfile; \
				memargs="$$memargs,slots=$${TEST_MEM_SLOTS},maxmem=$$(($${TEST_NORMAL_MEM_SIZE} + $${TEST_PMEM_MEM_SIZE}))M"; \
				memargs="$$memargs -object memory-backend-file,id=mem1,share=$${TEST_PMEM_SHARE},mem-path=$$pmemfile,size=$${TEST_PMEM_MEM_SIZE}M"; \
				memargs="$$memargs -device nvdimm,id=nvdimm1,memdev=mem1,label-size=$${TEST_PMEM_LABEL_SIZE}"; \
				memargs="$$memargs -machine pc,nvdimm"; \
			fi; \
			_work/start-clear-kvm _work/clear-kvm.$$i.img $$memargs -monitor none -serial file:_work/clear-kvm.$$i.log $$opts & \
			echo $$! >_work/clear-kvm.$$i.pid; \
		fi; \
	done
	while ! _work/ssh-clear-kvm true 2>/dev/null; do \
		sleep 1; \
	done
	_work/kube-clear-kvm
	_work/ssh-clear-kvm kubectl label node host-0 storage-
	for i in $$(seq 1 $$(($(NUM_NODES) - 1))); do \
		if ! [ -e _work/clear-kvm.$$i.labelsdone ]; then \
			_work/ssh-clear-kvm.$$i "/usr/bin/ndctl disable-region region0"; \
			_work/ssh-clear-kvm.$$i "/usr/bin/ndctl init-labels nmem0"; \
			_work/ssh-clear-kvm.$$i "/usr/bin/ndctl enable-region region0"; \
			touch _work/clear-kvm.$$i.labelsdone; \
		fi; \
		_work/ssh-clear-kvm kubectl label --overwrite node host-$$i storage=pmem; \
	done
	if ! [ -e _work/clear-kvm.secretsdone ] || [ $$(_work/ssh-clear-kvm 'kubectl get secrets | grep pmem- | wc -l') -ne 2 ]; then \
		KUBECONFIG=$(PWD)/_work/clear-kvm-kube.config PATH='$(PWD)/_work/bin/:$(PATH)' ./test/setup-ca-kubernetes.sh && \
		touch _work/clear-kvm.secretsdone; \
	fi
	_work/ssh-clear-kvm kubectl version --short | grep 'Server Version' | sed -e 's/.*: v\([0-9]*\)\.\([0-9]*\)\..*/\1.\2/' >_work/clear-kvm-kubernetes.version
	if ! _work/ssh-clear-kvm kubectl get statefulset.apps/pmem-csi-controller daemonset.apps/pmem-csi >/dev/null 2>&1; then \
		_work/ssh-clear-kvm kubectl create -f - <deploy/kubernetes-$$(cat _work/clear-kvm-kubernetes.version)/pmem-csi.yaml; \
	fi
	@ echo
	@ echo "The test cluster is ready. Log in with _work/ssh-clear-kvm, run kubectl once logged in."
	@ echo "Alternatively, KUBECONFIG=$$(pwd)/_work/clear-kvm-kube.config can also be used directly."
	@ echo "To try out the pmem-csi driver:"
	@ echo "   cat deploy/kubernetes-$$(cat _work/clear-kvm-kubernetes.version)/pmem-pvc.yaml | _work/ssh-clear-kvm kubectl create -f -"
	@ echo "   cat deploy/kubernetes-$$(cat _work/clear-kvm-kubernetes.version)/pmem-app.yaml | _work/ssh-clear-kvm kubectl create -f -"

stop:
	for i in $$(seq 0 $$(($(NUM_NODES) - 1))); do \
		if [ -e _work/clear-kvm.$$i.pid ]; then \
			kill -9 $$(cat _work/clear-kvm.$$i.pid) 2>/dev/null; \
			rm -f _work/clear-kvm.$$i.pid; \
		fi; \
	done
