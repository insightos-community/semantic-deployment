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
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"insightos.cn/semantic-robot-deployment/internal/bundle"
)

type fileMappings []string

func (values *fileMappings) String() string { return fmt.Sprint([]string(*values)) }
func (values *fileMappings) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = build(os.Args[2:])
	case "inspect":
		err = inspect(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "semantic-robot-bundle:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法: semantic-robot-bundle <build|inspect> [参数]")
	os.Exit(2)
}

func build(arguments []string) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	source := flags.String("source", "", "R1 Pro 类型包模板目录")
	output := flags.String("output", "", "只读 bundle 输出目录")
	python := flags.String("python", "python3", "创建 bundle 共享 Python 环境的解释器")
	wheelDirectory := flags.String("wheel-dir", "", "按 bundle.yaml 中的精确文件名从目录补齐 Python Wheel")
	var rawMappings fileMappings
	flags.Var(&rawMappings, "file", "bundle/relative/path=/existing/artifact，可重复")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *source == "" || *output == "" {
		return fmt.Errorf("build 需要 --source 和 --output")
	}
	mappings := make([]bundle.FileMapping, 0, len(rawMappings))
	for _, raw := range rawMappings {
		mapping, err := bundle.ParseFileMapping(raw)
		if err != nil {
			return err
		}
		mappings = append(mappings, mapping)
	}
	built, err := bundle.BuildWithOptions(*source, *output, mappings,
		bundle.BuildOptions{PythonExecutable: *python, WheelDirectory: *wheelDirectory})
	if err != nil {
		return err
	}
	result, err := bundle.Inspect(built.Root)
	if err != nil {
		return err
	}
	return printJSON(result)
}

func inspect(arguments []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	directory := flags.String("bundle", "", "bundle 目录")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" {
		return fmt.Errorf("inspect 需要 --bundle")
	}
	result, err := bundle.Inspect(*directory)
	if err != nil {
		return err
	}
	return printJSON(result)
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
