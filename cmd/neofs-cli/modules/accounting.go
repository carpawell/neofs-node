package cmd

import (
	"context"
	"fmt"
	"math"

	"github.com/nspcc-dev/neofs-api-go/pkg/owner"
	accountingsdk "github.com/nspcc-dev/neofs-sdk-go/pkg/api/accounting"
	sdk "github.com/nspcc-dev/neofs-sdk-go/pkg/api/client"
	ownersdk "github.com/nspcc-dev/neofs-sdk-go/pkg/api/owner"
	"github.com/spf13/cobra"
)

var (
	balanceOwner string
)

// accountingCmd represents the accounting command
var accountingCmd = &cobra.Command{
	Use:   "accounting",
	Short: "Operations with accounts and balances",
	Long:  `Operations with accounts and balances`,
}

var accountingBalanceCmd = &cobra.Command{
	Use:   "balance",
	Short: "Get internal balance of NeoFS account",
	Long:  `Get internal balance of NeoFS account`,
	Run: func(cmd *cobra.Command, args []string) {
		var (
			prm sdk.AccountBalancePrm
			res sdk.AccountBalanceRes
			oid *owner.ID

			ctx = context.Background()
		)

		key, err := getKey()
		exitOnErr(cmd, err)

		cli, err := getSDKClient() // new client can not use default private key yet(or it is not planned at all)
		exitOnErr(cmd, err)

		if balanceOwner == "" {
			wallet, err := owner.NEO3WalletFromPublicKey(&key.PublicKey)
			exitOnErr(cmd, err)

			oid = owner.NewIDFromNeo3Wallet(wallet)
		} else {
			oid, err = ownerFromString(balanceOwner)
			exitOnErr(cmd, err)
		}

		var ownerSDK ownersdk.ID // there is no work with wallet in SDK yet(or it is not planned at all)
		var val [25]byte
		copy(val[:], oid.ToV2().GetValue())
		ownerSDK.SetBytes(val)

		prm.SetOwner(ownerSDK)
		prm.SetECDSAPrivateKey(*key)

		err = cli.AccountBalance(ctx, prm, &res)
		exitOnErr(cmd, errf("rpc error : %w", err))

		// print to stdout
		prettyPrintDecimal(cmd, res.NumberOfFunds())
	},
}

func init() {
	rootCmd.AddCommand(accountingCmd)
	accountingCmd.AddCommand(accountingBalanceCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// accountingCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// accountingCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

	accountingBalanceCmd.Flags().StringVar(&balanceOwner, "owner", "", "owner of balance account (omit to use owner from private key)")
}

func prettyPrintDecimal(cmd *cobra.Command, decimal accountingsdk.Decimal) {
	if verbose {
		cmd.Println("value:", decimal.Value())
		cmd.Println("precision:", decimal.Precision())
	} else {
		// divider = 10^{precision}; v:365, p:2 => 365 / 10^2 = 3.65
		divider := math.Pow(10, float64(decimal.Precision()))

		// %0.8f\n for precision 8
		format := fmt.Sprintf("%%0.%df\n", decimal.Precision())

		cmd.Printf(format, float64(decimal.Value())/divider)
	}
}
