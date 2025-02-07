package blobovnicza

import (
	common "github.com/nspcc-dev/neofs-node/cmd/neofs-lens/internal"
	"github.com/nspcc-dev/neofs-node/pkg/local_object_storage/blobovnicza"
	"github.com/nspcc-dev/neofs-sdk-go/object"
	oid "github.com/nspcc-dev/neofs-sdk-go/object/id"
	"github.com/spf13/cobra"
)

var inspectCMD = &cobra.Command{
	Use:   "inspect",
	Short: "Object inspection",
	Long:  `Inspect specific object in a blobovnicza.`,
	Run:   inspectFunc,
}

func init() {
	common.AddAddressFlag(inspectCMD, &vAddress)
	common.AddComponentPathFlag(inspectCMD, &vPath)
	common.AddOutputFileFlag(inspectCMD, &vOut)
	common.AddPayloadOnlyFlag(inspectCMD, &vPayloadOnly)
}

func inspectFunc(cmd *cobra.Command, _ []string) {
	var addr oid.Address

	err := addr.DecodeString(vAddress)
	common.ExitOnErr(cmd, common.Errf("invalid address argument: %w", err))

	blz := openBlobovnicza(cmd)
	defer blz.Close()

	var prm blobovnicza.GetPrm
	prm.SetAddress(addr)

	res, err := blz.Get(prm)
	common.ExitOnErr(cmd, common.Errf("could not fetch object: %w", err))

	data := res.Object()

	var o object.Object
	common.ExitOnErr(cmd, common.Errf("could not unmarshal object: %w",
		o.Unmarshal(data)),
	)

	common.PrintObjectHeader(cmd, o)
	if vPayloadOnly {
		data = o.Payload()
		common.WriteObjectToFile(cmd, vOut, data, true)
		return
	}
	common.WriteObjectToFile(cmd, vOut, data, false)
}
