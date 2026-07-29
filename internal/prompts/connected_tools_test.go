package prompts

import (
	"strings"
	"testing"
)

func TestConnectedToolsBlockAPI(t *testing.T) {
	block := connectedToolsBlock([]ConnectionRef{{Provider: "google", Label: "work", Identity: "w@x.com"}},
		[]string{"gmail_search"}, BackendToolCalling, "")
	if !strings.Contains(block, "work") || !strings.Contains(block, "google") {
		t.Fatalf("block missing account: %s", block)
	}
	if !strings.Contains(block, "NATIVE tools") {
		t.Fatalf("tool-calling backend should get native-tools wording: %s", block)
	}
	if strings.Contains(strings.ToLower(block), "discover") {
		t.Fatalf("runtime block must NOT tell the agent to discover — tools are the interface")
	}
}

func TestConnectedToolsBlockCLI(t *testing.T) {
	block := connectedToolsBlock([]ConnectionRef{{Provider: "google", Label: "work", Identity: "w@x.com"}},
		[]string{"gmail_search", "gmail_send_email"}, BackendFullCoder, "/opt/rookery/rookery")
	if !strings.Contains(block, "/opt/rookery/rookery connector exec") {
		t.Fatalf("CLI backend should use the absolute binary path in the command: %s", block)
	}
	if !strings.Contains(block, "gmail_search") {
		t.Fatalf("CLI backend should list the available tool names: %s", block)
	}
}

func TestConnectedToolsBlockCLIFallsBackToBareName(t *testing.T) {
	block := connectedToolsBlock([]ConnectionRef{{Provider: "google", Label: "work"}},
		[]string{"gmail_search"}, BackendFullCoder, "")
	if !strings.Contains(block, "rookery connector exec") {
		t.Fatalf("empty bin should fall back to bare name: %s", block)
	}
}

func TestConnectedToolsBlockEmpty(t *testing.T) {
	if connectedToolsBlock(nil, nil, BackendToolCalling, "") != "" {
		t.Fatal("no connections → empty block")
	}
}

func TestConnectedToolsBlockMultiAccountMentionsSuffix(t *testing.T) {
	block := connectedToolsBlock([]ConnectionRef{
		{Provider: "google", Label: "work", Identity: "w@x.com"},
		{Provider: "google", Label: "home", Identity: "h@x.com"},
	}, nil, BackendToolCalling, "")
	if !strings.Contains(block, "__work") || !strings.Contains(block, "__home") {
		t.Fatalf("multi-account block must mention __<label> suffix: %s", block)
	}
}
