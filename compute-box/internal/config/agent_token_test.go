package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentTokenPersistsAndRotatesOnlyExplicitly(t *testing.T) {
	dir := t.TempDir()
	first, err := AgentToken(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 76 {
		t.Fatal("expected a 256-bit generated token")
	}
	second, err := AgentToken(dir, "")
	if err != nil || first != second {
		t.Fatal("token changed on restart")
	}
	override, err := AgentToken(dir, "explicit-test-override")
	if err != nil || override != "explicit-test-override" {
		t.Fatal("override ignored")
	}
	unchanged, _ := AgentToken(dir, "")
	if unchanged != first {
		t.Fatal("override overwrote saved token")
	}
	rotated, err := RotateAgentToken(dir)
	if err != nil || rotated == first {
		t.Fatal("rotation failed")
	}
	saved, _ := AgentToken(dir, "")
	if saved != rotated {
		t.Fatal("rotation was not persisted")
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-token"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := AgentToken(dir, ""); err == nil {
		t.Fatal("corrupt token silently replaced")
	}
}
