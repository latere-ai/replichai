package runtimes

import (
	"context"
	"fmt"

	"github.com/latere-ai/replichai/internal/managed"
)

// ModelResolver projects a managed runtime's qualified catalogue onto the
// legacy tier-pricing seam until provider-qualified cards land.
type ModelResolver struct{ Runtime managed.Runtime }

func (resolver ModelResolver) ResolveModel(ctx context.Context, preference string) (string, error) {
	models, err := resolver.Runtime.Models(ctx)
	if err != nil {
		return "", err
	}
	var match *managed.ModelRef
	for _, model := range models {
		for _, candidate := range model.Preferences {
			if candidate != preference {
				continue
			}
			if match != nil && *match != model.Ref {
				return "", fmt.Errorf("runtime %q maps preference %q to multiple models", resolver.Runtime.Name(), preference)
			}
			ref := model.Ref
			match = &ref
		}
	}
	if match == nil {
		return "", fmt.Errorf("runtime %q has no model for preference %q", resolver.Runtime.Name(), preference)
	}
	return match.ID, nil
}
