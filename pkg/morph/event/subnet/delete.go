package subnetevents

import (
	"fmt"

	"github.com/nspcc-dev/neo-go/pkg/core/transaction"
	"github.com/nspcc-dev/neo-go/pkg/network/payload"
	"github.com/nspcc-dev/neo-go/pkg/rpc/response/result/subscriptions"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"github.com/nspcc-dev/neofs-node/pkg/morph/client"
	"github.com/nspcc-dev/neofs-node/pkg/morph/event"
)

// Delete structures information about the notification generated by Delete method of Subnet contract.
type Delete struct {
	notaryRequest *payload.P2PNotaryRequest

	txHash util.Uint256

	id []byte
}

// MorphEvent implements Neo:Morph Event interface.
func (Delete) MorphEvent() {}

// ID returns identifier of the removed subnet in a binary format of NeoFS API protocol.
func (x Delete) ID() []byte {
	return x.id
}

// TxHash returns hash of the transaction which thrown the notification event.
// Makes sense only in non-notary environments (see NotaryMainTx).
func (x Delete) TxHash() util.Uint256 {
	return x.txHash
}

// NotaryMainTx returns main transaction of the request in the Notary service.
// Returns nil in non-notary environments.
func (x Delete) NotaryMainTx() *transaction.Transaction {
	if x.notaryRequest != nil {
		return x.notaryRequest.MainTransaction
	}

	return nil
}

const itemNumDelete = 1

// ParseDelete parses the notification about the removal of a subnet which has been thrown
// by the appropriate method of the Subnet contract.
//
// Resulting event is of Delete type.
func ParseDelete(e *subscriptions.NotificationEvent) (event.Event, error) {
	var (
		ev  Delete
		err error
	)

	items, err := event.ParseStackArray(e)
	if err != nil {
		return nil, fmt.Errorf("parse stack array: %w", err)
	}

	if ln := len(items); ln != itemNumDelete {
		return nil, event.WrongNumberOfParameters(itemNumDelete, ln)
	}

	// parse ID
	ev.id, err = client.BytesFromStackItem(items[0])
	if err != nil {
		return nil, fmt.Errorf("id item: %w", err)
	}

	ev.txHash = e.Container

	return ev, nil
}

// ParseNotaryDelete parses the notary notification about the removal of a subnet which has been
// thrown by the appropriate method of the Subnet contract.
//
// Resulting event is of Delete type.
func ParseNotaryDelete(e event.NotaryEvent) (event.Event, error) {
	var ev Delete

	ev.notaryRequest = e.Raw()
	if ev.notaryRequest == nil {
		panic(fmt.Sprintf("nil %T in notary environment", ev.notaryRequest))
	}

	var (
		err error

		prms = e.Params()
	)

	if ln := len(prms); ln != itemNumDelete {
		return nil, event.WrongNumberOfParameters(itemNumDelete, ln)
	}

	ev.id, err = event.BytesFromOpcode(prms[0])
	if err != nil {
		return nil, fmt.Errorf("id param: %w", err)
	}

	ev.notaryRequest = e.Raw()

	return ev, nil
}
