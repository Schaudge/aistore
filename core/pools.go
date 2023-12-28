// Package core provides core metadata and in-cluster API
/*
 * Copyright (c) 2018-2023, NVIDIA CORPORATION. All rights reserved.
 */
package core

import (
	"sync"

	"github.com/NVIDIA/aistore/cmn/debug"
)

var (
	lomPool sync.Pool
	lom0    LOM

	putObjPool sync.Pool
	putObj0    PutObjectParams
)

/////////////
// lomPool //
/////////////

func AllocLOM(objName string) *LOM {
	v := lomPool.Get()
	if v == nil {
		return &LOM{ObjName: objName}
	}
	lom := v.(*LOM)
	debug.Assert(lom.ObjName == "" && lom.FQN == "")
	lom.ObjName = objName
	return lom
}

func FreeLOM(lom *LOM) {
	debug.Assertf(lom.ObjName != "" || lom.FQN != "", "%q, %q", lom.ObjName, lom.FQN)
	*lom = lom0
	lomPool.Put(lom)
}

//
// PutObjectParams pool
//

func AllocPutObjParams() (a *PutObjectParams) {
	if v := putObjPool.Get(); v != nil {
		a = v.(*PutObjectParams)
		return
	}
	return &PutObjectParams{}
}

func FreePutObjParams(a *PutObjectParams) {
	*a = putObj0
	putObjPool.Put(a)
}