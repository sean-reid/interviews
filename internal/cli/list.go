package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/taxonomy"
)

type listItem struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Flavor      string   `json:"flavor,omitempty"`
	Class       string   `json:"class,omitempty"`
	Disciplines []string `json:"disciplines"`
	Levels      []string `json:"levels"`
	Title       string   `json:"title"`
}

func cmdList(args []string, stdout, stderr io.Writer) int {
	fs, contentRoot := newFlagSet("list", stderr)
	typeFilter := fs.String("type", "", "only this interview type")
	levelFilter := fs.String("level", "", "only problems that grade this level")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, perr := parsePermuted(fs, args)
	if perr != nil {
		return parseExit(perr)
	}
	if len(positional) > 0 {
		fmt.Fprintf(stderr, "interviews list: unexpected argument %q\n", positional[0])
		return 2
	}
	if *typeFilter != "" && !taxonomy.ValidType(taxonomy.Type(*typeFilter)) {
		fmt.Fprintf(stderr, "interviews list: unknown type %q\n", *typeFilter)
		return 2
	}
	level, err := parseLevel(*levelFilter)
	if err != nil {
		fmt.Fprintf(stderr, "interviews list: %v\n", err)
		return 2
	}
	reg, err := openRegistry(*contentRoot, false, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "interviews list: %v\n", err)
		return 1
	}

	items := []listItem{}
	for _, e := range reg.Problems() {
		if e.Problem == nil {
			continue
		}
		if *typeFilter != "" && string(e.Type) != *typeFilter {
			continue
		}
		if level != "" && !slices.Contains(e.Problem.Manifest.Levels, level) {
			continue
		}
		items = append(items, itemFor(e.Problem.Manifest))
	}

	if *asJSON {
		return writeJSON(stdout, stderr, items)
	}
	w := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tKIND\tLEVELS\tTITLE")
	for _, it := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", it.ID, it.Type, it.kind(), strings.Join(it.Levels, ","), it.Title)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(stderr, "writing output: %v\n", err)
		return 1
	}
	return 0
}

// itemFor projects a manifest into the shape list and describe both print.
func itemFor(m content.Manifest) listItem {
	return listItem{
		ID:          m.ID,
		Type:        string(m.Type),
		Flavor:      string(m.Flavor),
		Class:       string(m.Class),
		Disciplines: toStrings(m.Disciplines),
		Levels:      toStrings(m.Levels),
		Title:       m.Title,
	}
}

// kind is the flavor for debugging problems, the class for take-homes, and a
// dash for types that have neither.
func (it listItem) kind() string {
	switch {
	case it.Flavor != "":
		return it.Flavor
	case it.Class != "":
		return it.Class
	default:
		return "-"
	}
}

func toStrings[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "encoding JSON: %v\n", err)
		return 1
	}
	return 0
}
