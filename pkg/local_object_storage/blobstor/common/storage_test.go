package common_test

import (
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/blobstor/blobovniczatree"
	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/blobstor/common"
	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/blobstor/peapod"
	oid "github.com/nspcc-dev/neofs-sdk-go/object/id"
	oidtest "github.com/nspcc-dev/neofs-sdk-go/object/id/test"
	"github.com/stretchr/testify/require"
)

func TestCopy(t *testing.T) {
	dir := t.TempDir()
	const nObjects = 100

	src := blobovniczatree.NewBlobovniczaTree(
		blobovniczatree.WithBlobovniczaShallowWidth(2),
		blobovniczatree.WithBlobovniczaShallowDepth(3),
		blobovniczatree.WithRootPath(filepath.Join(dir, "blobovnicza")),
	)

	require.NoError(t, src.Open(false))
	require.NoError(t, src.Init())

	mObjs := make(map[oid.Address][]byte, nObjects)

	for i := 0; i < nObjects; i++ {
		addr := oidtest.Address()
		data := make([]byte, 32)
		rand.Read(data)
		mObjs[addr] = data

		_, err := src.Put(common.PutPrm{
			Address: addr,
			RawData: data,
		})
		require.NoError(t, err)
	}

	require.NoError(t, src.Close())

	dst := peapod.New(filepath.Join(dir, "peapod.db"), 0600, 10*time.Millisecond)

	err := common.Copy(dst, src)
	require.NoError(t, err)

	require.NoError(t, dst.Open(true))
	t.Cleanup(func() { _ = dst.Close() })

	_, err = dst.Iterate(common.IteratePrm{
		Handler: func(el common.IterationElement) error {
			data, ok := mObjs[el.Address]
			require.True(t, ok)
			require.Equal(t, data, el.ObjectData)
			return nil
		},
	})
	require.NoError(t, err)
}
