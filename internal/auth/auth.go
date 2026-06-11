// Package auth builds the credential aztui uses for every Azure call.
//
// The chain tries the Azure CLI first (zero setup if the user has run
// `az login`), then falls back to an interactive browser flow whose tokens
// are persisted so the browser only opens when truly needed.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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

// SignedInUser asks the credential for an ARM token and reads the identity
// claims out of it — no az CLI shell-out needed. Returns "" when the
// identity can't be determined.
func SignedInUser(ctx context.Context, cred azcore.TokenCredential) string {
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return ""
	}
	parts := strings.Split(token.Token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	for _, key := range []string{"upn", "preferred_username", "unique_name", "email", "appid"} {
		if v, ok := claims[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
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
