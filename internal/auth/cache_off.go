//go:build darwin && !cgo

package auth

import "github.com/Azure/azure-sdk-for-go/sdk/azidentity"

// persistentCache is unavailable in CGO-free darwin builds (the macOS
// keychain accessor requires CGO). Browser-flow tokens then live only in
// memory for the process lifetime; the Azure CLI credential path is
// unaffected.
func persistentCache() (azidentity.Cache, bool) {
	return azidentity.Cache{}, false
}
