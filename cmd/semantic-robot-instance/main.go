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

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	stopport "insightos.cn/semantic-robot-deployment/internal/ports/stop"
	"os"
	"time"

	"insightos.cn/semantic-robot-deployment/internal/instance"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "start":
		err = start(os.Args[2:])
	case "debug-stack":
		err = debugStack(os.Args[2:])
	case "render":
		err = render(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "status":
		err = status(os.Args[2:])
	case "stop":
		err = stop(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "semantic-robot-instance:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法: semantic-robot-instance <start|debug-stack|render|run|status|stop> [参数]")
	os.Exit(2)
}

func debugStack(arguments []string) error {
	flags := flag.NewFlagSet("debug-stack", flag.ContinueOnError)
	directory := flags.String("instance", "", "render 后的实例目录")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" {
		return fmt.Errorf("debug-stack 需要 --instance")
	}
	ctx, cancel, err := stopport.NotifyContext(context.Background())
	if err != nil {
		return err
	}
	defer cancel()
	return instance.RunDebugStack(ctx, *directory, os.Stdout)
}

func start(arguments []string) error {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	config := flags.String("config", "", "RobotDeployment YAML")
	dataDirectory := flags.String("data-dir", "", "Robot 实例可写目录；默认使用用户状态目录")
	bundleDirectory := flags.String("bundle", "", "共享只读类型包；默认从当前启动器位置推导")
	joinCode := flags.String("join-code", "", "首次加入 Server 的一次性加入码")
	serverHTTPURL := flags.String("server-http", "", "首次加入使用的 Semantic Server HTTP 地址")
	serverWSURL := flags.String("server-ws", "", "首次加入使用的 Semantic Server WebSocket 地址")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *config == "" {
		return fmt.Errorf("start 需要 --config")
	}
	ctx, cancel, err := stopport.NotifyContext(context.Background())
	if err != nil {
		return err
	}
	defer cancel()
	return instance.Start(ctx, instance.StartOptions{
		DeploymentPath: *config, DataDirectory: *dataDirectory, BundleDirectory: *bundleDirectory,
		JoinCode: *joinCode, ServerHTTPURL: *serverHTTPURL, ServerWSURL: *serverWSURL,
	})
}

func render(arguments []string) error {
	flags := flag.NewFlagSet("render", flag.ContinueOnError)
	config := flags.String("config", "", "RobotInstance YAML")
	output := flags.String("output", "", "实例可写目录")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *config == "" || *output == "" {
		return fmt.Errorf("render 需要 --config 和 --output")
	}
	state, err := instance.Render(*config, *output)
	if err != nil {
		return err
	}
	return printJSON(state)
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	directory := flags.String("instance", "", "render 后的实例目录")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" {
		return fmt.Errorf("run 需要 --instance")
	}
	ctx, cancel, err := stopport.NotifyContext(context.Background())
	if err != nil {
		return err
	}
	defer cancel()
	return instance.Run(ctx, *directory)
}

func status(arguments []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	directory := flags.String("instance", "", "实例目录")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" {
		return fmt.Errorf("status 需要 --instance")
	}
	state, err := instance.InspectStatus(*directory)
	if err != nil {
		return err
	}
	return printJSON(state)
}

func stop(arguments []string) error {
	flags := flag.NewFlagSet("stop", flag.ContinueOnError)
	directory := flags.String("instance", "", "实例目录")
	timeout := flags.Duration("timeout", 30*time.Second, "等待安全停止的最长时间")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" {
		return fmt.Errorf("stop 需要 --instance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return instance.Stop(ctx, *directory)
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
