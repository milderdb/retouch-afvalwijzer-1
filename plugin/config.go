package plugin

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func loadConfig(path string) Config {
	var c Config
	b, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}
func saveConfig(path string, c Config) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, b, 0o600)
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func str(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
func boolish(v any) bool { b, ok := v.(bool); return ok && b }
func atoiDefault(s string, d int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}
func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
func cleanLabel(s string) string { return strings.TrimSpace(strings.Join(strings.Fields(s), " ")) }
func shortType(s string) string {
	s = normalizeType(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	return s
}
