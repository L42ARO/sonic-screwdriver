package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentToken persists the shared agent secret independently of the OpenAI API key.
func AgentToken(dataDir, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	path := filepath.Join(dataDir, "agent-token")
	if raw, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(raw))
		if len(token) < 32 {
			return "", fmt.Errorf("invalid saved agent token; explicitly rotate it rather than silently replacing it")
		}
		return token, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return AgentToken(dataDir, "")
	}
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(token + "\n")
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	return token, closeErr
}

// RotateAgentToken is explicit; running servers must be restarted to use the new secret.
func RotateAgentToken(dataDir string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dataDir, "agent-token-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.WriteString(token + "\n")
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Rename(file.Name(), filepath.Join(dataDir, "agent-token")); err != nil {
		return "", err
	}
	return token, nil
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "sonic_agent_" + hex.EncodeToString(bytes), nil
}
