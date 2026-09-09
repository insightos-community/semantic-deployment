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
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

const semanticServerService = "_semantic-server._tcp"

type discoveredServer struct {
	ID          string
	DisplayName string
	HTTPURL     string
	WSURL       string
}

// discoverSemanticServers 只负责在局域网内找到 Server 地址。加入身份仍由一次性
// join code 建立，mDNS 不承担认证、配置同步或设备状态管理。
func discoverSemanticServers(ctx context.Context) ([]discoveredServer, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	discoveryContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry)
	if err := resolver.Browse(discoveryContext, semanticServerService, "local.", entries); err != nil {
		return nil, err
	}
	var servers []discoveredServer
	for {
		select {
		case entry, open := <-entries:
			if !open {
				return servers, nil
			}
			if server, ok := parseDiscoveredServer(entry); ok {
				servers = append(servers, server)
			}
		case <-discoveryContext.Done():
			sort.Slice(servers, func(i, j int) bool { return servers[i].DisplayName < servers[j].DisplayName })
			return servers, nil
		}
	}
}

func parseDiscoveredServer(entry *zeroconf.ServiceEntry) (discoveredServer, bool) {
	if entry == nil {
		return discoveredServer{}, false
	}
	values := make(map[string]string, len(entry.Text))
	for _, item := range entry.Text {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[key] = value
		}
	}
	if values["api_version"] != "1" {
		return discoveredServer{}, false
	}
	var address net.IP
	if len(entry.AddrIPv4) > 0 {
		address = entry.AddrIPv4[0]
	} else if len(entry.AddrIPv6) > 0 {
		address = entry.AddrIPv6[0]
	}
	if address == nil {
		return discoveredServer{}, false
	}
	httpPort := entry.Port
	if value, err := strconv.Atoi(values["http_port"]); err == nil && value > 0 {
		httpPort = value
	}
	wsPort, err := strconv.Atoi(values["ws_port"])
	if httpPort <= 0 || err != nil || wsPort <= 0 {
		return discoveredServer{}, false
	}
	displayName := values["display_name"]
	if displayName == "" {
		displayName = entry.Instance
	}
	return discoveredServer{ID: values["server_id"], DisplayName: displayName,
		HTTPURL: "http://" + net.JoinHostPort(address.String(), strconv.Itoa(httpPort)),
		WSURL:   "ws://" + net.JoinHostPort(address.String(), strconv.Itoa(wsPort)) + "/ws/pilot"}, true
}

func resolveServerByDiscovery(ctx context.Context, options *StartOptions) error {
	if options.ServerHTTPURL != "" || options.ServerWSURL != "" {
		if options.ServerHTTPURL == "" || options.ServerWSURL == "" {
			return errors.New("手工指定 Server 时必须同时提供 --server-http 和 --server-ws")
		}
		return nil
	}
	servers, err := discoverSemanticServers(ctx)
	if err != nil {
		return fmt.Errorf("发现 Semantic Server: %w", err)
	}
	if len(servers) == 0 {
		return errors.New("局域网内未发现 Semantic Server，请同时提供 --server-http 和 --server-ws")
	}
	if len(servers) > 1 {
		names := make([]string, 0, len(servers))
		for _, server := range servers {
			names = append(names, server.DisplayName+" ("+server.HTTPURL+")")
		}
		return fmt.Errorf("发现多个 Semantic Server，请明确指定地址: %s", strings.Join(names, ", "))
	}
	options.ServerHTTPURL = servers[0].HTTPURL
	options.ServerWSURL = servers[0].WSURL
	return nil
}
