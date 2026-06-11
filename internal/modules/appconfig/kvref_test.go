package appconfig

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
)

func TestParseKVRef(t *testing.T) {
	ref, err := parseKVRef(`{"uri":"https://myvault.vault.azure.net/secrets/db-password"}`)
	if err != nil {
		t.Fatalf("parseKVRef: %v", err)
	}
	if ref.VaultName != "myvault" || ref.SecretName != "db-password" || ref.Version != "" {
		t.Errorf("unexpected parse: %+v", ref)
	}
	if ref.VaultURL != "https://myvault.vault.azure.net" {
		t.Errorf("vault url: %s", ref.VaultURL)
	}
}

func TestParseKVRefWithVersion(t *testing.T) {
	ref, err := parseKVRef(`{"uri":"https://v.vault.azure.net/secrets/name/abc123"}`)
	if err != nil {
		t.Fatalf("parseKVRef: %v", err)
	}
	if ref.Version != "abc123" {
		t.Errorf("version: %q", ref.Version)
	}
}

func TestParseKVRefRejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		`not json`,
		`{"uri":""}`,
		`{"uri":"https://v.vault.azure.net/keys/not-a-secret"}`,
	} {
		if _, err := parseKVRef(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestDisplayValue(t *testing.T) {
	plain := setting("a", "", "hello", "text/plain")
	if got := displayValue(plain); got != "hello" {
		t.Errorf("plain value: %q", got)
	}
	ref := azappconfig.Setting{
		Key:         to.Ptr("a"),
		Value:       to.Ptr(`{"uri":"https://myvault.vault.azure.net/secrets/db-password"}`),
		ContentType: to.Ptr(kvRefContentType),
	}
	if got := displayValue(ref); got != "🔑 → myvault/db-password" {
		t.Errorf("ref value: %q", got)
	}
}
