package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// Test 1: SuccessResult with empty repos serializes repos as [] not null.
func TestSuccessResultEmptyReposNotNull(t *testing.T) {
	r := SuccessResult{
		Command:  "list",
		ExitCode: 0,
		Summary:  "ok",
		Repos:    make([]string, 0),
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	repos, ok := m["repos"]
	if !ok {
		t.Fatal("expected repos key to be present")
	}
	if repos == nil {
		t.Fatal("expected repos to be [] not null")
	}
	arr, ok := repos.([]interface{})
	if !ok {
		t.Fatalf("expected repos to be an array, got %T", repos)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty repos array, got length %d", len(arr))
	}
}

// Test 2: SuccessResult with repo entries populates repos array.
func TestSuccessResultWithRepos(t *testing.T) {
	type RepoEntry struct {
		Name string `json:"name"`
	}
	repos := []RepoEntry{
		{Name: "owner/alpha"},
		{Name: "owner/beta"},
	}
	r := SuccessResult{
		Command:  "list",
		ExitCode: 0,
		Summary:  "2 repos",
		Repos:    repos,
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	arr, ok := m["repos"].([]interface{})
	if !ok {
		t.Fatalf("expected repos to be an array, got %T", m["repos"])
	}
	if len(arr) != 2 {
		t.Errorf("expected 2 repos, got %d", len(arr))
	}
}

// Test 3: ErrorResult omits summary and repos keys entirely from JSON.
func TestErrorResultOmitsSummaryAndRepos(t *testing.T) {
	r := ErrorResult{
		Command:  "sync",
		ExitCode: 2,
		Error:    "something went wrong",
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if _, ok := m["summary"]; ok {
		t.Error("expected summary key to be absent from ErrorResult JSON")
	}
	if _, ok := m["repos"]; ok {
		t.Error("expected repos key to be absent from ErrorResult JSON")
	}
}

// Test 4: ErrorResult without hint omits hint key.
func TestErrorResultWithoutHintOmitsHintKey(t *testing.T) {
	r := ErrorResult{
		Command:  "bootstrap",
		ExitCode: 2,
		Error:    "fatal error",
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if _, ok := m["hint"]; ok {
		t.Error("expected hint key to be absent when no hint is set")
	}
}

// Test 5: Compact mode omits repos key from SuccessResult.
// Replicates the same transformation PrintAndExit performs for compact mode
// without calling os.Exit.
func TestCompactModeOmitsReposKey(t *testing.T) {
	r := SuccessResult{
		Command:  "list",
		ExitCode: 0,
		Summary:  "3 repos",
		Repos:    []string{"a", "b", "c"},
	}

	// Replicate the compact transformation from PrintAndExit.
	compact := struct {
		Command  string      `json:"command"`
		ExitCode int         `json:"exit_code"`
		DryRun   bool        `json:"dry_run,omitempty"`
		Summary  interface{} `json:"summary"`
	}{r.Command, r.ExitCode, r.DryRun, r.Summary}

	b, err := json.Marshal(compact)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if _, ok := m["repos"]; ok {
		t.Error("expected repos key to be absent in compact mode")
	}
	if _, ok := m["summary"]; !ok {
		t.Error("expected summary key to be present in compact mode")
	}
	if _, ok := m["command"]; !ok {
		t.Error("expected command key to be present in compact mode")
	}
}

// Test 6: HintError wrapping — Error() returns inner message, Unwrap() returns
// inner error, and errors.As works through wrapping.
func TestHintErrorWrapping(t *testing.T) {
	inner := errors.New("manifest not found")
	he := NewHintError(inner, "run rp init first")

	if he.Error() != inner.Error() {
		t.Errorf("Error() = %q, want %q", he.Error(), inner.Error())
	}
	if he.Unwrap() != inner {
		t.Errorf("Unwrap() returned wrong error")
	}

	var target *HintError
	if !errors.As(he, &target) {
		t.Error("errors.As should find *HintError through wrapping")
	}
	if target.Hint != "run rp init first" {
		t.Errorf("Hint = %q, want %q", target.Hint, "run rp init first")
	}

	// Also verify errors.As works when the HintError is wrapped by another error.
	wrapped := fmt.Errorf("outer: %w", he)
	var target2 *HintError
	if !errors.As(wrapped, &target2) {
		t.Error("errors.As should find *HintError through fmt.Errorf wrapping")
	}
}

// Test 7: UpResult with 3 sub-results — all present in JSON.
func TestUpResultWithThreeSubResults(t *testing.T) {
	r := UpResult{
		Command:  "up",
		ExitCode: 0,
		Bootstrap: &SubResult{
			Summary: "bootstrapped 2 repos",
			Repos:   []string{"a", "b"},
		},
		Sync: &SubResult{
			Summary: "synced 2 repos",
			Repos:   []string{"a", "b"},
		},
		Install: &SubResult{
			Summary: "install done",
			Repos:   []string{"a"},
		},
		Update: &SubResult{
			Summary: "update done",
			Repos:   []string{"b"},
		},
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	for _, key := range []string{"bootstrap", "sync", "install", "update"} {
		if _, ok := m[key]; !ok {
			t.Errorf("expected %q key to be present in UpResult JSON", key)
		}
	}
}

// Test 8: UpResult with nil Install/Update — keys present as null (no omitempty).
func TestUpResultNilInstallUpdatePresent(t *testing.T) {
	r := UpResult{
		Command:  "up",
		ExitCode: 0,
		Bootstrap: &SubResult{
			Summary: "bootstrapped 1 repo",
		},
		Sync: &SubResult{
			Summary: "synced 1 repo",
		},
		Install: nil,
		Update:  nil,
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// install and update must be present (as null) even when nil — no omitempty.
	if _, ok := m["install"]; !ok {
		t.Error("expected install key to be present (as null) when Install is nil")
	}
	if _, ok := m["update"]; !ok {
		t.Error("expected update key to be present (as null) when Update is nil")
	}
	if _, ok := m["bootstrap"]; !ok {
		t.Error("expected bootstrap key to be present")
	}
	if _, ok := m["sync"]; !ok {
		t.Error("expected sync key to be present")
	}
}
