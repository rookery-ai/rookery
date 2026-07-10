package prompts

import (
	"strings"
	"testing"
)

func TestConnectedToolsBlock(t *testing.T) {
	block := connectedToolsBlock([]ConnectionRef{{Provider: "google", Label: "work", Identity: "w@x.com"}})
	if !strings.Contains(block, "work") || !strings.Contains(block, "google") {
		t.Fatalf("block missing account: %s", block)
	}
	if strings.Contains(strings.ToLower(block), "discover") {
		t.Fatalf("runtime block must NOT tell the agent to discover — tools are the interface")
	}
}

func TestConnectedToolsBlockEmpty(t *testing.T) {
	if connectedToolsBlock(nil) != "" {
		t.Fatal("no connections → empty block")
	}
}

func TestConnectedToolsBlockMultiAccountMentionsSuffix(t *testing.T) {
	block := connectedToolsBlock([]ConnectionRef{
		{Provider: "google", Label: "work", Identity: "w@x.com"},
		{Provider: "google", Label: "home", Identity: "h@x.com"},
	})
	if !strings.Contains(block, "__work") || !strings.Contains(block, "__home") {
		t.Fatalf("multi-account block must mention __<label> suffix: %s", block)
	}
}
