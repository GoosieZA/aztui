// Package storage is the aztui module for Azure Storage accounts: browse
// containers and blobs hierarchically, download blobs, and peek storage
// queue messages. Read-focused by design.
package storage

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/modules"
)

type module struct{}

func init() { modules.Register(module{}) }

func (module) ID() string        { return "storage" }
func (module) Aliases() []string { return []string{"st", "blob"} }
func (module) Title() string     { return "Storage" }
func (module) Icon() string      { return "🗄" }

func (module) ResourceTypes() []string {
	return []string{"microsoft.storage/storageaccounts"}
}

func endpoints(res azure.Resource) (blobURL, queueURL string, err error) {
	pe, _ := res.Properties["primaryEndpoints"].(map[string]any)
	if pe == nil {
		return "", "", fmt.Errorf("storage account %s has no primaryEndpoints", res.Name)
	}
	blobURL, _ = pe["blob"].(string)
	queueURL, _ = pe["queue"].(string)
	if blobURL == "" {
		return "", "", fmt.Errorf("storage account %s has no blob endpoint", res.Name)
	}
	return blobURL, queueURL, nil
}

func (module) Open(mctx modules.Context, res azure.Resource) (tea.Model, error) {
	blobURL, queueURL, err := endpoints(res)
	if err != nil {
		return nil, err
	}
	blob, err := azblob.NewClient(blobURL, mctx.Cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating blob client for %s: %w", res.Name, err)
	}
	var queues *azqueue.ServiceClient
	if queueURL != "" {
		if q, err := azqueue.NewServiceClient(queueURL, mctx.Cred, nil); err == nil {
			queues = q
		}
	}
	return newRootView(res, blob, queues), nil
}
