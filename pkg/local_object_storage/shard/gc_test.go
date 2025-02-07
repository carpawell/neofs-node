package shard_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	objectV2 "github.com/nspcc-dev/neofs-api-go/v2/object"
	objectCore "github.com/nspcc-dev/neofs-node/pkg/core/object"
	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/blobstor"
	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/blobstor/fstree"
	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/blobstor/peapod"
	meta "github.com/nspcc-dev/neofs-node/pkg/local_object_storage/metabase"
	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/shard"
	"github.com/nspcc-dev/neofs-node/pkg/util"
	cidtest "github.com/nspcc-dev/neofs-sdk-go/container/id/test"
	"github.com/nspcc-dev/neofs-sdk-go/object"
	objectSDK "github.com/nspcc-dev/neofs-sdk-go/object"
	oid "github.com/nspcc-dev/neofs-sdk-go/object/id"
	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestGC_ExpiredObjectWithExpiredLock(t *testing.T) {
	var sh *shard.Shard

	epoch := &epochState{
		Value: 10,
	}

	rootPath := t.TempDir()
	opts := []shard.Option{
		shard.WithLogger(zap.NewNop()),
		shard.WithBlobStorOptions(
			blobstor.WithStorages([]blobstor.SubStorage{
				{
					Storage: peapod.New(
						filepath.Join(rootPath, "blob", "peapod"),
						0600,
						time.Second,
					),
					Policy: func(_ *object.Object, data []byte) bool {
						return len(data) <= 1<<20
					},
				},
				{
					Storage: fstree.New(
						fstree.WithPath(filepath.Join(rootPath, "blob"))),
				},
			}),
		),
		shard.WithMetaBaseOptions(
			meta.WithPath(filepath.Join(rootPath, "meta")),
			meta.WithEpochState(epoch),
		),
		shard.WithDeletedLockCallback(func(_ context.Context, aa []oid.Address) {
			sh.HandleDeletedLocks(aa)
		}),
		shard.WithExpiredLocksCallback(func(_ context.Context, aa []oid.Address) {
			sh.HandleExpiredLocks(aa)
		}),
		shard.WithGCWorkerPoolInitializer(func(sz int) util.WorkerPool {
			pool, err := ants.NewPool(sz)
			require.NoError(t, err)

			return pool
		}),
	}

	sh = shard.New(opts...)
	require.NoError(t, sh.Open())
	require.NoError(t, sh.Init())

	t.Cleanup(func() {
		releaseShard(sh, t)
	})

	cnr := cidtest.ID()

	var expAttr objectSDK.Attribute
	expAttr.SetKey(objectV2.SysAttributeExpEpoch)
	expAttr.SetValue("1")

	obj := generateObjectWithCID(t, cnr)
	obj.SetAttributes(expAttr)
	objID, _ := obj.ID()

	expAttr.SetValue("3")

	lock := generateObjectWithCID(t, cnr)
	lock.SetType(object.TypeLock)
	lock.SetAttributes(expAttr)
	lockID, _ := lock.ID()

	var putPrm shard.PutPrm
	putPrm.SetObject(obj)

	_, err := sh.Put(putPrm)
	require.NoError(t, err)

	err = sh.Lock(cnr, lockID, []oid.ID{objID})
	require.NoError(t, err)

	putPrm.SetObject(lock)
	_, err = sh.Put(putPrm)
	require.NoError(t, err)

	epoch.Value = 5
	sh.NotificationChannel() <- shard.EventNewEpoch(epoch.Value)

	var getPrm shard.GetPrm
	getPrm.SetAddress(objectCore.AddressOf(obj))
	require.Eventually(t, func() bool {
		_, err = sh.Get(getPrm)
		return shard.IsErrNotFound(err)
	}, 3*time.Second, 1*time.Second, "lock expiration should free object removal")
}
