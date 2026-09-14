// Copyright 2026 InsightOS
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"context"
	"fmt"
	"time"
)

type abilityStopClient interface {
	Stop(context.Context, string) error
	WaitStopped(context.Context, string, time.Duration) error
}

// Called only after Pilot hold and exit have been confirmed (or before Pilot
// starts). Dispatch in reverse activation order, then confirm every accepted
// request within the shared deadline. Waiting after each dispatch would add all
// asynchronous teardown delays together and starve later Abilities of a stop request.
func stopAbilities(ctx context.Context, client abilityStopClient, activated []string, timeout time.Duration) (int, []string) {
	var accepted, failures []string
	for index := len(activated) - 1; index >= 0; index-- {
		id := activated[index]
		if err := client.Stop(ctx, id); err != nil {
			failures = append(failures, fmt.Sprintf("停止 Ability %s: %v", id, err))
		} else {
			accepted = append(accepted, id)
		}
	}
	confirmed := 0
	for _, id := range accepted {
		if err := client.WaitStopped(ctx, id, timeout); err != nil {
			failures = append(failures, fmt.Sprintf("确认 Ability %s 停止: %v", id, err))
		} else {
			confirmed++
		}
	}
	return confirmed, failures
}
