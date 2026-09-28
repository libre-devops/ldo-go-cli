package sorting

import (
	"slices"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

func TestNaturalOrder(t *testing.T) {
	values := []string{"web10", "Web3", "web2", "10.0", "9.8", "High", "Low", "Critical",
		"1.419.100.0", "1.419.99.0", "2026-09-24", "2026-09-03"}
	slices.SortStableFunc(values, Compare)
	want := []string{"9.8", "10.0", "Low", "High", "Critical", "1.419.99.0", "1.419.100.0",
		"2026-09-03", "2026-09-24", "web2", "Web3", "web10"}
	if !slices.Equal(values, want) {
		t.Fatalf("got %v", values)
	}
}

func TestRowsSortOnSeveralKeysWithBlanksLast(t *testing.T) {
	rows := [][]string{{"b", "1"}, {"a", "-"}, {"a", "3"}, {"", "2"}, {"a", "10"}}
	cell := func(row []string, column int) string { return row[column] }
	got := SortRows(rows, []Key{{Column: 0}, {Column: 1, Descending: true}}, cell)
	want := [][]string{{"a", "10"}, {"a", "3"}, {"a", "-"}, {"b", "1"}, {"", "2"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("got %v", got)
	}
	unique := Unique([][]string{{"WEB01", "x"}, {"web01 ", "y"}, {"web02", "z"}}, []int{0}, cell)
	if len(unique) != 2 || unique[0][1] != "x" {
		t.Fatalf("unique %v", unique)
	}
}

func TestSortSpecsAndColumns(t *testing.T) {
	name, down, err := ParseSort("last seen:desc")
	if err != nil || name != "last seen" || !down {
		t.Fatal(name, down, err)
	}
	if name, down, _ := ParseSort("NAME"); name != "NAME" || down {
		t.Fatal(name)
	}
	if _, _, err := ParseSort("name:up"); !errs.Is(err, errs.Input) {
		t.Fatal(err)
	}
	if index, err := ColumnIndex([]string{"DEVICE", "LAST SEEN"}, "last_seen"); err != nil || index != 1 {
		t.Fatal(index, err)
	}
	if _, err := ColumnIndex([]string{"DEVICE"}, "owner"); errs.HintOf(err) != "columns: DEVICE" {
		t.Fatal(err)
	}
}
