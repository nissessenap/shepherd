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
	"github.com/NissesSenap/shepherd/pkg/operator"
)

type OperatorCmd struct {
	MetricsAddr        string `help:"Metrics address" default:":9090" env:"SHEPHERD_METRICS_ADDR"`
	HealthAddr         string `help:"Health probe address" default:":8081" env:"SHEPHERD_HEALTH_ADDR"`
	LeaderElection     bool   `help:"Enable leader election" default:"false" env:"SHEPHERD_LEADER_ELECTION"`
	AllowedRunnerImage string `help:"Allowed runner image" required:"" env:"SHEPHERD_RUNNER_IMAGE"`
	RunnerSecretName   string `help:"Runner app key secret" default:"shepherd-runner-app-key" env:"SHEPHERD_RUNNER_SECRET"`
	InitImage          string `help:"Init container image" default:"shepherd-init:latest" env:"SHEPHERD_INIT_IMAGE"`
}

func (c *OperatorCmd) Run(_ *CLI) error {
	return operator.Run(operator.Options{
		MetricsAddr:        c.MetricsAddr,
		HealthAddr:         c.HealthAddr,
		LeaderElection:     c.LeaderElection,
		AllowedRunnerImage: c.AllowedRunnerImage,
		RunnerSecretName:   c.RunnerSecretName,
		InitImage:          c.InitImage,
	})
}
