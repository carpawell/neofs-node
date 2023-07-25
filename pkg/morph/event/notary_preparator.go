package event

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/nspcc-dev/neo-go/pkg/core/interop/interopnames"
	"github.com/nspcc-dev/neo-go/pkg/core/transaction"
	"github.com/nspcc-dev/neo-go/pkg/crypto/hash"
	"github.com/nspcc-dev/neo-go/pkg/crypto/keys"
	"github.com/nspcc-dev/neo-go/pkg/network/payload"
	"github.com/nspcc-dev/neo-go/pkg/smartcontract"
	"github.com/nspcc-dev/neo-go/pkg/smartcontract/callflag"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"github.com/nspcc-dev/neo-go/pkg/vm"
	"github.com/nspcc-dev/neo-go/pkg/vm/opcode"
	"github.com/nspcc-dev/neofs-node/pkg/morph/client"
)

var (
	errNotContractCall            = errors.New("received main tx is not a contract call")
	errUnexpectedWitnessAmount    = errors.New("received main tx has unexpected amount of witnesses")
	errUnexpectedCosignersAmount  = errors.New("received main tx has unexpected amount of cosigners")
	errIncorrectAlphabetSigner    = errors.New("received main tx has incorrect Alphabet signer")
	errIncorrectProxyWitnesses    = errors.New("received main tx has non-empty Proxy witnesses")
	errIncorrectInvokerWitnesses  = errors.New("received main tx has empty Invoker witness")
	errIncorrectAlphabet          = errors.New("received main tx has incorrect Alphabet verification")
	errIncorrectNotaryPlaceholder = errors.New("received main tx has incorrect Notary contract placeholder")
	errIncorrectAttributesAmount  = errors.New("received main tx has incorrect attributes amount")
	errIncorrectAttribute         = errors.New("received main tx has incorrect attribute")
	errIncorrectCallFlag          = errors.New("received main tx has unexpected call flag")
	errIncorrectArgPacking        = errors.New("received main tx has incorrect argument packing")
	errUnexpectedCONVERT          = errors.New("received main tx has unexpected CONVERT opcode")

	errIncorrectFBAttributesAmount = errors.New("received fallback tx has incorrect attributes amount")
	errIncorrectFBAttributes       = errors.New("received fallback tx has incorrect attributes")

	// ErrTXAlreadyHandled is returned if received TX has already been signed.
	ErrTXAlreadyHandled = errors.New("received main tx has already been handled")

	// ErrMainTXExpired is returned if received fallback TX is already valid.
	ErrMainTXExpired = errors.New("received main tx has expired")
)

// BlockCounter must return block count of the network
// from which notary requests are received.
type BlockCounter interface {
	BlockCount() (res uint32, err error)
}

// PreparatorPrm groups the required parameters of the preparator constructor.
type PreparatorPrm struct {
	AlphaKeys client.AlphabetKeys

	// BlockCount must return block count of the network
	// from which notary requests are received.
	BlockCounter BlockCounter
}

// preparator constructs NotaryEvent
// from the NotaryRequest event.
type preparator struct {
	// contractSysCall contract call in NeoVM
	contractSysCall []byte
	// dummyInvocationScript is invocation script from TX that is not signed.
	dummyInvocationScript []byte

	alphaKeys client.AlphabetKeys

	blockCounter BlockCounter
}

// notaryPreparator inits and returns preparator.
//
// Considered to be used for preparing notary request
// for parsing it by event.Listener.
func notaryPreparator(prm PreparatorPrm) preparator {
	switch {
	case prm.AlphaKeys == nil:
		panic("alphabet keys source must not be nil")
	case prm.BlockCounter == nil:
		panic("block counter must not be nil")
	}

	contractSysCall := make([]byte, 4)
	binary.LittleEndian.PutUint32(contractSysCall, interopnames.ToID([]byte(interopnames.SystemContractCall)))

	dummyInvocationScript := append([]byte{byte(opcode.PUSHDATA1), 64}, make([]byte, 64)...)

	return preparator{
		contractSysCall:       contractSysCall,
		dummyInvocationScript: dummyInvocationScript,
		alphaKeys:             prm.AlphaKeys,
		blockCounter:          prm.BlockCounter,
	}
}

// Prepare converts raw notary requests to NotaryEvent.
//
// Returns ErrTXAlreadyHandled if transaction shouldn't be
// parsed and handled. It is not "error case". Every handled
// transaction is expected to be received one more time
// from the Notary service but already signed. This happens
// since every notary call is a new notary request in fact.
func (p preparator) Prepare(nr *payload.P2PNotaryRequest) (NotaryEvent, error) {
	// notary request's main tx is expected to have
	// three or four witnesses: one for proxy contract,
	// one for alphabet multisignature, one optional for
	// notary's invoker and one is for notary  contract
	ln := len(nr.MainTransaction.Scripts)
	switch ln {
	case 3, 4:
	default:
		return nil, errUnexpectedWitnessAmount
	}
	invokerWitness := ln == 4

	// alphabet node should handle only notary requests that do not yet have inner
	// ring multisignature filled => such main TXs either have empty invocation script
	// of the inner ring witness (in case if Notary Actor is used to create request)
	// or have it filled with dummy bytes (if request was created manually with the old
	// neo-go API)
	//
	// this check prevents notary flow recursion
	if !(len(nr.MainTransaction.Scripts[1].InvocationScript) == 0 ||
		bytes.Equal(nr.MainTransaction.Scripts[1].InvocationScript, p.dummyInvocationScript)) { // compatibility with old version
		return nil, ErrTXAlreadyHandled
	}

	currentAlphabet, err := p.alphaKeys()
	if err != nil {
		return nil, fmt.Errorf("could not fetch Alphabet public keys: %w", err)
	}

	err = p.validateCosigners(ln, nr.MainTransaction.Signers, currentAlphabet)
	if err != nil {
		return nil, err
	}

	// validate main TX's notary attribute
	err = p.validateAttributes(nr.MainTransaction.Attributes, currentAlphabet, invokerWitness)
	if err != nil {
		return nil, err
	}

	// validate main TX's witnesses
	err = p.validateWitnesses(nr.MainTransaction.Scripts, currentAlphabet, invokerWitness)
	if err != nil {
		return nil, err
	}

	// validate main TX expiration
	err = p.validateExpiration(nr.FallbackTransaction)
	if err != nil {
		return nil, err
	}

	var (
		opCode opcode.Opcode
		param  []byte
	)

	ctx := vm.NewContext(nr.MainTransaction.Script)
	ops := make([]Op, 0, 10) // 10 is maximum num of opcodes for calling contracts with 4 args(no arrays of arrays)

	for {
		opCode, param, err = ctx.Next()
		if err != nil {
			return nil, fmt.Errorf("could not get next opcode in script: %w", err)
		}

		if opCode == opcode.RET {
			break
		}

		ops = append(ops, Op{code: opCode, param: param})
	}

	opsLen := len(ops)

	// check if it is tx with contract call
	if !bytes.Equal(ops[opsLen-1].param, p.contractSysCall) {
		return nil, errNotContractCall
	}

	// retrieve contract's script hash
	contractHash, err := util.Uint160DecodeBytesBE(ops[opsLen-2].param)
	if err != nil {
		return nil, fmt.Errorf("could not decode contract hash: %w", err)
	}

	// retrieve contract's method
	contractMethod := string(ops[opsLen-3].param)

	// check if there is a call flag(must be in range [0:15))
	callFlag := callflag.CallFlag(ops[opsLen-4].code - opcode.PUSH0)
	if callFlag > callflag.All {
		return nil, errIncorrectCallFlag
	}

	args := ops[:opsLen-4]

	if len(args) != 0 {
		err = p.validateParameterOpcodes(args)
		if err != nil {
			return nil, fmt.Errorf("could not validate arguments: %w", err)
		}

		// without args packing opcodes
		args = args[:len(args)-2]
	}

	return parsedNotaryEvent{
		hash:       contractHash,
		notaryType: NotaryTypeFromString(contractMethod),
		params:     args,
		raw:        nr,
	}, nil
}

func (p preparator) validateParameterOpcodes(ops []Op) error {
	l := len(ops)

	if ops[l-1].code != opcode.PACK {
		return fmt.Errorf("unexpected packing opcode: %s", ops[l-1].code)
	}

	argsLen, err := IntFromOpcode(ops[l-2])
	if err != nil {
		return fmt.Errorf("could not parse argument len: %w", err)
	}

	err = validateNestedArgs(argsLen, ops[:l-2])
	if err != nil {
		return err
	}

	return nil
}

func validateNestedArgs(expArgLen int64, ops []Op) error {
	var (
		currentCode opcode.Opcode

		opsLenGot = len(ops)
	)

	for i := opsLenGot - 1; i >= 0; i-- {
		// only PUSH(also, PACK for arrays and CONVERT for booleans)
		// codes are allowed; number of params and their content must
		// be checked in a notary parser and a notary handler of a
		// particular contract
		switch currentCode = ops[i].code; {
		case currentCode <= opcode.PUSH16:
		case currentCode == opcode.CONVERT:
			if i == 0 || ops[i-1].code != opcode.PUSHT && ops[i-1].code != opcode.PUSHF {
				return errUnexpectedCONVERT
			}

			expArgLen++
		case currentCode == opcode.PACK:
			if i == 0 {
				return errIncorrectArgPacking
			}

			argsLen, err := IntFromOpcode(ops[i-1])
			if err != nil {
				return fmt.Errorf("could not parse argument len: %w", err)
			}

			expArgLen += argsLen + 1
			i--
		default:
			return fmt.Errorf("received main tx has unexpected(not PUSH) NeoVM opcode: %s", currentCode)
		}
	}

	if int64(opsLenGot) != expArgLen {
		return errIncorrectArgPacking
	}

	return nil
}

func (p preparator) validateExpiration(fbTX *transaction.Transaction) error {
	if len(fbTX.Attributes) != 3 {
		return errIncorrectFBAttributesAmount
	}

	nvbAttrs := fbTX.GetAttributes(transaction.NotValidBeforeT)
	if len(nvbAttrs) != 1 {
		return errIncorrectFBAttributes
	}

	nvb, ok := nvbAttrs[0].Value.(*transaction.NotValidBefore)
	if !ok {
		return errIncorrectFBAttributes
	}

	currBlock, err := p.blockCounter.BlockCount()
	if err != nil {
		return fmt.Errorf("could not fetch current chain height: %w", err)
	}

	if currBlock >= nvb.Height {
		return ErrMainTXExpired
	}

	return nil
}

func (p preparator) validateCosigners(expected int, s []transaction.Signer, alphaKeys keys.PublicKeys) error {
	if len(s) != expected {
		return errUnexpectedCosignersAmount
	}

	alphaVerificationScript, err := smartcontract.CreateMultiSigRedeemScript(len(alphaKeys)*2/3+1, alphaKeys)
	if err != nil {
		return fmt.Errorf("could not get Alphabet verification script: %w", err)
	}

	if !s[1].Account.Equals(hash.Hash160(alphaVerificationScript)) {
		return errIncorrectAlphabetSigner
	}

	return nil
}

func (p preparator) validateWitnesses(w []transaction.Witness, alphaKeys keys.PublicKeys, invokerWitness bool) error {
	// the first one(proxy contract) must have empty
	// witnesses
	if len(w[0].VerificationScript)+len(w[0].InvocationScript) != 0 {
		return errIncorrectProxyWitnesses
	}

	alphaVerificationScript, err := smartcontract.CreateMultiSigRedeemScript(len(alphaKeys)*2/3+1, alphaKeys)
	if err != nil {
		return fmt.Errorf("could not get Alphabet verification script: %w", err)
	}

	// the second one must be witness of the current
	// alphabet multiaccount
	if !bytes.Equal(w[1].VerificationScript, alphaVerificationScript) {
		return errIncorrectAlphabet
	}

	if invokerWitness {
		// the optional third one must be an invoker witness
		if len(w[2].VerificationScript)+len(w[2].InvocationScript) == 0 {
			return errIncorrectInvokerWitnesses
		}
	}

	// the last one must be a placeholder for notary contract witness
	last := len(w) - 1
	if !(len(w[last].InvocationScript) == 0 || // https://github.com/nspcc-dev/neo-go/pull/2981
		bytes.Equal(w[last].InvocationScript, p.dummyInvocationScript)) || // compatibility with old version
		len(w[last].VerificationScript) != 0 {
		return errIncorrectNotaryPlaceholder
	}

	return nil
}

func (p preparator) validateAttributes(aa []transaction.Attribute, alphaKeys keys.PublicKeys, invokerWitness bool) error {
	// main tx must have exactly one attribute
	if len(aa) != 1 {
		return errIncorrectAttributesAmount
	}

	expectedN := uint8(len(alphaKeys))
	if invokerWitness {
		expectedN++
	}

	val, ok := aa[0].Value.(*transaction.NotaryAssisted)
	if !ok || val.NKeys != expectedN {
		return errIncorrectAttribute
	}

	return nil
}

type parsedNotaryEvent struct {
	hash       util.Uint160
	notaryType NotaryType
	params     []Op
	raw        *payload.P2PNotaryRequest
}

func (p parsedNotaryEvent) ScriptHash() util.Uint160 {
	return p.hash
}

func (p parsedNotaryEvent) Type() NotaryType {
	return p.notaryType
}

func (p parsedNotaryEvent) Params() []Op {
	return p.params
}

func (p parsedNotaryEvent) Raw() *payload.P2PNotaryRequest {
	return p.raw
}
