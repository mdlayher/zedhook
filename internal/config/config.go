// Copyright 2022 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package config provides configuration for zedhookd.
package config

import (
	"fmt"
	"io"
	"net"

	"github.com/pelletier/go-toml"
)

// Minimal is the minimal configuration file for zedhookd.
const Minimal = `# %s configuration file
# Listen HTTP on localhost only.
[server]
address = "localhost:9919"

# Optional: enable Prometheus metrics.
[debug]
prometheus = true
`

// A file is the raw top-level configuration file representation.
type file struct {
	Server Server `toml:"server"`
	Debug  Debug  `toml:"debug"`
}

// Config specifies the configuration for zedhookd.
type Config struct {
	Server Server
	Debug  Debug
}

// Server provides configuration for the zedhookd Server.
type Server struct {
	Address string `toml:"address"`
}

// Debug provides configuration for debugging and observability.
type Debug struct {
	Prometheus bool `toml:"prometheus"`
	PProf      bool `toml:"pprof"`
}

// Parse parses a Config in TOML format from an io.Reader and verifies that
// the configuration is valid.
func Parse(r io.Reader) (*Config, error) {
	var f file
	if err := toml.NewDecoder(r).Strict(true).Decode(&f); err != nil {
		return nil, err
	}

	var c Config

	// Validate debug configuration if set.
	if f.Server.Address != "" {
		if _, err := net.ResolveTCPAddr("tcp", f.Server.Address); err != nil {
			return nil, fmt.Errorf("bad server address: %v", err)
		}
		c.Server = f.Server
	}

	return &c, nil
}
