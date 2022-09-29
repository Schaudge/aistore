// Package objwalk provides common context and helper methods for object listing and
// object querying.
/*
 * Copyright (c) 2018-2022, NVIDIA CORPORATION. All rights reserved.
 */
package objwalk

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/aistore/api/apc"
	"github.com/NVIDIA/aistore/cluster"
	"github.com/NVIDIA/aistore/cmn"
	"github.com/NVIDIA/aistore/cmn/cos"
	"github.com/NVIDIA/aistore/fs"
)

type (
	ctxKey           int
	PostCallbackFunc func(lom *cluster.LOM)

	// used to traverse local filesystem and collect objects info
	WalkInfo struct {
		t         cluster.Target
		smap      *cluster.Smap
		postCb    PostCallbackFunc
		markerDir string
		msg       apc.ListObjsMsg
		wanted    cos.BitFlags
	}
)

const (
	CtxPostCallbackKey ctxKey = iota
)

func isOK(status uint16) bool { return status == apc.LocOK }

// TODO: `msg.StartAfter`
func NewWalkInfo(ctx context.Context, t cluster.Target, msg *apc.ListObjsMsg) (wi *WalkInfo) {
	var (
		markerDir string
		wanted    cos.BitFlags
		postCb, _ = ctx.Value(CtxPostCallbackKey).(PostCallbackFunc)
	)
	if msg.ContinuationToken != "" { // marker is always a filename
		markerDir = filepath.Dir(msg.ContinuationToken)
		if markerDir == "." {
			markerDir = ""
		}
	}
	for prop, fl := range allmap {
		if msg.WantProp(prop) {
			wanted = wanted.Set(fl)
		}
	}
	wi = &WalkInfo{
		t:         t,
		smap:      t.Sowner().Get(),
		postCb:    postCb,
		markerDir: markerDir,
		wanted:    wanted,
	}
	wi.msg = *msg
	return
}

// Checks if the directory should be processed by cache list call
// Does checks:
//   - Object name must start with prefix (if it is set)
//   - Object name is not in early processed directories by the previous call:
//     paging support
func (wi *WalkInfo) ProcessDir(fqn string) error {
	ct, err := cluster.NewCTFromFQN(fqn, nil)
	if err != nil {
		return nil
	}

	if !cmn.DirNameContainsPrefix(ct.ObjectName(), wi.msg.Prefix) {
		return filepath.SkipDir
	}

	// When markerDir = "b/c/d/" we should skip directories: "a/", "b/a/",
	// "b/b/" etc. but should not skip entire "b/" or "b/c/" since it is our
	// parent which we want to traverse (see that: "b/" < "b/c/d/").
	if wi.markerDir != "" && ct.ObjectName() < wi.markerDir && !strings.HasPrefix(wi.markerDir, ct.ObjectName()) {
		return filepath.SkipDir
	}

	return nil
}

// Returns true if LOM is to be included in the result set.
func (wi *WalkInfo) match(lom *cluster.LOM) bool {
	if !cmn.ObjNameContainsPrefix(lom.ObjName, wi.msg.Prefix) {
		return false
	}
	if wi.msg.ContinuationToken != "" && cmn.TokenIncludesObject(wi.msg.ContinuationToken, lom.ObjName) {
		return false
	}
	return true
}

// new entry to be added to the listed page (TODO: add/support EC info)
func (wi *WalkInfo) ls(lom *cluster.LOM, status uint16) *cmn.ObjEntry {
	entry := &cmn.ObjEntry{Name: lom.ObjName, Flags: status | apc.EntryIsCached}
	if wi.msg.IsFlagSet(apc.LsNameOnly) {
		return entry
	}
	for name, fl := range allmap {
		if !wi.wanted.IsSet(fl) {
			continue
		}
		switch name {
		case apc.GetPropsName:
		case apc.GetPropsStatus:
		case apc.GetPropsCached:

		case apc.GetPropsSize:
			entry.Size = lom.SizeBytes()
		case apc.GetPropsVersion:
			entry.Version = lom.Version()
		case apc.GetPropsChecksum:
			if lom.Checksum() != nil {
				entry.Checksum = lom.Checksum().Value()
			}
		case apc.GetPropsAtime:
			entry.Atime = cos.FormatUnixNano(lom.AtimeUnix(), wi.msg.TimeFormat)

		case apc.GetTargetURL:
			// TODO -- FIXME: rename as target-info, remove URL, add node ID and mountpath instead
			entry.TargetURL = wi.t.Snode().URL(cmn.NetPublic)
		case apc.GetPropsNode:
			// TODO -- FIXME: as above
		case apc.GetPropsCopies:
			// TODO -- FIXME: may not be true - double-check
			entry.Copies = int16(lom.NumCopies())
		case apc.GetPropsEC:
			// TODO -- FIXME: add/support EC info
		case apc.GetPropsCustom:
			// TODO -- FIXME: add/support custom
		default:
		}
	}
	if wi.postCb != nil {
		wi.postCb(lom)
	}
	return entry
}

// By default, Callback performs a number of syscalls to load object metadata.
// A note in re cmn.LsNameOnly (usage below):
//
//	the flag cmn.LsNameOnly optimizes-out loading object metadata. If defined,
//	the function returns (only the) name and status.
func (wi *WalkInfo) Callback(fqn string, de fs.DirEntry) (entry *cmn.ObjEntry, err error) {
	if de.IsDir() {
		return
	}
	lom := cluster.AllocLOM("")
	entry, err = wi.cb(lom, fqn)
	cluster.FreeLOM(lom)
	return
}

func (wi *WalkInfo) cb(lom *cluster.LOM, fqn string) (*cmn.ObjEntry, error) {
	status := uint16(apc.LocOK)
	if err := lom.InitFQN(fqn, nil); err != nil {
		return nil, err
	}

	if !wi.match(lom) {
		return nil, nil
	}

	_, local, err := lom.HrwTarget(wi.smap)
	if err != nil {
		return nil, err
	}
	if !local {
		status = apc.LocMisplacedNode
	} else if !lom.IsHRW() {
		// preliminary
		status = apc.LocMisplacedMountpath
	}

	// shortcut #1: name-only (NOTE: won't show misplaced and copies)
	if wi.msg.IsFlagSet(apc.LsNameOnly) {
		if !isOK(status) {
			return nil, nil
		}
		return wi.ls(lom, status), nil
	}
	// load
	if err := lom.Load(isOK(status) /*cache it*/, false /*locked*/); err != nil {
		if cmn.IsErrObjNought(err) || !isOK(status) {
			return nil, nil
		}
		return nil, err
	}
	if local && lom.IsCopy() {
		// still may change below
		status = apc.LocIsCopy
	}
	if isOK(status) {
		return wi.ls(lom, status), nil
	}

	if !wi.msg.IsFlagSet(apc.LsAll) {
		return nil, nil
	}
	if local {
		// check hrw mountpath location
		hlom := &cluster.LOM{}
		if err := hlom.InitFQN(lom.HrwFQN, lom.Bucket()); err != nil {
			return nil, err
		}
		if err := hlom.Load(true /*cache it*/, false /*locked*/); err != nil {
			mirror := lom.MirrorConf()
			if mirror.Enabled && mirror.Copies > 1 {
				status = apc.LocIsCopyMissingObj
			}
		}
	}
	return wi.ls(lom, status), nil
}