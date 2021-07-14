/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package deploy

import (
	"context"
	"fmt"
	"os"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/util/errors"

	pmemexec "github.com/intel/pmem-csi/pkg/exec"
)

func ResetPMEM(ctx context.Context, node string) error {
	var errs []error

	sshcmd := fmt.Sprintf("%s/_work/%s/ssh.%s", os.Getenv("REPO_ROOT"), os.Getenv("CLUSTER"), node)

	if err := resetLVM(ctx, sshcmd, "vgs", "vgremove"); err != nil {
		errs = append(errs, fmt.Errorf("LVM volume groups: %v", err))
	}

	if err := resetLVM(ctx, sshcmd, "pvs", "pvremove"); err != nil {
		errs = append(errs, fmt.Errorf("LVM physical volumes: %v", err))
	}

	if _, err := pmemexec.RunCommand(ctx, sshcmd, "sudo ndctl destroy-namespace --force all"); err != nil {
		errs = append(errs, fmt.Errorf("erasing namespaces failed: %v", err))
	}

	return apierrors.NewAggregate(errs)
}

func resetLVM(ctx context.Context, sshCmd, listCmd, rmCmd string) error {
	out, err := pmemexec.RunCommand(ctx, sshCmd, "sudo "+listCmd+" --noheadings --options name")
	if err != nil {
		return fmt.Errorf("listing failed: %v", err)
	} else {
		for _, item := range strings.Split(string(out), "\n") {
			switch listCmd {
			case "vgs":
				if !strings.HasPrefix(item, "ndbus") {
					continue
				}
			case "pvs":
				if !strings.HasPrefix(item, "/dev/pmem") {
					continue
				}
			}

			if _, err := pmemexec.RunCommand(ctx, sshCmd, "sudo "+rmCmd+" -f "+item); err != nil {
				return fmt.Errorf("removal failed: %v", err)
			}
		}
	}
	return nil
}
