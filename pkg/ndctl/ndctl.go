/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package ndctl

import (
        "bytes"
	"fmt"
	"encoding/json"
	"errors"
	"os/exec"

        "github.com/golang/glog"
)

// This needs to be []struct because ndctl reports json-format output inside []
// TODO: Early dev has been done using/seeing one region only.
// For multiple regions, ndctl shows repeated blocks like this.
// For multi-region case, we should not report sum of free spaces, but pick one which is biggest.
// In NUMA case, the numa-local region should have higher priority
type ndlistr []struct {
        Dev               string `json:"dev"`
        Size              uint64 `json:"size"`
        AvailSize         uint64 `json:"available_size"`
        Type              string `json:"type"`
        IsetID            uint64 `json:"iset_id"`
        PersistenceDomain string `json:"persistence_domain"`
}

// Check for available capacity
func GetAvailableSize() (uint64, error) {
	result := new(ndlistr)
        if err := run("ndctl", result, "list", "-R"); err != nil {
                return 0, err
        }
        //glog.Infof("result is: %+v", result)
        glog.Infof("GetAvailableSize: %v", (*result)[0].AvailSize)
	return  (*result)[0].AvailSize, nil
}

// Create namespace
// this is returned by ndctl create-namespace -s SIZE -n NAME
type ndcreatenamespace struct {
        Dev               string `json:"dev"`
        Mode              string `json:"mode"`
        Map               string `json:"map"`
        Size              uint64 `json:"size"`
        Uuid              string `json:"uuid"`
        RawUuid           string `json:"raw_uuid"`
        SectorSize        uint64 `json:"sector_size"`
        BlockDev          string `json:"blockdev"`
        Name              string `json:"name"`
        Numanode          uint64 `json:"numa_node"`
}

func CreateNamespace(size uint64, volname string) error {
	result := new(ndcreatenamespace)
	sizestr := fmt.Sprint(size)
        if err := run("ndctl", result, "create-namespace", "-s", sizestr, "-n", volname); err != nil {
                return err
        }
        glog.Infof("CreateNamespace: result; %+v", result)
	return nil
}


// List namespaces
type ndlistnamespaces []struct {
        Dev               string `json:"dev"`
        Mode              string `json:"mode"`
        Map               string `json:"map"`
        Size              uint64 `json:"size"`
        Uuid              string `json:"uuid"`
        BlockDev          string `json:"blockdev"`
        Name              string `json:"name"`
}

func ListNamespaces() (error,  []string, []uint64) {
        var names []string
        var sizes []uint64
	result := new(ndlistnamespaces)
        if err := run("ndctl", result, "list", "--namespaces"); err != nil {
                return err, nil, nil
        }
	for _, elem:= range *result {
		names = append(names, elem.Name)
		sizes = append(sizes, elem.Size)
        }
        glog.Infof("ListNamespaces: names: %v", names)
        glog.Infof("ListNamespaces: sizes: %v", sizes)
	return nil, names, sizes
}

// Find entry by Name, return it's  Blockdev and Size
// We get list of all namespaces and walk it
func GetBlockDevByName(name string) (string, uint64, error) {
	result := new(ndlistnamespaces)
        if err := run("ndctl", result, "list", "--namespaces"); err != nil {
                return "error", 0, err
        }
	for _, elem:= range *result {
                //glog.Infof("elem is: %+v", elem)
                //glog.Infof("  Name: %v Dev: %v Size: %v", elem.Name, elem.Dev, elem.Size)
		if elem.Name == name {
                        glog.Infof("GetBlockDevByName:%v result is: %v", name, elem.BlockDev)
	                return elem.BlockDev, elem.Size, nil
		}
        }
        glog.Infof("GetBlockByName: did not find namespace entry for volumename %v", name)
        return "error", 0, errors.New("GetBlockByName: did not find match for volumename")
}

// Delete one: list all namespaces, walk, match by Name and use Dev to delete
func DeleteNamespace(volname string) error {
	result := new(ndlistnamespaces)
        if err := run("ndctl", result, "list", "--namespaces"); err != nil {
                return err
        }
	for _, elem:= range *result {
		if elem.Name == volname {
                        glog.Infof("DeleteNamespace: based on volumename %v found dev: %s", volname, elem.Dev)
                        if err := run("ndctl", nil, "destroy-namespace", "--force", elem.Dev); err != nil {
                                glog.Infof("destroy-namespace output is: %+v", result)
                                return err
                        }
                        glog.Infof("DeleteNamespace: destroy-namespace successful")
	                return nil
		}
        }
        glog.Infof("DeleteNamespace: did not find namespace entry for volumename %v", volname)
        return errors.New("DeleteNamespace: did not find match for volumename")
}

// Run a command
// This is loosely based on similar function in LVM-CSI driver,
// simplified for current use
func run(cmd string, v interface{}, extraArgs ...string) error {
        var args []string
        args = append(args, extraArgs...)
        c := exec.Command(cmd, args...)
        glog.Infof("run: Executing: %v", c)
        stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
        c.Stdout = stdout
        c.Stderr = stderr 
        if err := c.Run(); err != nil {
                glog.Infof("run: stdout: " + stdout.String())
                glog.Infof("run: stderr: " + stderr.String())
                return errors.New(stderr.String())
        }
        stdoutbuf := stdout.Bytes()
        stderrbuf := stderr.Bytes()
        glog.Infof("run: stdout: " + string(stdoutbuf))
        glog.Infof("run: stderr: " + string(stderrbuf))
        if v != nil {
                if err := json.Unmarshal(stdoutbuf, v); err != nil {
                        glog.Errorf("run: %v: [%v]", err, string(stdoutbuf))
                        return fmt.Errorf("%v: [%v]", err, string(stdoutbuf))
                }
        }
        return nil
}
