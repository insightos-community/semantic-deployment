# Copyright 2026 InsightOS
# SPDX-License-Identifier: Apache-2.0
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

.PHONY: build test lint verify clean

build:
	mkdir -p bin
	go build -trimpath -o bin/semantic-robot-bundle ./cmd/semantic-robot-bundle
	go build -trimpath -o bin/semantic-robot-instance ./cmd/semantic-robot-instance

test:
	go test -race ./...

lint:
	go vet ./...

verify: lint test build

clean:
	rm -rf bin
