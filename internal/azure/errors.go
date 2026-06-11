package azure

import (
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// IsForbidden reports whether an error is an Azure 403 — almost always a
// missing data-plane role on a resource the user can otherwise see, since
// ARM (portal/listing) access and data access are granted separately.
func IsForbidden(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == 403
}
