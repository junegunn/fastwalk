// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build freebsd || openbsd || netbsd
// +build freebsd openbsd netbsd

package fastwalk

import "syscall"

func direntNamlen(dirent *syscall.Dirent) (uint64, error) {
	return uint64(dirent.Namlen), nil
}

func direntInode(dirent *syscall.Dirent) uint64 {
	return uint64(dirent.Fileno)
}
