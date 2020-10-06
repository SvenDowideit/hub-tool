/*
   Copyright 2020 Docker Inc.

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

package tag

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/docker/cli/cli"
	"github.com/docker/cli/cli/command"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/go-units"
	"github.com/spf13/cobra"

	"github.com/docker/hub-cli-plugin/internal/format"
	"github.com/docker/hub-cli-plugin/internal/hub"
	"github.com/docker/hub-cli-plugin/internal/metrics"
)

const (
	lsName = "ls"
)

var (
	defaultColumns = []column{
		{"TAG", func(t hub.Tag) string { return t.Name }},
		{"DIGEST", func(t hub.Tag) string {
			if len(t.Images) > 0 {
				return t.Images[0].Digest
			}
			return ""
		}},
		{"STATUS", func(t hub.Tag) string {
			return t.Status
		}},
		{"EXPIRES", func(t hub.Tag) string {
			if t.Expires.Nanosecond() == 0 {
				return ""
			}
			return units.HumanDuration(time.Until(t.Expires))
		}},
		{"LAST UPDATE", func(t hub.Tag) string {
			if t.LastUpdated.Nanosecond() == 0 {
				return ""
			}
			return fmt.Sprintf("%s ago", units.HumanDuration(time.Since(t.LastUpdated)))
		}},
		{"LAST PUSHED", func(t hub.Tag) string {
			if t.LastPushed.Nanosecond() == 0 {
				return ""
			}
			return units.HumanDuration(time.Since(t.LastPushed))
		}},
		{"LAST PULLED", func(t hub.Tag) string {
			if t.LastPulled.Nanosecond() == 0 {
				return ""
			}
			return units.HumanDuration(time.Since(t.LastPulled))
		}},
		{"SIZE", func(t hub.Tag) string {
			size := t.FullSize
			if len(t.Images) > 0 {
				size = 0
				for _, image := range t.Images {
					size += image.Size
				}
			}
			return units.HumanSize(float64(size))
		}},
	}
	platformColumn = column{
		"OS/ARCH",
		func(t hub.Tag) string {
			var platforms []string
			for _, image := range t.Images {
				platform := fmt.Sprintf("%s/%s", image.Os, image.Architecture)
				if image.Variant != "" {
					platform += "/" + image.Variant
				}
				platforms = append(platforms, platform)
			}
			return strings.Join(platforms, ",")
		},
	}
)

type column struct {
	header string
	value  func(t hub.Tag) string
}

type listOptions struct {
	format.Option
	platforms bool
	all       bool
	sort      string
}

func newListCmd(ctx context.Context, dockerCli command.Cli, parent string) *cobra.Command {
	var opts listOptions
	cmd := &cobra.Command{
		Use:   lsName + " [OPTION] REPOSITORY",
		Short: "List all the images in a repository",
		Args:  cli.ExactArgs(1),
		PreRun: func(cmd *cobra.Command, args []string) {
			metrics.Send(parent, lsName)
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return runList(ctx, dockerCli, opts, args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.platforms, "platforms", false, "List all available platforms per tag")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Fetch all available tags")
	cmd.Flags().StringVar(&opts.sort, "sort", "", "Sort tags by (updated|name)[=(asc|desc)] (e.g.: --sort updated or --sort name=desc)")
	opts.AddFormatFlag(cmd.Flags())
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func runList(ctx context.Context, dockerCli command.Cli, opts listOptions, repository string) error {
	ordering, err := mapOrdering(opts.sort)
	if err != nil {
		return err
	}
	authResolver := func(hub *registry.IndexInfo) types.AuthConfig {
		return command.ResolveAuthConfig(ctx, dockerCli, hub)
	}
	var clientOps []hub.ClientOp
	if opts.all {
		clientOps = append(clientOps, hub.WithAllElements())
	}
	client, err := hub.NewClient(authResolver, clientOps...)
	if err != nil {
		return err
	}
	var reqOps []hub.RequestOp
	if ordering != "" {
		reqOps = append(reqOps, hub.WithSortingOrder(ordering))
	}
	tags, err := client.GetTags(repository, reqOps...)
	if err != nil {
		return err
	}

	if opts.platforms {
		defaultColumns = append(defaultColumns, platformColumn)
	}

	return opts.Print(os.Stdout, tags, printTags)
}

func printTags(out io.Writer, values interface{}) error {
	tags := values.([]hub.Tag)
	w := tabwriter.NewWriter(out, 20, 1, 3, ' ', 0)
	var headers []string
	for _, column := range defaultColumns {
		headers = append(headers, column.header)
	}
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, tag := range tags {
		var values []string
		for _, column := range defaultColumns {
			values = append(values, column.value(tag))
		}
		fmt.Fprintln(w, strings.Join(values, "\t"))
	}
	return w.Flush()
}

const (
	sortAsc  = "asc"
	sortDesc = "desc"
)

func mapOrdering(order string) (string, error) {
	if order == "" {
		return "", nil
	}
	name := "-name"
	update := "last_updated"
	fields := strings.SplitN(order, "=", 2)
	if len(fields) == 2 {
		switch fields[1] {
		case sortDesc:
			name = "name"
			update = "-last_updated"
		case sortAsc:
		default:
			return "", fmt.Errorf(`invalid sorting direction %q: should be either "asc" or "desc"`, fields[1])
		}
	}
	switch fields[0] {
	case "updated":
		return update, nil
	case "name":
		return name, nil
	default:
		return "", fmt.Errorf(`unknown sorting column %q: should be either "name" or "updated"`, fields[0])
	}
}
