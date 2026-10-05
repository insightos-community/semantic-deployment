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
	"net"
	"testing"

	"github.com/grandcat/zeroconf"
)

func TestParseDiscoveredServer(t *testing.T) {
	entry := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "Semantic Server"},
		AddrIPv4: []net.IP{net.ParseIP("192.0.2.10")},
		Text:     []string{"server_id=server-a", "display_name=Studio A", "api_version=1", "http_port=8080", "ws_port=8081"}}
	server, ok := parseDiscoveredServer(entry)
	if !ok {
		t.Fatal("合法的 mDNS 广播应被识别")
	}
	if server.HTTPURL != "http://192.0.2.10:8080" || server.WSURL != "ws://192.0.2.10:8081/ws/pilot" {
		t.Fatalf("发现地址错误: %+v", server)
	}
}

func TestParseDiscoveredServerRejectsOtherAPIVersion(t *testing.T) {
	entry := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "Old"},
		AddrIPv4: []net.IP{net.ParseIP("192.0.2.11")}, Text: []string{"api_version=0", "ws_port=8081"}}
	entry.Port = 8080
	if _, ok := parseDiscoveredServer(entry); ok {
		t.Fatal("不兼容的 api_version 不应参与自动选择")
	}
}
