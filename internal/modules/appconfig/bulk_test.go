package appconfig

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
)

func setting(key, label, value, contentType string) azappconfig.Setting {
	s := azappconfig.Setting{Key: to.Ptr(key), Value: to.Ptr(value)}
	if label != "" {
		s.Label = to.Ptr(label)
	}
	if contentType != "" {
		s.ContentType = to.Ptr(contentType)
	}
	return s
}

func originals(settings ...azappconfig.Setting) map[string]azappconfig.Setting {
	m := make(map[string]azappconfig.Setting, len(settings))
	for _, s := range settings {
		m[bulkKey(deref(s.Key), deref(s.Label))] = s
	}
	return m
}

func TestBuildPlanDiff(t *testing.T) {
	orig := originals(
		setting("a", "", "1", ""),
		setting("b", "prod", "2", "text/plain"),
		setting("c", "", "3", ""),
	)
	entries := []bulkEntry{
		{Key: "a", Value: "1"}, // unchanged
		{Key: "b", Label: "prod", Value: "2x", ContentType: "text/plain"}, // value changed
		{Key: "d", Value: "4"}, // new
		// "c" removed → delete
	}

	plan, err := buildPlan(orig, entries)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if len(plan.updates) != 1 || plan.updates[0].entry.Key != "b" {
		t.Errorf("expected exactly one update for b, got %+v", plan.updates)
	}
	if len(plan.creates) != 1 || plan.creates[0].Key != "d" {
		t.Errorf("expected exactly one create for d, got %+v", plan.creates)
	}
	if len(plan.deletes) != 1 || deref(plan.deletes[0].Key) != "c" {
		t.Errorf("expected exactly one delete for c, got %+v", plan.deletes)
	}
}

func TestBuildPlanContentTypeOnlyChange(t *testing.T) {
	orig := originals(setting("a", "", "1", ""))
	plan, err := buildPlan(orig, []bulkEntry{{Key: "a", Value: "1", ContentType: "application/json"}})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if len(plan.updates) != 1 {
		t.Errorf("content-type change should count as an update, got %+v", plan.updates)
	}
}

func TestBuildPlanLabelDistinguishesSettings(t *testing.T) {
	// Same key, different label = different setting: editing one must not
	// delete the other when it wasn't selected.
	orig := originals(setting("a", "dev", "1", ""))
	plan, err := buildPlan(orig, []bulkEntry{{Key: "a", Label: "prod", Value: "1"}})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if len(plan.creates) != 1 || len(plan.deletes) != 1 {
		t.Errorf("label change should be create+delete, got creates=%+v deletes=%+v", plan.creates, plan.deletes)
	}
}

func TestBuildPlanRejectsBadInput(t *testing.T) {
	if _, err := buildPlan(originals(), []bulkEntry{{Key: "", Value: "x"}}); err == nil {
		t.Error("empty key should be rejected")
	}
	if _, err := buildPlan(originals(), []bulkEntry{
		{Key: "a", Value: "1"},
		{Key: "a", Value: "2"},
	}); err == nil {
		t.Error("duplicate key+label should be rejected")
	}
}

func TestBuildPlanNoChanges(t *testing.T) {
	orig := originals(setting("a", "", "1", "text/plain"))
	plan, err := buildPlan(orig, []bulkEntry{{Key: "a", Value: "1", ContentType: "text/plain"}})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if !plan.empty() {
		t.Errorf("identical entries should produce an empty plan, got %s", plan.summary())
	}
}
