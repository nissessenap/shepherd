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

	"github.com/NissesSenap/shepherd/pkg/api"
)

type APICmd struct {
	ListenAddr           string `help:"Public API listen address" default:":8080" env:"SHEPHERD_API_ADDR"`
	InternalListenAddr   string `help:"Internal (runner) API listen address" default:":8081" env:"SHEPHERD_INTERNAL_API_ADDR"`
	CallbackSecret       string `help:"HMAC secret for adapter callbacks" env:"SHEPHERD_CALLBACK_SECRET"`
	Namespace            string `help:"Namespace for task creation" default:"shepherd" env:"SHEPHERD_NAMESPACE"`
	GithubAppID          int64  `help:"GitHub Runner App ID" env:"SHEPHERD_GITHUB_APP_ID"`
	GithubInstallationID int64  `help:"GitHub Installation ID" env:"SHEPHERD_GITHUB_INSTALLATION_ID"`
	GithubAPIURL         string `help:"GitHub API URL" default:"https://api.github.com" env:"SHEPHERD_GITHUB_API_URL"`
	GithubPrivateKeyPath string `help:"Path to Runner App private key" env:"SHEPHERD_GITHUB_PRIVATE_KEY_PATH"`
}

func (c *APICmd) Run(_ *CLI) error {
	githubFlagsSet := c.GithubAppID != 0 || c.GithubInstallationID != 0 || c.GithubPrivateKeyPath != ""
	if githubFlagsSet {
		if c.GithubAppID == 0 || c.GithubInstallationID == 0 || c.GithubPrivateKeyPath == "" {
			return fmt.Errorf("github-app-id, github-installation-id, and github-private-key-path must all be set together")
		}
	}

	return api.Run(api.Options{
		ListenAddr:           c.ListenAddr,
		InternalListenAddr:   c.InternalListenAddr,
		CallbackSecret:       c.CallbackSecret,
		Namespace:            c.Namespace,
		GithubAppID:          c.GithubAppID,
		GithubInstallationID: c.GithubInstallationID,
		GithubAPIURL:         c.GithubAPIURL,
		GithubPrivateKeyPath: c.GithubPrivateKeyPath,
	})
}
