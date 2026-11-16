package output

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"go.ed.linuxfoundation.org/lfed/sdk/jq"
	"go.yaml.in/yaml/v3"
)

type Mode string

const (
	Table Mode = "table"
	JSON  Mode = "json"
	YAML  Mode = "yaml"
)

const Modes = "table, json or yaml"

func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case Table, JSON, YAML:
		return Mode(s), nil
	case "":
		return Table, nil
	}
	return "", fmt.Errorf("invalid output %q; expected %s", s, Modes)
}

func (m Mode) Structured() bool { return m == JSON || m == YAML }

type Column[T any] struct {
	Header string
	Value  func(T) string
}

type Printer struct {
	Out  io.Writer
	Mode Mode
	JQ   string
}

func Print[T any](p Printer, rows []T, columns []Column[T]) error {
	if p.Mode.Structured() {
		if rows == nil {
			rows = []T{}
		}
		return p.PrintValue(rows)
	}
	return printTable(p.Out, rows, columns)
}

func PrintOne[T any](p Printer, v T, columns []Column[T]) error {
	if p.Mode.Structured() {
		return p.PrintValue(v)
	}
	return printTable(p.Out, []T{v}, columns)
}

// PrintDetail prints one value. A table is one "HEADER  value" line per
// column, and a column whose value is "" is left out. Structured modes
// print v itself.
func PrintDetail[T any](p Printer, v T, columns []Column[T]) error {
	if p.Mode.Structured() {
		return p.PrintValue(v)
	}
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	for _, c := range columns {
		if value := c.Value(v); value != "" {
			fmt.Fprintf(tw, "%s\t%s\n", strings.ToUpper(c.Header), value)
		}
	}
	return tw.Flush()
}

func (p Printer) PrintValue(v any) error {
	if p.JQ != "" {
		doc, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return jq.Evaluate(context.Background(), p.Out, p.JQ, doc)
	}
	if p.Mode != YAML {
		return PrintJSON(p.Out, v)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return err
	}
	blockStyle(&node)
	enc := yaml.NewEncoder(p.Out)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return err
	}
	return enc.Close()
}

func PrintJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printTable[T any](w io.Writer, rows []T, columns []Column[T]) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	headers := make([]string, len(columns))
	for i, c := range columns {
		headers[i] = strings.ToUpper(c.Header)
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	cells := make([]string, len(columns))
	for _, row := range rows {
		for i, c := range columns {
			cells[i] = c.Value(row)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

func blockStyle(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		blockStyle(c)
	}
}
