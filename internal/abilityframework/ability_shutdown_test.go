// Copyright 2026 InsightOS
// SPDX-License-Identifier: Apache-2.0

package abilityframework

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type asynchronousStops struct {
	requested           []string
	waited              []string
	count               int
	reject, unconfirmed string
}

func (s *asynchronousStops) Stop(ctx context.Context, id string) error {
	s.requested = append(s.requested, id)
	if id == s.reject {
		return errors.New("stop rejected")
	}
	return ctx.Err()
}
func (s *asynchronousStops) WaitStopped(ctx context.Context, id string, timeout time.Duration) error {
	s.waited = append(s.waited, id)
	// Model asynchronous teardown that must not block dispatch to other Abilities.
	if len(s.requested) != s.count {
		return errors.New("other Abilities have not received stop")
	}
	if id == s.unconfirmed {
		return context.DeadlineExceeded
	}
	return ctx.Err()
}
func TestAbilityShutdownDispatchesAllBeforeWaiting(t *testing.T) {
	ids := []string{"navigation", "motion", "gripper", "state", "sensor", "perception", "planning"}
	client := &asynchronousStops{count: len(ids)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	confirmed, failures := StopAll(ctx, client, ids, time.Second)
	if confirmed != 7 || len(failures) != 0 {
		t.Fatalf("confirmed=%d failures=%v", confirmed, failures)
	}
	want := []string{"planning", "perception", "sensor", "state", "gripper", "motion", "navigation"}
	if !reflect.DeepEqual(client.requested, want) || !reflect.DeepEqual(client.waited, want) {
		t.Fatalf("requests=%v waits=%v", client.requested, client.waited)
	}
}
func TestAbilityShutdownKeepsUnconfirmedEvidence(t *testing.T) {
	client := &asynchronousStops{count: 3, reject: "rejected", unconfirmed: "unknown"}
	confirmed, failures := StopAll(context.Background(), client, []string{"ready", "rejected", "unknown"}, time.Second)
	if confirmed != 1 || len(failures) != 2 {
		t.Fatalf("confirmed=%d failures=%v", confirmed, failures)
	}
	if !reflect.DeepEqual(client.waited, []string{"unknown", "ready"}) {
		t.Fatal(client.waited)
	}
}
