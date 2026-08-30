// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package runtimes

import (
	"context"
	"fmt"

	"github.com/latere-ai/replichai/internal/managed"
)

// ModelResolver projects a managed runtime's qualified catalogue onto the
// legacy tier-pricing seam until provider-qualified cards land.
type ModelResolver struct{ Runtime managed.Runtime }

// ResolveModel answers with the bare model id, which is all the unqualified
// credit.ModelResolver seam accepts. Callers that can carry provenance should
// take ResolveModelRef instead.
func (resolver ModelResolver) ResolveModel(ctx context.Context, preference string) (string, error) {
	ref, err := resolver.ResolveModelRef(ctx, preference)
	return ref.ID, err
}

// ResolveModelRef resolves a preference to the qualified model the runtime will
// actually run. A catalogue that maps one preference onto two different models
// is an error rather than an arbitrary pick, because a session is priced on
// that choice being determinate.
func (resolver ModelResolver) ResolveModelRef(ctx context.Context, preference string) (managed.ModelRef, error) {
	models, err := resolver.Runtime.Models(ctx)
	if err != nil {
		return managed.ModelRef{}, err
	}
	var match *managed.ModelRef
	for _, model := range models {
		for _, candidate := range model.Preferences {
			if candidate != preference {
				continue
			}
			if match != nil && *match != model.Ref {
				return managed.ModelRef{}, fmt.Errorf("runtime %q maps preference %q to multiple models", resolver.Runtime.Name(), preference)
			}
			ref := model.Ref
			match = &ref
		}
	}
	if match == nil {
		return managed.ModelRef{}, fmt.Errorf("runtime %q has no model for preference %q", resolver.Runtime.Name(), preference)
	}
	return *match, nil
}
