/*
 * Copyright (c) 2018, NVIDIA CORPORATION. All rights reserved.
 */
package health

import (
	"fmt"
	"os"
	"testing"

	"github.com/NVIDIA/dfcpub/cmn"
	"github.com/NVIDIA/dfcpub/fs"
	"github.com/NVIDIA/dfcpub/memsys"
)

const (
	fsCheckerTmpDir = "/tmp/fshc"
)

var gmem2 *memsys.Mem2

func testMemInit(name string) {
	gmem2 = &memsys.Mem2{Name: name}
	_ = gmem2.Init(false /* ignore init-time errors */)
}

func testCheckerMountPaths() *fs.MountedFS {
	cmn.CreateDir(fsCheckerTmpDir)
	cmn.CreateDir(fsCheckerTmpDir + "/1")
	cmn.CreateDir(fsCheckerTmpDir + "/2")
	cmn.CreateDir(fsCheckerTmpDir + "/3")
	cmn.CreateDir(fsCheckerTmpDir + "/4")

	mountedFS := fs.NewMountedFS("local", "cloud")
	mountedFS.DisableFsIDCheck()
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("%s/%d", fsCheckerTmpDir, i)
		mountedFS.Add(name)
	}

	os.RemoveAll(fsCheckerTmpDir + "/3") // one folder is deleted
	mountedFS.Disable(fsCheckerTmpDir + "/4")
	return mountedFS
}

func testCheckerConfig() *cmn.Config {
	config := cmn.Config{}
	config.FSHC.Enabled = true
	config.FSHC.ErrorLimit = 2
	return &config
}

type MockFSDispatcher struct {
	faultyPath    string
	faultDetected bool
}

func newMockFSDispatcher(mpathToFail string) *MockFSDispatcher {
	return &MockFSDispatcher{
		faultyPath: mpathToFail,
	}
}

func (d *MockFSDispatcher) Disable(path, why string) (disabled, exists bool) {
	d.faultDetected = path == d.faultyPath
	return d.faultDetected, true
}

func testCheckerCleanup() {
	os.RemoveAll(fsCheckerTmpDir)
}

func TestFSCheckerMain(t *testing.T) {
	testMemInit("fshctest")
	fshc := NewFSHC(testCheckerMountPaths(), gmem2)
	if fshc == nil {
		t.Error("Failed to create fshc")
	}
	fshc.Setconf(testCheckerConfig())

	// intial state = 2 availble FSes - must pass
	availablePaths, disabledPaths := fshc.mountpaths.Get()
	if len(availablePaths) != 3 || len(disabledPaths) != 1 {
		t.Errorf("Invalid number of mountpaths at start: %v - %v",
			availablePaths, disabledPaths)
	}

	// inaccessible mountpath
	_, _, exists := fshc.testMountpath(
		fsCheckerTmpDir+"/3/testfile", fsCheckerTmpDir+"/3", 4, 1024)
	if exists {
		t.Error("Testing non-existing mountpath must fail")
	}

	// healthy mountpath
	reads, writes, exists := fshc.testMountpath(
		fsCheckerTmpDir+"/2/testfile", fsCheckerTmpDir+"/2", 4, 1024)
	if !exists {
		t.Error("Testing existing mountpath must detect the mountpath is available")
	}
	if reads != 0 || writes != 0 {
		t.Errorf("Testing existing mountpath must not fail. Read errors: %d, write errors: %d", reads, writes)
	}

	// failed mountpath must be disabled
	failedMpath := fsCheckerTmpDir + "/3"
	dispatcher := newMockFSDispatcher(failedMpath)
	fshc.SetDispatcher(dispatcher)
	fshc.runMpathTest(failedMpath, failedMpath+"/dir/testfile")

	if !dispatcher.faultDetected {
		t.Errorf("Faulty mountpath %s was not detected", failedMpath)
	}

	// decision making function
	type tstInfo struct {
		title               string
		readErrs, writeErrs int
		avail, result       bool
	}
	testList := []tstInfo{
		{"Inaccessible mountpath", 0, 0, false, false},
		{"Healthy mountpath", 0, 0, true, true},
		{"Unstable but OK mountpath", 1, 1, true, true},
		{"Reads failed", 3, 0, true, false},
		{"Writes failed", 1, 3, true, false},
		{"Reads and writes failed", 3, 3, true, false},
	}

	for _, tst := range testList {
		fmt.Printf("Test: %s.\n", tst.title)
		res, _ := fshc.isTestPassed("/tmp", tst.readErrs, tst.writeErrs, tst.avail)
		if res == tst.result {
			fmt.Printf("    PASSED\n")
		} else {
			fmt.Printf("    FAILED\n")
			t.Errorf("%s failed. %v expected but %v got", tst.title, tst.result, res)
		}
	}

	testCheckerCleanup()
}
