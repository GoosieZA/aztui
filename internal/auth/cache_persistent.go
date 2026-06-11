//go:build !darwin || cgo

package auth

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity/cache"
)

// persistentCache returns a token cache shared across runs. On macOS the
// implementation needs CGO for the keychain; this variant covers every
// platform where that's available (and all non-darwin platforms).
func persistentCache() (azidentity.Cache, bool) {
	c, err := cache.New(&cache.Options{Name: "aztui"})
	if err != nil {
		return azidentity.Cache{}, false
	}
	return c, true
}
