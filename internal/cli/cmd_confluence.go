package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/atlassian/confluence"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
)

func confluenceCommand(rt *Runtime) *cobra.Command {
	group := newGroup("confluence", "Confluence Cloud: spaces, pages (as Markdown) and CQL search.")
	group.AddCommand(confluenceSpacesCommand(rt), confluencePagesCommand(rt), confluencePageCommand(rt), confluenceSearchCommand(rt))
	return group
}

// withConfluence is a Confluence client for the chosen profile.
func withConfluence(rt *Runtime, name string) (*confluence.Client, error) {
	profile, err := rt.AtlassianProfile(name)
	if err != nil {
		return nil, err
	}
	return rt.Confluence(profile)
}

func confluenceSpacesCommand(rt *Runtime) *cobra.Command {
	var common Common
	command := &cobra.Command{
		Use:   "spaces",
		Short: "The spaces the account can see.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, err := withConfluence(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Spaces(rt.Ctx())
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := make([]any, len(found))
			for index, space := range found {
				rows[index] = render.Cells(space.Key, space.Name, space.Type, space.Status, space.URL)
				records[index] = map[string]any{"key": space.Key, "id": space.ID, "name": space.Name, "type": space.Type,
					"status": space.Status, "url": orNil(space.URL)}
			}
			return rt.Console.Emit(output, []string{"KEY", "NAME", "TYPE", "STATUS", "LINK"}, rows, records)
		},
	}
	atlassianOutput(command, &common, true)
	return command
}

func confluencePagesCommand(rt *Runtime) *cobra.Command {
	var common Common
	var space, title string
	var limit int
	command := &cobra.Command{
		Use:   "pages",
		Short: "Pages, the most recently changed first: every space's, or one's.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			if err := positive(cmd, "limit", limit); err != nil {
				return err
			}
			client, err := withConfluence(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Pages(rt.Ctx(), space, title, limit)
			if err != nil {
				return err
			}
			keys, err := spaceKeys(rt, client, len(found) > 0)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := make([]any, len(found))
			for index, page := range found {
				rows[index] = render.Cells(page.ID, page.Title, or(keys[page.SpaceID], page.SpaceID), strconv.Itoa(page.Version),
					rt.Console.When(page.Updated), page.URL)
				records[index] = pageRecord(page, keys[page.SpaceID])
			}
			return rt.Console.Emit(output, []string{"ID", "TITLE", "SPACE", "VERSION", "UPDATED", "LINK"}, rows, records)
		},
	}
	command.Flags().StringVar(&space, "space", "", "Only this space's pages, by key.")
	command.Flags().StringVar(&title, "title", "", "Only pages with this exact title.")
	command.Flags().IntVarP(&limit, "limit", "n", 50, "How many to show.")
	atlassianOutput(command, &common, true)
	return command
}

// spaceKeys are each space's key by its id, when there are pages to name them for.
func spaceKeys(rt *Runtime, client *confluence.Client, wanted bool) (map[string]string, error) {
	keys := map[string]string{}
	if !wanted {
		return keys, nil
	}
	spaces, err := client.Spaces(rt.Ctx())
	for _, space := range spaces {
		keys[space.ID] = space.Key
	}
	return keys, err
}

func pageRecord(page confluence.Page, space string) map[string]any {
	return map[string]any{"id": page.ID, "title": page.Title, "space_id": page.SpaceID, "space": orNil(space), "status": page.Status,
		"version": page.Version, "updated": render.ISO(page.Updated), "url": orNil(page.URL)}
}

func confluencePageCommand(rt *Runtime) *cobra.Command {
	var common Common
	var onlyMarkdown bool
	command := &cobra.Command{
		Use:   "page ID",
		Short: "One page: its title, where it is, its version, and its body as Markdown.",
		Long:  "One page: its title, where it is, its version, and its body as Markdown. ID is the page's id: the number in its link.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, err := withConfluence(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Page(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			if onlyMarkdown {
				rt.Console.Println(found.Body)
				return nil
			}
			items := [][2]string{{"Title", found.Title}, {"Id", found.ID}, {"Version", strconv.Itoa(found.Version)},
				{"Updated", render.Moment(found.Updated)}, {"Link", found.URL}}
			if output == render.Table {
				rt.Console.Println(pairs(rt, items))
				rt.Console.Println("")
				rt.Console.Println(or(found.Body, "(an empty page)"))
				return nil
			}
			record := pageRecord(found, "")
			record["body"] = orNil(found.Body)
			return showPairs(rt, output, append(items, [2]string{"Body", found.Body}), record)
		},
	}
	command.Flags().BoolVar(&onlyMarkdown, "markdown", false, "Only the page, as Markdown.")
	atlassianOutput(command, &common, false)
	return command
}

func confluenceSearchCommand(rt *Runtime) *cobra.Command {
	var common Common
	var limit int
	command := &cobra.Command{
		Use:   "search CQL",
		Short: "What a CQL query finds: pages, blog posts, attachments.",
		Long:  "What a CQL query finds: pages, blog posts, attachments. CQL is a query, e.g. type = page AND text ~ \"runbook\".",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			if err := positive(cmd, "limit", limit); err != nil {
				return err
			}
			client, err := withConfluence(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Search(rt.Ctx(), args[0], limit)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := make([]any, len(found))
			for index, hit := range found {
				rows[index] = render.Cells(hit.Type, hit.Title, hit.Space, rt.Console.When(hit.Updated), hit.URL)
				records[index] = map[string]any{"id": orNil(hit.ID), "type": hit.Type, "title": hit.Title, "space": orNil(hit.Space),
					"updated": render.ISO(hit.Updated), "url": orNil(hit.URL), "excerpt": orNil(hit.Excerpt)}
			}
			return rt.Console.Emit(output, []string{"TYPE", "TITLE", "SPACE", "UPDATED", "LINK"}, rows, records)
		},
	}
	command.Flags().IntVarP(&limit, "limit", "n", 25, "How many to show.")
	atlassianOutput(command, &common, true)
	return command
}
