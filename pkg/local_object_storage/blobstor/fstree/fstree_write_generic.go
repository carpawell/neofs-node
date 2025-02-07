//go:build !linux

package fstree

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"syscall"

	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/blobstor/common"
)

func (t *FSTree) writeData(p string, data []byte) error {
	// Here is a situation:
	// Feb 09 13:10:37 buky neofs-node[32445]: 2023-02-09T13:10:37.161Z        info        log/log.go:13        local object storage operation        {"shard_id": "SkT8BfjouW6t93oLuzQ79s", "address": "7NxFz4SruSi8TqXacr2Ae22nekMhgYk1sfkddJo9PpWk/5enyUJGCyU1sfrURDnHEjZFdbGqANVhayYGfdSqtA6wA", "op": "PUT", "type": "fstree", "storage_id": ""}
	// Feb 09 13:10:37 buky neofs-node[32445]: 2023-02-09T13:10:37.183Z        info        log/log.go:13        local object storage operation        {"shard_id": "SkT8BfjouW6t93oLuzQ79s", "address": "7NxFz4SruSi8TqXacr2Ae22nekMhgYk1sfkddJo9PpWk/5enyUJGCyU1sfrURDnHEjZFdbGqANVhayYGfdSqtA6wA", "op": "metabase PUT"}
	// Feb 09 13:10:37 buky neofs-node[32445]: 2023-02-09T13:10:37.862Z        debug        policer/check.go:231        shortage of object copies detected        {"component": "Object Policer", "object": "7NxFz4SruSi8TqXacr2Ae22nekMhgYk1sfkddJo9PpWk/5enyUJGCyU1sfrURDnHEjZFdbGqANVhayYGfdSqtA6wA", "shortage": 1}
	// Feb 09 13:10:37 buky neofs-node[32445]: 2023-02-09T13:10:37.862Z        debug        shard/get.go:124        object is missing in write-cache        {"shard_id": "SkT8BfjouW6t93oLuzQ79s", "addr": "7NxFz4SruSi8TqXacr2Ae22nekMhgYk1sfkddJo9PpWk/5enyUJGCyU1sfrURDnHEjZFdbGqANVhayYGfdSqtA6wA", "skip_meta": false}
	//
	// 1. We put an object on node 1.
	// 2. Relentless policer sees that it has only 1 copy and tries to PUT it to node 2.
	// 3. PUT operation started by client at (1) also puts an object here.
	// 4. Now we have concurrent writes and one of `Rename` calls will return `no such file` error.
	//    Even more than that, concurrent writes can corrupt data.
	//
	// So here is a solution:
	// 1. Write a file to 'name + 1'.
	// 2. If it exists, retry with temporary name being 'name + 2'.
	// 3. Set some reasonable number of attempts.
	//
	// It is a bit kludgey, but I am unusually proud about having found this out after
	// hours of research on linux kernel, dirsync mount option and ext4 FS, turned out
	// to be so hecking simple.
	// In a very rare situation we can have multiple partially written copies on disk,
	// this will be fixed in another issue (we should remove garbage on start).
	const retryCount = 5
	for i := 0; i < retryCount; i++ {
		tmpPath := p + "#" + strconv.FormatUint(uint64(i), 10)
		err := t.writeAndRename(tmpPath, p, data)
		if err != syscall.EEXIST || i == retryCount-1 {
			return err
		}
	}

	// unreachable, but precaution never hurts, especially 1 day before release.
	return fmt.Errorf("couldn't read file after %d retries", retryCount)
}

// writeAndRename opens tmpPath exclusively, writes data to it and renames it to p.
func (t *FSTree) writeAndRename(tmpPath, p string, data []byte) error {
	err := t.writeFile(tmpPath, data)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			switch pe.Err {
			case syscall.ENOSPC:
				err = common.ErrNoSpace
				_ = os.RemoveAll(tmpPath)
			case syscall.EEXIST:
				return syscall.EEXIST
			}
		}
	} else {
		err = os.Rename(tmpPath, p)
	}
	return err
}

func (t *FSTree) writeFlags() int {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC | os.O_EXCL
	if t.noSync {
		return flags
	}
	return flags | os.O_SYNC
}

// writeFile writes data to a file with path p.
// The code is copied from `os.WriteFile` with minor corrections for flags.
func (t *FSTree) writeFile(p string, data []byte) error {
	f, err := os.OpenFile(p, t.writeFlags(), t.Permissions)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err1 := f.Close(); err1 != nil && err == nil {
		err = err1
	}
	return err
}
