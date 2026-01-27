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

package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	zapraw "go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type CLI struct {
	API      APICmd      `cmd:"" help:"Run API server"`
	Operator OperatorCmd `cmd:"" help:"Run K8s operator"`
	GitHub   GitHubCmd   `cmd:"" name:"github" help:"Run GitHub adapter"`

	LogLevel int  `help:"Log level (0=info, 1=debug)" default:"0"`
	DevMode  bool `help:"Enable development mode logging" default:"false"`
}

type APICmd struct {
	ListenAddr string `help:"API listen address" default:":8080" env:"SHEPHERD_API_ADDR"`
}

func (c *APICmd) Run(globals *CLI) error {
	return fmt.Errorf("api server not implemented yet")
}

type GitHubCmd struct {
	ListenAddr    string `help:"GitHub adapter listen address" default:":8082" env:"SHEPHERD_GITHUB_ADDR"`
	WebhookSecret string `help:"GitHub webhook secret" env:"SHEPHERD_GITHUB_WEBHOOK_SECRET"`
	AppID         int64  `help:"GitHub App ID" env:"SHEPHERD_GITHUB_APP_ID"`
	PrivateKey    string `help:"Path to GitHub App private key" env:"SHEPHERD_GITHUB_PRIVATE_KEY"`
}

func (c *GitHubCmd) Run(globals *CLI) error {
	return fmt.Errorf("github adapter not implemented yet")
}

func main() {
	cli := CLI{}
	ctx := kong.Parse(&cli,
		kong.Name("shepherd"),
		kong.Description("Background coding agent orchestrator"),
	)

	// Configure logging
	logger := zap.New(
		zap.UseDevMode(cli.DevMode),
		zap.Level(zapraw.Level(-cli.LogLevel)),
	)
	log.SetLogger(logger)

	err := ctx.Run(&cli)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
