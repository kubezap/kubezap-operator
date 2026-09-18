/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhookcerts

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Rotator is a controller-runtime Runnable that periodically re-runs Ensure.
//
// It must run on every replica, never leader-gated: each replica serves its
// admission webhook from its own on-disk cert files (via certwatcher), so
// each replica must independently notice when the shared Secret has a newer
// cert than what's on its local disk — not just the replica that happens to
// perform the rotation.
type Rotator struct {
	Client  client.Client
	Options Options
	// Interval between reconcile ticks. Defaults to 1 hour.
	Interval time.Duration
}

// NeedLeaderElection implements manager.LeaderElectionRunnable. Always false —
// see the Rotator doc comment for why this must run on every replica.
func (r *Rotator) NeedLeaderElection() bool {
	return false
}

// Start implements manager.Runnable.
func (r *Rotator) Start(ctx context.Context) error {
	interval := r.Interval
	if interval == 0 {
		interval = time.Hour
	}
	log := logf.FromContext(ctx).WithName("webhookcerts-rotator")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := Ensure(ctx, r.Client, r.Options); err != nil {
				log.Error(err, "failed to reconcile webhook serving certificate")
			}
		}
	}
}
