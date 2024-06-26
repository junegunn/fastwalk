// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (linux || freebsd || openbsd || netbsd) && !appengine
// +build linux freebsd openbsd netbsd
// +build !appengine

package fastwalk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"unsafe"
)

const blockSize = 8 << 10

// unknownFileMode is a sentinel (and bogus) os.FileMode
// value used to represent a syscall.DT_UNKNOWN Dirent.Type.
const unknownFileMode os.FileMode = os.ModeNamedPipe | os.ModeSocket | os.ModeDevice

func readDir(dirName string, fn func(dirName, entName string, de fs.DirEntry) error) error {
	fd, err := open(dirName, 0, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: dirName, Err: err}
	}
	defer syscall.Close(fd)

	// The buffer must be at least a block long.
	buf := make([]byte, blockSize) // stack-allocated; doesn't escape
	bufp := 0                      // starting read position in buf
	nbuf := 0                      // end valid data in buf
	skipFiles := false
	for {
		if bufp >= nbuf {
			bufp = 0
			nbuf, err = readDirent(fd, buf)
			if err != nil {
				return os.NewSyscallError("readdirent", err)
			}
			if nbuf <= 0 {
				return nil
			}
		}
		consumed, name, typ, err := parseDirEnt(buf[bufp:nbuf])
		if err != nil {
			return os.NewSyscallError("readdirent", err)
		}
		bufp += consumed
		if name == "" || name == "." || name == ".." {
			continue
		}
		// Fallback for filesystems (like old XFS) that don't
		// support Dirent.Type and have DT_UNKNOWN (0) there
		// instead.
		if typ == unknownFileMode {
			fi, err := os.Lstat(dirName + "/" + name)
			if err != nil {
				// It got deleted in the meantime.
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
			typ = fi.Mode() & os.ModeType
		}
		if skipFiles && typ.IsRegular() {
			continue
		}
		de := newUnixDirent(dirName, name, typ)
		if err := fn(dirName, name, de); err != nil {
			if err == ErrSkipFiles {
				skipFiles = true
				continue
			}
			return err
		}
	}
}

func parseDirEnt(buf []byte) (consumed int, name string, typ os.FileMode, err error) {
	// golang.org/issue/37269
	dirent := &syscall.Dirent{}
	copy((*[unsafe.Sizeof(syscall.Dirent{})]byte)(unsafe.Pointer(dirent))[:], buf)
	if v := unsafe.Offsetof(dirent.Reclen) + unsafe.Sizeof(dirent.Reclen); uintptr(len(buf)) < v {
		err = errors.New(fmt.Sprintf("buf size of %d smaller than dirent header size %d", len(buf), v))
		return
	}
	if len(buf) < int(dirent.Reclen) {
		err = errors.New(fmt.Sprintf("buf size %d < record length %d", len(buf), dirent.Reclen))
		return
	}
	consumed = int(dirent.Reclen)
	if direntInode(dirent) == 0 { // File absent in directory.
		return
	}
	switch dirent.Type {
	case syscall.DT_REG:
		typ = 0
	case syscall.DT_DIR:
		typ = os.ModeDir
	case syscall.DT_LNK:
		typ = os.ModeSymlink
	case syscall.DT_BLK:
		typ = os.ModeDevice
	case syscall.DT_FIFO:
		typ = os.ModeNamedPipe
	case syscall.DT_SOCK:
		typ = os.ModeSocket
	case syscall.DT_UNKNOWN:
		typ = unknownFileMode
	default:
		// Skip weird things.
		// It's probably a DT_WHT (http://lwn.net/Articles/325369/)
		// or something. Revisit if/when this package is moved outside
		// of goimports. goimports only cares about regular files,
		// symlinks, and directories.
		return
	}

	nameBuf := (*[unsafe.Sizeof(dirent.Name)]byte)(unsafe.Pointer(&dirent.Name[0]))
	var nameLen uint64
	nameLen, err = direntNamlen(dirent)
	if err != nil {
		return
	}

	// Special cases for common things:
	if nameLen == 1 && nameBuf[0] == '.' {
		name = "."
	} else if nameLen == 2 && nameBuf[0] == '.' && nameBuf[1] == '.' {
		name = ".."
	} else {
		name = string(nameBuf[:nameLen])
	}
	return
}

// According to https://golang.org/doc/go1.14#runtime
// A consequence of the implementation of preemption is that on Unix systems, including Linux and macOS
// systems, programs built with Go 1.14 will receive more signals than programs built with earlier releases.
//
// This causes syscall.Open and syscall.ReadDirent sometimes fail with EINTR errors.
// We need to retry in this case.
func open(path string, mode int, perm uint32) (fd int, err error) {
	for {
		fd, err := syscall.Open(path, mode, perm)
		if err != syscall.EINTR {
			return fd, err
		}
	}
}

func readDirent(fd int, buf []byte) (n int, err error) {
	for {
		nbuf, err := syscall.ReadDirent(fd, buf)
		if err != syscall.EINTR {
			return nbuf, err
		}
	}
}
