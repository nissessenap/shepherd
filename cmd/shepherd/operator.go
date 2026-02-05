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
	"net/url"

	"github.com/NissesSenap/shepherd/pkg/operator"
)

type OperatorCmd struct {
	MetricsAddr    string `help:"Metrics address" default:":9090" env:"SHEPHERD_METRICS_ADDR"`
	HealthAddr     string `help:"Health probe address" default:":8082" env:"SHEPHERD_HEALTH_ADDR"`
	LeaderElection bool   `help:"Enable leader election" default:"false" env:"SHEPHERD_LEADER_ELECTION"`
	APIURL         string `help:"Internal API server URL" required:"" env:"SHEPHERD_API_URL"`
}

func (c *OperatorCmd) Run(_ *CLI) error {
	u, err := url.Parse(c.APIURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid SHEPHERD_API_URL %q: must be a valid URL with scheme and host", c.APIURL)
	}

	return operator.Run(operator.Options{
		MetricsAddr:    c.MetricsAddr,
		HealthAddr:     c.HealthAddr,
		LeaderElection: c.LeaderElection,
		APIURL:         c.APIURL,
	})
}
