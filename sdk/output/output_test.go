package output

import (
	"bytes"
	"strings"
	"testing"
)

type row struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

var cols = []Column[row]{
	{Header: "name", Value: func(r row) string { return r.Name }},
	{Header: "count", Value: func(r row) string { return string(rune('0' + r.Count)) }},
}

func TestPrintTableEmptyListPrintsHeaderOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(Printer{Out: &buf, Mode: Table}, []row(nil), cols); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "NAME  COUNT\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPrintTableAlignsColumns(t *testing.T) {
	var buf bytes.Buffer
	rows := []row{{"a", 1}, {"longer", 2}}
	if err := Print(Printer{Out: &buf, Mode: Table}, rows, cols); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d: %q", len(lines), buf.String())
	}
	if idx := strings.Index(lines[1], "1"); idx != strings.Index(lines[2], "2") {
		t.Fatalf("columns misaligned: %q", buf.String())
	}
}

func TestPrintJSONEmptyListIsArray(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(Printer{Out: &buf, Mode: JSON}, []row(nil), cols); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Fatalf("got %q", got)
	}
}

func TestPrintOneTableAndJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintOne(Printer{Out: &buf, Mode: Table}, row{"a", 1}, cols); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "NAME  COUNT\na     1\n" {
		t.Fatalf("table: %q", got)
	}
	buf.Reset()
	if err := PrintOne(Printer{Out: &buf, Mode: JSON}, row{"a", 1}, cols); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "{\n  \"name\": \"a\",\n  \"count\": 1\n}\n" {
		t.Fatalf("json: %q", got)
	}
}

func TestParseMode(t *testing.T) {
	if m, err := ParseMode(""); err != nil || m != Table {
		t.Fatalf("empty: %v %v", m, err)
	}
	if _, err := ParseMode("xml"); err == nil {
		t.Fatal("xml accepted")
	}
}

func TestPrintYAMLFollowsJSONTags(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(Printer{Out: &buf, Mode: YAML}, []row{{Name: "a", Count: 2}}, cols); err != nil {
		t.Fatal(err)
	}

	if got := buf.String(); got != "- name: a\n  count: 2\n" {
		t.Fatalf("got %q", got)
	}
	buf.Reset()
	if err := Print(Printer{Out: &buf, Mode: YAML}, []row(nil), cols); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Fatalf("empty list: %q", got)
	}
}

func TestPrintJQSelectsRawString(t *testing.T) {
	var buf bytes.Buffer
	p := Printer{Out: &buf, Mode: JSON, JQ: ".[0].name"}
	if err := Print(p, []row{{"a", 1}, {"b", 2}}, cols); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "a\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPrintValueJQOnStruct(t *testing.T) {
	var buf bytes.Buffer
	p := Printer{Out: &buf, Mode: JSON, JQ: ".x"}
	if err := p.PrintValue(struct {
		X []int `json:"x"`
	}{X: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "[1,2]\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPrintJQInvalidExpressionErrors(t *testing.T) {
	var buf bytes.Buffer
	p := Printer{Out: &buf, Mode: JSON, JQ: ".["}
	if err := p.PrintValue(map[string]int{"a": 1}); err == nil {
		t.Fatal("expected parse error")
	}
}
