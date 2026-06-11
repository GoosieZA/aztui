package azure

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"

	"github.com/GoosieZA/aztui/internal/ui"
)

// DiscoverResources queries Azure Resource Graph across all subscriptions the
// credential can see, returning every resource whose ARM type is in types.
func DiscoverResources(ctx context.Context, cred azcore.TokenCredential, types []string) ([]Resource, error) {
	client, err := armresourcegraph.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating resource graph client: %w", err)
	}

	quoted := make([]string, len(types))
	for i, t := range types {
		quoted[i] = fmt.Sprintf("'%s'", t)
	}
	// Join against resourcecontainers so the UI can show subscription display
	// names instead of GUIDs.
	query := fmt.Sprintf(
		"resources | where type in~ (%s)"+
			" | project id, name, type, resourceGroup, subscriptionId, location, properties"+
			" | join kind=leftouter (resourcecontainers"+
			" | where type =~ 'microsoft.resources/subscriptions'"+
			" | project subscriptionId, subscriptionName = name) on subscriptionId"+
			" | project-away subscriptionId1",
		strings.Join(quoted, ", "),
	)

	var resources []Resource
	var skipToken *string
	for {
		resp, err := client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: to.Ptr(query),
			Options: &armresourcegraph.QueryRequestOptions{
				Top:       to.Ptr[int32](1000),
				SkipToken: skipToken,
			},
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("resource graph query: %w", err)
		}

		rows, ok := resp.Data.([]any)
		if !ok {
			return nil, fmt.Errorf("unexpected resource graph response shape %T", resp.Data)
		}
		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			res := Resource{
				ID:               str(m["id"]),
				Name:             str(m["name"]),
				Type:             strings.ToLower(str(m["type"])),
				ResourceGroup:    str(m["resourceGroup"]),
				SubscriptionID:   str(m["subscriptionId"]),
				SubscriptionName: str(m["subscriptionName"]),
				Location:         str(m["location"]),
			}
			if props, ok := m["properties"].(map[string]any); ok {
				res.Properties = props
			}
			resources = append(resources, res)
		}

		if resp.SkipToken == nil || *resp.SkipToken == "" {
			break
		}
		skipToken = resp.SkipToken
	}

	sort.SliceStable(resources, func(i, j int) bool {
		return ui.NaturalLess(strings.ToLower(resources[i].Name), strings.ToLower(resources[j].Name))
	})
	return resources, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
