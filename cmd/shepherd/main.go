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

	"github.com/NissesSenap/shepherd/pkg/adapters/github"
)

type CLI struct {
	API      APICmd      `cmd:"" help:"Run API server"`
	Operator OperatorCmd `cmd:"" help:"Run K8s operator"`
	GitHub   GitHubCmd   `cmd:"" name:"github" help:"Run GitHub adapter"`

	LogLevel int  `help:"Log level (0=info, 1=debug)" default:"0"`
	DevMode  bool `help:"Enable development mode logging" default:"false"`
}

type GitHubCmd struct {
	ListenAddr             string `help:"GitHub adapter listen address" default:":8082" env:"SHEPHERD_GITHUB_ADDR"`
	WebhookSecret          string `help:"GitHub webhook secret" env:"SHEPHERD_GITHUB_WEBHOOK_SECRET"`
	GithubAppID            int64  `help:"GitHub App ID" env:"SHEPHERD_GITHUB_APP_ID"`
	GithubInstallationID   int64  `help:"GitHub Installation ID" env:"SHEPHERD_GITHUB_INSTALLATION_ID"`
	GithubPrivateKeyPath   string `help:"Path to GitHub App private key" env:"SHEPHERD_GITHUB_PRIVATE_KEY_PATH"`
	APIURL                 string `help:"Shepherd API URL" required:"" env:"SHEPHERD_API_URL"`
	CallbackSecret         string `help:"Shared secret for callback verification" env:"SHEPHERD_CALLBACK_SECRET"`
	CallbackURL            string `help:"Callback URL for API to call back" env:"SHEPHERD_CALLBACK_URL"`
	DefaultSandboxTemplate string `help:"Default sandbox template" default:"default"`
}

func (c *GitHubCmd) Run(_ *CLI) error {
	if c.WebhookSecret == "" {
		return fmt.Errorf("webhook-secret is required")
	}
	if c.GithubAppID == 0 {
		return fmt.Errorf("github-app-id is required")
	}
	if c.GithubInstallationID == 0 {
		return fmt.Errorf("github-installation-id is required")
	}
	if c.GithubPrivateKeyPath == "" {
		return fmt.Errorf("github-private-key-path is required")
	}
	if c.CallbackURL == "" {
		return fmt.Errorf("callback-url is required")
	}

	return github.Run(github.Options{
		ListenAddr:             c.ListenAddr,
		WebhookSecret:          c.WebhookSecret,
		AppID:                  c.GithubAppID,
		InstallationID:         c.GithubInstallationID,
		PrivateKeyPath:         c.GithubPrivateKeyPath,
		APIURL:                 c.APIURL,
		CallbackSecret:         c.CallbackSecret,
		CallbackURL:            c.CallbackURL,
		DefaultSandboxTemplate: c.DefaultSandboxTemplate,
	})
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
