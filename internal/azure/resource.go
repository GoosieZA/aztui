// Package azure holds shared types and helpers for talking to Azure
// management-plane APIs.
package azure

import (
	"fmt"
	"net/url"
	"strings"
)

// Resource is a discovered Azure resource, as returned by Resource Graph.
type Resource struct {
	ID               string         `yaml:"id"`
	Name             string         `yaml:"name"`
	Type             string         `yaml:"type"` // lowercase ARM type, e.g. "microsoft.appconfiguration/configurationstores"
	ResourceGroup    string         `yaml:"resourceGroup"`
	SubscriptionID   string         `yaml:"subscriptionId"`
	SubscriptionName string         `yaml:"subscriptionName"`
	Location         string         `yaml:"location"`
	Properties       map[string]any `yaml:"-"`
}

// Property returns a string property from the resource's properties bag.
func (r Resource) Property(key string) string {
	if r.Properties == nil {
		return ""
	}
	if v, ok := r.Properties[key].(string); ok {
		return v
	}
	return ""
}

// Endpoint returns the data-plane endpoint for resource types that expose one
// (App Configuration stores, Service Bus namespaces). The second return is the
// bare host name, useful for SDK clients that want a fully qualified namespace.
func (r Resource) Endpoint() (string, string, error) {
	var raw string
	switch {
	case strings.Contains(r.Type, "appconfiguration"):
		raw = r.Property("endpoint")
	case strings.Contains(r.Type, "servicebus"):
		raw = r.Property("serviceBusEndpoint")
	case strings.Contains(r.Type, "keyvault"):
		raw = r.Property("vaultUri")
	}
	if raw == "" {
		return "", "", fmt.Errorf("resource %s has no endpoint in its properties", r.Name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parsing endpoint %q: %w", raw, err)
	}
	return strings.TrimSuffix(raw, "/"), u.Hostname(), nil
}
