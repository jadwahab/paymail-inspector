package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/paymail-inspector/chalker"
	"github.com/mrz1836/paymail-inspector/paymail"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/ttacon/chalk"
)

// resolveCmd represents the resolve command
var resolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Resolves a paymail address",
	Long: chalk.Green.Color(`
                            .__               
_______   ____   __________ |  |___  __ ____  
\_  __ \_/ __ \ /  ___/  _ \|  |\  \/ // __ \ 
 |  | \/\  ___/ \___ (  <_> )  |_\   /\  ___/ 
 |__|    \___  >____  >____/|____/\_/  \___  >
             \/     \/                     \/`) + `
` + chalk.Yellow.Color(`
Resolves a paymail address into a hex-encoded Bitcoin script, address and public profile (if found).

Given a sender and a receiver, where the sender knows the receiver's 
paymail handle <alias>@<domain>.<tld>, the sender can perform Service Discovery against 
the receiver and request a payment destination from the receiver's paymail service.

Read more at: `+chalk.Cyan.Color("http://bsvalias.org/04-01-basic-address-resolution.html")),
	Aliases:    []string{"r", "resolution"},
	SuggestFor: []string{"address", "destination", "payment", "addressing"},
	Example:    configDefault + " resolve this@address.com",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return chalker.Error("resolve requires either a paymail address")
		} else if len(args) > 1 {
			return chalker.Error("resolve only supports one address at a time")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {

		// Extract the parts given
		var senderDomain string
		var senderHandle string
		senderDomain, senderHandle = paymail.ExtractParts(viper.GetString(flagSenderHandle))
		domain, paymailAddress := paymail.ExtractParts(args[0])

		// Did we get a paymail address?
		if len(paymailAddress) == 0 {
			chalker.Log(chalker.ERROR, "paymail address not found or invalid")
			return
		}

		// Validate the paymail address and domain (error already shown)
		if ok := validatePaymailAndDomain(paymailAddress, domain); !ok {
			return
		}

		// No sender handle given? (default: set to the receiver's paymail address)
		if len(senderHandle) == 0 {
			chalker.Log(chalker.WARN, fmt.Sprintf("--%s not set, using: %s", flagSenderHandle, paymailAddress))
			senderHandle = paymailAddress
			senderDomain, senderHandle = paymail.ExtractParts(senderHandle)
		} else { // Sender handle is set (basic validation)

			// Validate the paymail address and domain (error already shown)
			if ok := validatePaymailAndDomain(senderHandle, senderDomain); !ok {
				return
			}
		}

		// Get the capabilities
		capabilities, err := getCapabilities(domain)
		if err != nil {
			if strings.Contains(err.Error(), "context deadline exceeded") {
				chalker.Log(chalker.WARN, fmt.Sprintf("no capabilities found for: %s", domain))
			} else {
				chalker.Log(chalker.ERROR, fmt.Sprintf("error: %s", err.Error()))
			}
			return
		}

		// Set the URL - Does the paymail provider have the capability?
		pkiUrl := capabilities.GetValueString(paymail.BRFCPki, paymail.BRFCPkiAlternate)
		if len(pkiUrl) == 0 {
			chalker.Log(chalker.ERROR, fmt.Sprintf("%s is missing a required capability: %s", domain, paymail.BRFCPki))
			return
		}

		// Set the URL - Does the paymail provider have the capability?
		resolveUrl := capabilities.GetValueString(paymail.BRFCPaymentDestination, paymail.BRFCBasicAddressResolution)
		if len(resolveUrl) == 0 {
			chalker.Log(chalker.ERROR, fmt.Sprintf("%s is missing a required capability: %s", domain, paymail.BRFCPaymentDestination))
			return
		}

		// Does this provider require sender validation?
		// https://bsvalias.org/04-02-sender-validation.html
		if capabilities.GetValueBool(paymail.BRFCSenderValidation, "") {
			chalker.Log(chalker.WARN, "sender validation is ENFORCED")

			// Required if flag is enforced
			if len(signature) == 0 {
				chalker.Log(chalker.ERROR, fmt.Sprintf("missing required flag: %s - see the help section: -h", "--signature"))

				// todo: generate a real signature if possible
				chalker.Log(chalker.WARN, fmt.Sprintf("attempting to fake a signature for: %s...", senderHandle))
				signature, _ = RandomHex(64)
			}

			// Only if it's not the same (set from above ^^)
			if senderHandle != paymailAddress {

				// Get the capabilities
				senderCapabilities, getErr := getCapabilities(senderDomain)
				if getErr != nil {
					if strings.Contains(getErr.Error(), "context deadline exceeded") {
						chalker.Log(chalker.WARN, fmt.Sprintf("no capabilities found for: %s", domain))
					} else {
						chalker.Log(chalker.ERROR, fmt.Sprintf("error: %s", getErr.Error()))
					}
					return
				}

				// Set the URL - Does the paymail provider have the capability?
				senderPkiUrl := senderCapabilities.GetValueString(paymail.BRFCPki, paymail.BRFCPkiAlternate)
				if len(senderPkiUrl) == 0 {
					chalker.Log(chalker.ERROR, fmt.Sprintf("%s is missing a required capability: %s", senderDomain, paymail.BRFCPki))
					return
				}

				// Get the alias of the address
				parts := strings.Split(senderHandle, "@")

				// Get the PKI for the given address
				var senderPki *paymail.PKIResponse
				if senderPki, err = getPki(senderPkiUrl, parts[0], parts[1]); err != nil {
					chalker.Log(chalker.ERROR, fmt.Sprintf("error: %s", err.Error()))
					return
				} else if senderPki != nil {
					chalker.Log(chalker.INFO, fmt.Sprintf("--%s %s@%s's pubkey: %s", flagSenderHandle, parts[0], parts[1], chalk.Cyan.Color(senderPki.PubKey)))
				}
			}

			// once completed, the full sender validation will be complete
			chalker.Log(chalker.SUCCESS, "send request pre-validation: passed")
		}

		// Get the alias of the address
		parts := strings.Split(paymailAddress, "@")

		// Get the PKI for the given address
		var pki *paymail.PKIResponse
		if pki, err = getPki(pkiUrl, parts[0], domain); err != nil {
			chalker.Log(chalker.ERROR, fmt.Sprintf("error: %s", err.Error()))
			return
		}

		// Setup the request body
		senderRequest := &paymail.AddressResolutionRequest{
			Amount:       amount,
			Dt:           time.Now().UTC().Format(time.RFC3339), // UTC is assumed
			Purpose:      purpose,
			SenderHandle: senderHandle,
			SenderName:   viper.GetString(flagSenderName),
			Signature:    signature,
		}

		// Resolve the address from a given paymail
		chalker.Log(chalker.DEFAULT, fmt.Sprintf("resolving address: %s...", chalk.Cyan.Color(parts[0]+"@"+domain)))

		var resolutionResponse *paymail.AddressResolutionResponse
		if resolutionResponse, err = paymail.AddressResolution(resolveUrl, parts[0], domain, senderRequest); err != nil {
			chalker.Log(chalker.ERROR, fmt.Sprintf("address resolution failed: %s", err.Error()))
			return
		}

		// Success
		chalker.Log(chalker.SUCCESS, "address resolution successful")

		// Attempt to get a public profile if the capability is found
		url := capabilities.GetValueString(paymail.BRFCPublicProfile, "")
		if len(url) > 0 && !skipPublicProfile {
			chalker.Log(chalker.DEFAULT, fmt.Sprintf("getting public profile for: %s...", chalk.Cyan.Color(parts[0]+"@"+domain)))
			var profile *paymail.PublicProfileResponse
			if profile, err = paymail.GetPublicProfile(url, parts[0], domain); err != nil {
				chalker.Log(chalker.ERROR, fmt.Sprintf("get public profile failed: %s", err.Error()))
			} else if profile != nil {
				if len(profile.Name) > 0 {
					chalker.Log(chalker.DEFAULT, fmt.Sprintf("name: %s", chalk.Cyan.Color(profile.Name)))
				}
				if len(profile.Avatar) > 0 {
					chalker.Log(chalker.DEFAULT, fmt.Sprintf("avatar: %s", chalk.Cyan.Color(profile.Avatar)))
				}
			}
		}

		// Show pubkey
		if pki != nil && len(pki.PubKey) > 0 {
			chalker.Log(chalker.DEFAULT, fmt.Sprintf("pubkey: %s", chalk.Cyan.Color(pki.PubKey)))
		}

		// Show output script
		chalker.Log(chalker.DEFAULT, fmt.Sprintf("output script: %s", chalk.Cyan.Color(resolutionResponse.Output)))

		// Show the resolved address from the output script
		chalker.Log(chalker.DEFAULT, fmt.Sprintf("address: %s", chalk.Cyan.Color(resolutionResponse.Address)))
	},
}

func init() {
	rootCmd.AddCommand(resolveCmd)

	// Set the amount for the sender request
	resolveCmd.Flags().Uint64VarP(&amount, "amount", "a", 0, "Amount in satoshis for the payment request")

	// Set the purpose for the sender request
	resolveCmd.Flags().StringVarP(&purpose, "purpose", "p", "", "Purpose for the transaction")

	// Set the sender's handle for the sender request
	resolveCmd.PersistentFlags().String(flagSenderHandle, "", "Sender's paymail handle. Required by bsvalias spec. Receiver paymail used if not specified.")
	er(viper.BindPFlag(flagSenderHandle, resolveCmd.PersistentFlags().Lookup(flagSenderHandle)))

	// Set the sender's name for the sender request
	resolveCmd.Flags().String(flagSenderName, "", "The sender's name")
	er(viper.BindPFlag(flagSenderName, resolveCmd.PersistentFlags().Lookup(flagSenderHandle)))

	// Set the signature of the entire request
	resolveCmd.Flags().StringVarP(&signature, "signature", "s", "", "The signature of the entire request")

	// Skip getting the PubKey
	resolveCmd.Flags().BoolVar(&skipPki, "skip-pki", false, "Skip firing pki request and getting the pubkey")

	// Skip getting public profile
	resolveCmd.Flags().BoolVar(&skipPublicProfile, "skip-public-profile", false, "Skip firing public profile request and getting the avatar")
}
