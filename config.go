package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config собирается из env. Все лимиты настраиваются, дефолты:
// файл — 10MB, суммарная квота — 1GB.
type Config struct {
	Port           string
	Dir            string
	ServerURL      string
	APIKeys        []string
	MaxFileSize    int64
	MaxStorageSize int64
}

func loadConfig() (Config, error) {
	maxFile, err := parseSize(envOr("MAX_FILE_SIZE", "10MB"))
	if err != nil {
		return Config{}, fmt.Errorf("bad MAX_FILE_SIZE: %w", err)
	}
	maxStorage, err := parseSize(envOr("MAX_STORAGE_SIZE", "1GB"))
	if err != nil {
		return Config{}, fmt.Errorf("bad MAX_STORAGE_SIZE: %w", err)
	}
	if maxFile <= 0 || maxStorage <= 0 {
		return Config{}, fmt.Errorf("limits must be positive")
	}
	return Config{
		Port:           envOr("PORT", "8080"),
		Dir:            envOr("FILES_DIRECTORY", "./data"),
		ServerURL:      strings.TrimSuffix(os.Getenv("SERVER_URL"), "/"),
		APIKeys:        splitKeys(os.Getenv("API_KEYS")),
		MaxFileSize:    maxFile,
		MaxStorageSize: maxStorage,
	}, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// "API_KEYS" — список через запятую: "key1,key2".
func splitKeys(s string) []string {
	var out []string
	for _, k := range strings.Split(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// parseSize понимает "1024", "10KB", "10MB", "1GB" (регистр не важен),
// дробные значения ("1.5GB") тоже.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	var mult int64 = 1
	for _, suf := range []struct {
		suffix string
		mult   int64
	}{
		{"GB", 1 << 30}, {"G", 1 << 30},
		{"MB", 1 << 20}, {"M", 1 << 20},
		{"KB", 1 << 10}, {"K", 1 << 10},
		{"B", 1},
	} {
		if strings.HasSuffix(s, suf.suffix) {
			mult = suf.mult
			s = strings.TrimSpace(strings.TrimSuffix(s, suf.suffix))
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q", s)
	}
	if f <= 0 {
		return 0, fmt.Errorf("size must be positive")
	}
	return int64(f * float64(mult)), nil
}
