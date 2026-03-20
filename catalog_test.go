package ssql

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadCatalog(t *testing.T) {
	// Create temp catalog file
	dir := t.TempDir()
	catalogFile := filepath.Join(dir, "catalog.csv")
	os.WriteFile(catalogFile, []byte(`host,path,format,date_from,date_to,region
node1,/data/jan.csv,csv,2025-01-01,2025-01-31,us-east
node2,/data/feb.jsonl,jsonl,2025-02-01,2025-02-28,eu-west
`), 0644)

	entries, err := ReadCatalog(catalogFile)
	if err != nil {
		t.Fatalf("ReadCatalog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Host != "node1" || entries[0].Path != "/data/jan.csv" || entries[0].Format != "csv" {
		t.Errorf("entry 0: got %+v", entries[0])
	}
	if entries[1].Host != "node2" || entries[1].Format != "jsonl" {
		t.Errorf("entry 1: got %+v", entries[1])
	}
	if entries[0].Metadata["date_from"] != "2025-01-01" {
		t.Errorf("expected date_from=2025-01-01, got %q", entries[0].Metadata["date_from"])
	}
	if entries[0].Metadata["region"] != "us-east" {
		t.Errorf("expected region=us-east, got %q", entries[0].Metadata["region"])
	}
}

func TestReadCatalogMissingColumns(t *testing.T) {
	dir := t.TempDir()
	catalogFile := filepath.Join(dir, "bad.csv")
	os.WriteFile(catalogFile, []byte("name,value\na,1\n"), 0644)

	_, err := ReadCatalog(catalogFile)
	if err == nil {
		t.Fatal("expected error for missing host/path columns")
	}
}

func TestPruneCatalog(t *testing.T) {
	entries := []CatalogEntry{
		{Host: "n1", Path: "/jan.csv", Metadata: map[string]string{"date_from": "2025-01-01", "date_to": "2025-01-31", "region": "us"}},
		{Host: "n2", Path: "/feb.csv", Metadata: map[string]string{"date_from": "2025-02-01", "date_to": "2025-02-28", "region": "eu"}},
		{Host: "n3", Path: "/mar.csv", Metadata: map[string]string{"date_from": "2025-03-01", "date_to": "2025-03-31", "region": "us"}},
	}

	tests := []struct {
		name    string
		filters []CatalogFilter
		want    int
	}{
		{"no filters", nil, 3},
		{"range ge", []CatalogFilter{{Field: "date", Operator: "ge", Value: "2025-02-01"}}, 2},
		{"range le", []CatalogFilter{{Field: "date", Operator: "le", Value: "2025-01-15"}}, 1},
		{"range eq", []CatalogFilter{{Field: "date", Operator: "eq", Value: "2025-02-15"}}, 1},
		{"exact match", []CatalogFilter{{Field: "region", Operator: "eq", Value: "us"}}, 2},
		{"exact ne", []CatalogFilter{{Field: "region", Operator: "ne", Value: "us"}}, 1},
		{"combined", []CatalogFilter{
			{Field: "date", Operator: "ge", Value: "2025-02-01"},
			{Field: "region", Operator: "eq", Value: "us"},
		}, 1},
		{"unknown field", []CatalogFilter{{Field: "status", Operator: "eq", Value: "active"}}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PruneCatalog(entries, tt.filters)
			if len(result) != tt.want {
				t.Errorf("got %d entries, want %d", len(result), tt.want)
			}
		})
	}
}
