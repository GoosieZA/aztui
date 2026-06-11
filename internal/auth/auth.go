// Package auth builds the credential aztui uses for every Azure call.
//
// The chain tries the Azure CLI first (zero setup if the user has run
// `az login`), then falls back to an interactive browser flow whose tokens
// are persisted so the browser only opens when truly needed.
package auth

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// NewCredential returns the chained credential and a short human-readable
// description of the sources in the chain, for display in the status bar.
func NewCredential() (azcore.TokenCredential, string, error) {
	var chain []azcore.TokenCredential
	var sources []string

	if cli, err := azidentity.NewAzureCLICredential(nil); err == nil {
		chain = append(chain, cli)
		sources = append(sources, "az cli")
	}

	browserOpts := &azidentity.InteractiveBrowserCredentialOptions{}
	if c, ok := persistentCache(); ok {
		browserOpts.Cache = c
	}
	if browser, err := azidentity.NewInteractiveBrowserCredential(browserOpts); err == nil {
		chain = append(chain, browser)
		sources = append(sources, "browser")
	}

	if len(chain) == 0 {
		return nil, "", fmt.Errorf("no usable credential source: install the Azure CLI or allow browser auth")
	}

	cred, err := azidentity.NewChainedTokenCredential(chain, nil)
	if err != nil {
		return nil, "", fmt.Errorf("building credential chain: %w", err)
	}
	return cred, joinArrow(sources), nil
}

func joinArrow(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " → "
		}
		out += p
	}
	return out
}
