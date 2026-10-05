// Copyright 2026 InsightOS
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package instance

import (
	"encoding/json"
	"fmt"
	processport "insightos.cn/semantic-robot-deployment/internal/ports/process"
	stopport "insightos.cn/semantic-robot-deployment/internal/ports/stop"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const stateFilename = "run/state.json"

type Status string

const (
	StatusRendered    Status = "rendered"
	StatusStarting    Status = "starting"
	StatusRunning     Status = "running"
	StatusStopping    Status = "stopping"
	StatusInterrupted Status = "interrupted"
	StatusStopped     Status = "stopped"
	StatusFailed      Status = "failed"
)

type State struct {
	InstanceName        string        `json:"instance_name"`
	RobotID             string        `json:"robot_id"`
	Status              Status        `json:"status"`
	Revision            int64         `json:"revision"`
	SupervisorIdentity  string        `json:"supervisor_identity,omitempty"`
	SupervisorPID       int           `json:"supervisor_pid,omitempty"`
	AbilityFrameworkPID int           `json:"ability_framework_pid,omitempty"`
	PilotPID            int           `json:"pilot_pid,omitempty"`
	AbilityInstanceIDs  []string      `json:"ability_instance_ids,omitempty"`
	StartedAt           time.Time     `json:"started_at,omitempty"`
	UpdatedAt           time.Time     `json:"updated_at"`
	StoppedAt           time.Time     `json:"stopped_at,omitempty"`
	Error               string        `json:"error,omitempty"`
	StopEvidence        *StopEvidence `json:"stop_evidence,omitempty"`
}

type StopEvidence struct {
	ExecutionID          string    `json:"execution_id,omitempty"`
	Safe                 bool      `json:"safe"`
	HoldConfirmed        bool      `json:"hold_confirmed"`
	ActiveInvocations    []string  `json:"active_invocations"`
	Reason               string    `json:"reason"`
	FinishedAt           time.Time `json:"finished_at,omitempty"`
	PilotExitedCleanly   bool      `json:"pilot_exited_cleanly"`
	AbilityStopRequested int       `json:"ability_stop_requested"`
	AbilityStopConfirmed int       `json:"ability_stop_confirmed"`
	RecordedAt           time.Time `json:"recorded_at"`
}

func ReadState(instanceDirectory string) (State, error) {
	data, err := os.ReadFile(filepath.Join(instanceDirectory, stateFilename))
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("解析实例状态失败: %w", err)
	}
	return state, nil
}

func writeState(instanceDirectory string, state *State) error {
	state.Revision++
	state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(instanceDirectory, stateFilename), append(data, '\n'), 0o640)
}

func InspectStatus(instanceDirectory string) (State, error) {
	state, err := ReadState(instanceDirectory)
	if err != nil {
		return State{}, err
	}
	if (state.Status == StatusStarting || state.Status == StatusRunning || state.Status == StatusStopping) &&
		state.SupervisorPID > 0 && !supervisorAlive(state) {
		state.Status = StatusFailed
		state.Error = "supervisor 进程已退出，状态尚未完成收口"
	}
	return state, nil
}

func processAlive(pid int) bool { return processport.Alive(pid) }
func supervisorAlive(state State) bool {
	if !processAlive(state.SupervisorPID) {
		return false
	}
	if state.SupervisorIdentity == "" {
		return true
	}
	actual, err := stopport.Identity(state.SupervisorPID)
	return err == nil && actual == state.SupervisorIdentity
}

func writePID(path string, pid int) error {
	return atomicWrite(path, []byte(strconv.Itoa(pid)+"\n"), 0o640)
}
