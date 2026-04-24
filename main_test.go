package main

import (
	"testing"

	"github.com/purisev/vault-kv-diff/internal/comparator"
)

func TestGroupByPath_Empty(t *testing.T) {
	got := groupByPath(nil)
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestGroupByPath_SinglePathSingleKey(t *testing.T) {
	dups := []comparator.DuplicateKey{
		{Path: "app/db", Key: "HOST", KV1: "stage", KV2: "prod"},
	}
	got := groupByPath(dups)
	if len(got) != 1 {
		t.Fatalf("expected 1 path, got %d", len(got))
	}
	if got[0].Path != "app/db" {
		t.Errorf("expected path app/db, got %s", got[0].Path)
	}
	if len(got[0].Keys) != 1 || got[0].Keys[0] != "HOST" {
		t.Errorf("expected keys [HOST], got %v", got[0].Keys)
	}
}

func TestGroupByPath_MultipleKeysOnePath(t *testing.T) {
	dups := []comparator.DuplicateKey{
		{Path: "app/db", Key: "PASS", KV1: "stage", KV2: "prod"},
		{Path: "app/db", Key: "HOST", KV1: "stage", KV2: "prod"},
		{Path: "app/db", Key: "PORT", KV1: "stage", KV2: "prod"},
	}
	got := groupByPath(dups)
	if len(got) != 1 {
		t.Fatalf("expected 1 path, got %d", len(got))
	}
	want := []string{"HOST", "PASS", "PORT"}
	for i, k := range want {
		if got[0].Keys[i] != k {
			t.Errorf("keys[%d]: expected %s, got %s", i, k, got[0].Keys[i])
		}
	}
}

func TestGroupByPath_MultiplePathsSorted(t *testing.T) {
	dups := []comparator.DuplicateKey{
		{Path: "z/last", Key: "K", KV1: "stage", KV2: "prod"},
		{Path: "a/first", Key: "K", KV1: "stage", KV2: "prod"},
		{Path: "m/middle", Key: "K", KV1: "stage", KV2: "prod"},
	}
	got := groupByPath(dups)
	if len(got) != 3 {
		t.Fatalf("expected 3 paths, got %d", len(got))
	}
	wantOrder := []string{"a/first", "m/middle", "z/last"}
	for i, p := range wantOrder {
		if got[i].Path != p {
			t.Errorf("paths[%d]: expected %s, got %s", i, p, got[i].Path)
		}
	}
}

func TestGroupByPath_PathsAffectedCount(t *testing.T) {
	dups := []comparator.DuplicateKey{
		{Path: "app/db", Key: "HOST", KV1: "stage", KV2: "prod"},
		{Path: "app/db", Key: "PASS", KV1: "stage", KV2: "prod"},
		{Path: "app/redis", Key: "URL", KV1: "stage", KV2: "prod"},
	}
	got := groupByPath(dups)
	if len(got) != 2 {
		t.Errorf("expected 2 affected paths, got %d", len(got))
	}
}
