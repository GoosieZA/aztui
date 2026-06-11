package appconfig

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
)

// App Configuration represents a Key Vault reference as a setting with this
// content type and a {"uri": "..."} JSON value. The store never resolves the
// secret itself — clients (and their permissions) do.
const kvRefContentType = "application/vnd.microsoft.appconfig.keyvaultref+json"

func isKVRef(s azappconfig.Setting) bool {
	return strings.Contains(strings.ToLower(deref(s.ContentType)), "keyvaultref")
}

type kvRef struct {
	URI        string
	VaultURL   string // https://<vault>.vault.azure.net
	VaultName  string
	SecretName string
	Version    string
}

func parseKVRef(value string) (kvRef, error) {
	var body struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal([]byte(value), &body); err != nil || body.URI == "" {
		return kvRef{}, fmt.Errorf("not a key vault reference payload")
	}
	u, err := url.Parse(body.URI)
	if err != nil {
		return kvRef{}, fmt.Errorf("invalid secret uri %q", body.URI)
	}
	ref := kvRef{
		URI:       body.URI,
		VaultURL:  u.Scheme + "://" + u.Host,
		VaultName: strings.Split(u.Hostname(), ".")[0],
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || !strings.EqualFold(parts[0], "secrets") {
		return kvRef{}, fmt.Errorf("uri %q is not a secret reference", body.URI)
	}
	ref.SecretName = parts[1]
	if len(parts) > 2 {
		ref.Version = parts[2]
	}
	return ref, nil
}

// displayValue is what the settings table shows for a setting: Key Vault
// references render as a pointer instead of raw JSON.
func displayValue(s azappconfig.Setting) string {
	if !isKVRef(s) {
		return deref(s.Value)
	}
	ref, err := parseKVRef(deref(s.Value))
	if err != nil {
		return "🔑 " + deref(s.Value)
	}
	return "🔑 → " + ref.VaultName + "/" + ref.SecretName
}
