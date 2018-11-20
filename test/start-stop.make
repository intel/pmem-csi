# Brings up the emulator environment:
# - starts QEMU virtual machines
# - starts a Kubernetes cluster
start: _work/clear-kvm.img _work/kube-clear-kvm _work/start-clear-kvm _work/ssh-clear-kvm
	for i in $$(seq 0 $$(($(NUM_NODES) - 1))); do \
		if ! [ -e _work/clear-kvm.$$i.pid ] || ! kill -0 $$(cat _work/clear-kvm.$$i.pid) 2>/dev/null; then \
			_work/start-clear-kvm _work/clear-kvm.$$i.img -monitor none -serial file:_work/clear-kvm.$$i.log $$opts & \
			echo $$! >_work/clear-kvm.$$i.pid; \
		fi; \
	done
	while ! _work/ssh-clear-kvm true 2>/dev/null; do \
		sleep 1; \
	done
	_work/kube-clear-kvm
	@ echo "The test cluster is ready. Log in with _work/ssh-clear-kvm, run kubectl once logged in."

stop:
	for i in $$(seq 0 $$(($(NUM_NODES) - 1))); do \
		if [ -e _work/clear-kvm.$$i.pid ]; then \
			kill -9 $$(cat _work/clear-kvm.$$i.pid) 2>/dev/null; \
			rm -f _work/clear-kvm.$$i.pid; \
		fi; \
	done
