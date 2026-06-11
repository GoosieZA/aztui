// Package modules defines aztui's plugin surface. A resource module owns one
// or more ARM resource types and provides the view opened when the user
// selects a matching resource. New modules self-register from an init() in
// their own package; cmd/aztui imports them for side effects.
package modules

import (
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GoosieZA/aztui/internal/azure"
	"github.com/GoosieZA/aztui/internal/config"
)

// Context carries the app-wide dependencies every module needs.
type Context struct {
	Cred   azcore.TokenCredential
	Config *config.Config
}

// Module is one resource integration (App Configuration, Service Bus, ...).
type Module interface {
	// ID is the canonical command name, e.g. ":appconfig".
	ID() string
	// Aliases are alternative command names, e.g. ":ac".
	Aliases() []string
	// Title is the human-readable name shown in tables and breadcrumbs.
	Title() string
	// Icon is a single glyph for the module's tile on the welcome screen.
	Icon() string
	// ResourceTypes are the lowercase ARM types this module opens.
	ResourceTypes() []string
	// Open builds the module's root view for a specific resource instance.
	Open(ctx Context, res azure.Resource) (tea.Model, error)
}

var registry []Module

func Register(m Module) {
	registry = append(registry, m)
	sort.Slice(registry, func(i, j int) bool { return registry[i].ID() < registry[j].ID() })
}

func All() []Module { return registry }

// AllTypes returns every ARM type any registered module can open.
func AllTypes() []string {
	var types []string
	for _, m := range registry {
		types = append(types, m.ResourceTypes()...)
	}
	return types
}

// ForType finds the module owning an ARM resource type, or nil.
func ForType(armType string) Module {
	armType = strings.ToLower(armType)
	for _, m := range registry {
		for _, t := range m.ResourceTypes() {
			if t == armType {
				return m
			}
		}
	}
	return nil
}

// Find resolves a command name or alias to a module, or nil.
func Find(name string) Module {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, m := range registry {
		if m.ID() == name {
			return m
		}
		for _, a := range m.Aliases() {
			if a == name {
				return m
			}
		}
	}
	return nil
}
