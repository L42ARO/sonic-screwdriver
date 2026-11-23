// Package config reads the compute-box settings from the environment and the .env file.
package config

import (
	"bufio"
	"cmp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr       string        // HTTP listen address
	DataDir    string        // SQLite database and job screenshots
	PublicURL  string        // how people reach this server, e.g. https://box.example.com; used in printed links
	Brain      string        // "openai" or "scripted"
	ScriptsDir string        // action scripts for the scripted brain
	OpenAIKey  string        // never logged
	Model      string        // OpenAI model for the openai brain
	Reasoning  string        // reasoning effort sent to the model; empty uses the model default
	BudgetUSD  float64       // hard cap on total model spend; 0 disables
	NtfyTopic  string        // ntfy.sh topic for alerts; empty disables
	JobTimeout time.Duration // a job running longer than this fails with timeout
	MaxSteps   int           // most screen actions one job may take
}

// Load reads .env (if present) then the environment. Real environment variables win.
func Load() Config {
	loadDotEnv(".env")
	dataDir := cmp.Or(os.Getenv("SONIC_DATA_DIR"), "data")
	return Config{
		Addr:       cmp.Or(os.Getenv("SONIC_ADDR"), "127.0.0.1:8080"), // localhost only unless asked
		DataDir:    dataDir,
		PublicURL:  strings.TrimRight(os.Getenv("SONIC_PUBLIC_URL"), "/"),
		Brain:      cmp.Or(os.Getenv("SONIC_BRAIN"), "openai"),
		ScriptsDir: cmp.Or(os.Getenv("SONIC_SCRIPTS_DIR"), filepath.Join("scripts")),
		OpenAIKey:  cmp.Or(os.Getenv("OPENAI_KEY"), os.Getenv("OPENAI_API_KEY")),
		Model:      cmp.Or(os.Getenv("SONIC_MODEL"), "gpt-6-luna"),
		Reasoning:  os.Getenv("SONIC_REASONING"),
		BudgetUSD:  floatEnv("SONIC_BUDGET_USD", 3.00),
		NtfyTopic:  os.Getenv("SONIC_NTFY_TOPIC"),
		JobTimeout: durationEnv("SONIC_JOB_TIMEOUT", 10*time.Minute),
		MaxSteps:   int(floatEnv("SONIC_MAX_STEPS", 40)),
	}
}

// loadDotEnv sets KEY=VALUE pairs from a .env file without overriding the environment.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}

func floatEnv(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

func durationEnv(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
