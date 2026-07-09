//go:build linux

/*
   Copyright The containerd Authors.

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

package sysboxsnapshotter

import (
	"errors"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	sysboxsnapshotter "github.com/w7panel/sysbox/sysbox-snapshotter"
)

// Config represents configuration for the sysbox snapshotter plugin.
type Config struct {
	// Root directory for the plugin
	RootPath string `toml:"root_path"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.SnapshotPlugin,
		ID:     "sysbox",
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())

			config, ok := ic.Config.(*Config)
			if !ok {
				return nil, errors.New("invalid sysbox snapshotter configuration")
			}

			root := ic.Properties[plugins.PropertyRootDir]
			if config.RootPath != "" {
				root = config.RootPath
			}

			ic.Meta.Exports["root"] = root
			return sysboxsnapshotter.NewSnapshotter(root)
		},
	})
}
